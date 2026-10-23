// A seeded chaos monkey: the real QML app offscreen at a random size against a
// private wispd --mock with a flood of incoming messages and a ringing call,
// driven by random clicks, keys, typing, pastes, scrolls, resizes, calls and
// daemon restarts. Invariants are checked after every step, against the
// daemon every 50 steps, and for leaks and idle CPU at the end. A failure
// saves a screenshot and the last 30 steps to build/chaos/<seed>/.
//   SEED (default: random), STEPS (default 2000), WISP_BIN (default: bin/)
#include "chatlistmodel.h"
#include "conversation.h"
#include "emojimodel.h"
#include "harness.h"
#include "imageprovider.h"
#include "rpcclient.h"

#include <QClipboard>
#include <QColor>
#include <QDesktopServices>
#include <QDir>
#include <QElapsedTimer>
#include <QEventLoop>
#include <QFile>
#include <QFileInfo>
#include <QGuiApplication>
#include <QHash>
#include <QImage>
#include <QInputMethodEvent>
#include <QInputMethodQueryEvent>
#include <QJsonArray>
#include <QMutex>
#include <QPointer>
#include <QProcess>
#include <QQmlApplicationEngine>
#include <QQuickItem>
#include <QQuickWindow>
#include <QSet>
#include <QStandardPaths>
#include <QTemporaryDir>
#include <QTest>
#include <QTimer>
#include <QUrl>
#include <QtQml/QQmlExtensionPlugin>
#include <QtQuickTest/quicktest.h>

#include <algorithm>
#include <array>
#include <cmath>
#include <csignal>
#include <cstdio>
#include <functional>
#include <memory>
#include <random>
#include <string>

#include <fcntl.h>
#include <sys/resource.h>
#include <unistd.h>

Q_IMPORT_QML_PLUGIN(WispPlugin)

using namespace Qt::StringLiterals;
using harness::findView;
using harness::itemAt;
using harness::named;

namespace {

constexpr int kKeep = 30;          // steps saved with a failure
constexpr int kDeepEvery = 50;     // steps between checks against the daemon
constexpr int kLeakMark = 200;     // the step whose RSS the end is compared with
constexpr qint64 kLeakBytes = 30 << 20;
constexpr double kIdleCpu = 0.01;  // of one core, with nothing happening
constexpr qreal kNear = 48;        // how far from its click a menu may open
constexpr int kStepMs = 16;        // a frame after every step
constexpr int kScreenshots = 3;    // per kind of failure
constexpr int kPrinted = 10;       // failures printed per kind

const QStringList kDaemonArgs{u"--mock-traffic"_s, u"300ms"_s, u"--mock-call"_s, u"25s"_s};

// Warnings from Qt and QML, collected from any thread until the next check.
QMutex g_lock;
QStringList g_warnings;
QtMessageHandler g_print = nullptr;

void onMessage(QtMsgType type, const QMessageLogContext &ctx, const QString &msg)
{
    if (type == QtWarningMsg || type == QtCriticalMsg || type == QtFatalMsg) {
        const bool named = ctx.category && qstrcmp(ctx.category, "default") != 0;
        QMutexLocker lock(&g_lock);
        g_warnings << (named ? QString::fromLatin1(ctx.category) + ": "_L1 : QString()) + msg;
    }
    g_print(type, ctx, msg);
}

// The last steps, kept as plain bytes for the crash handler.
std::array<std::string, kKeep> g_recent;
int g_recentNext = 0;
std::string g_crashFile;

void put(int fd, const char *s, size_t n)
{
    while (n > 0) {
        const ssize_t w = ::write(fd, s, n);
        if (w <= 0)
            return;
        s += w;
        n -= size_t(w);
    }
}

void onCrash(int sig)
{
    const int fd = ::open(g_crashFile.c_str(), O_WRONLY | O_CREAT | O_TRUNC, 0644);
    const auto both = [fd](const char *s, size_t n) {
        put(2, s, n);
        if (fd >= 0)
            put(fd, s, n);
    };
    const char head[] = "chaos: crashed after these steps\n";
    both(head, sizeof head - 1);
    for (int i = 0; i < kKeep; ++i) {
        const std::string &line = g_recent[(g_recentNext + i) % kKeep];
        if (!line.empty()) {
            both(line.data(), line.size());
            both("\n", 1);
        }
    }
    if (fd >= 0)
        ::close(fd);
    std::signal(sig, SIG_DFL);
    std::raise(sig);
}

// As the chat list folds names for its search.
QString fold(const QString &s)
{
    QString out;
    for (QChar c : s.normalized(QString::NormalizationForm_D))
        if (c.category() != QChar::Mark_NonSpacing)
            out.append(c);
    return out.toCaseFolded();
}

QRectF sceneRect(const QQuickItem *item)
{
    return item->mapRectToScene(QRectF(0, 0, item->width(), item->height()));
}

bool shown(const QQuickItem *item)
{
    return item && item->isVisible() && item->opacity() > 0;
}

bool inside(const QRectF &r, const QRectF &bounds)
{
    return r.left() >= bounds.left() - 0.5 && r.top() >= bounds.top() - 0.5
        && r.right() <= bounds.right() + 0.5 && r.bottom() <= bounds.bottom() + 0.5;
}

qreal distance(const QRectF &r, QPointF p)
{
    const qreal dx = std::max({r.left() - p.x(), 0.0, p.x() - r.right()});
    const qreal dy = std::max({r.top() - p.y(), 0.0, p.y() - r.bottom()});
    return std::hypot(dx, dy);
}

QString str(const QRectF &r)
{
    return u"%1,%2 %3x%4"_s.arg(qRound(r.x())).arg(qRound(r.y())).arg(qRound(r.width())).arg(qRound(r.height()));
}

QString str(QPointF p)
{
    return u"%1,%2"_s.arg(qRound(p.x())).arg(qRound(p.y()));
}

// Text for the log: one line, at most 60 characters.
QString shorten(const QString &text)
{
    QString s = text;
    s.replace(u'\n', u'⏎');
    if (s.size() > 60)
        s = s.left(60) + u"…(%1 chars)"_s.arg(text.size());
    return u'"' + s + u'"';
}

// The QML type or objectName of o, or "" for Qt's own items.
QString typeName(const QObject *o)
{
    if (!o->objectName().isEmpty())
        return o->objectName();
    const QString c = QString::fromLatin1(o->metaObject()->className());
    for (const auto mark : {"_QMLTYPE_"_L1, "_QML_"_L1})
        if (const qsizetype i = c.indexOf(mark); i > 0)
            return c.left(i);
    return c.startsWith("QQuick"_L1) ? QString() : c;
}

QQuickItem *popupItem(const QObject *popup)
{
    for (const char *part : {"contentItem", "background"})
        if (auto *item = popup->property(part).value<QQuickItem *>(); item && item->parentItem())
            return item->parentItem();
    return nullptr;
}

QQuickItem *ancestor(QQuickItem *item, const char *type)
{
    for (QQuickItem *p = item ? item->parentItem() : nullptr; p; p = p->parentItem())
        if (p->inherits(type))
            return p;
    return nullptr;
}

qint64 rssBytes()
{
    QFile f(u"/proc/self/statm"_s);
    if (!f.open(QIODevice::ReadOnly))
        return 0;
    const QList<QByteArray> fields = f.readAll().split(' ');
    return fields.size() > 1 ? fields[1].toLongLong() * ::sysconf(_SC_PAGESIZE) : 0;
}

qint64 cpuMicros()
{
    rusage u{};
    ::getrusage(RUSAGE_SELF, &u);
    return (u.ru_utime.tv_sec + u.ru_stime.tv_sec) * 1000000LL + u.ru_utime.tv_usec + u.ru_stime.tv_usec;
}

const QStringList kWords{u"hi"_s, u"ok"_s, u"see you at 6"_s, u"lol"_s, u"sure"_s, u"on my way"_s,
                         u"Thanks!"_s, u"what's up?"_s, u"tomorrow"_s, u"haha"_s, u"Can you send it again"_s,
                         u"no worries"_s, u"brb"_s, u"Dinner?"_s, u"yes"_s, u"nope"_s, u"meeting at 10:30"_s};
const QStringList kEmoji{u"😀"_s, u"😂"_s, u"👍"_s, u"❤️"_s, u"🎉"_s, u"🔥"_s, u"🙏"_s, u"👍🏽"_s,
                         u"👩‍👩‍👧"_s, u"🇦🇺"_s, u"🫠"_s, u"☺️"_s, u"✌🏿"_s, u"🏳️‍🌈"_s};
const QStringList kCodes{u":sku"_s, u":thumbsup:"_s, u":heart"_s, u":joy"_s, u":+1:"_s, u":smile"_s,
                         u":fire:"_s, u":tada"_s, u":xx"_s, u":a"_s, u"::"_s, u":skull: :skull:"_s, u"a:sm"_s};
const QStringList kOdd{u"*bold* _it_ ~gone~ ```mono```"_s, u"<b>not bold</b> &amp; &lt;br&gt;"_s,
                       u"https://example.com/a?b=c&d=e"_s, u"a\u200bb\u200dc"_s, QChar(0x202e) + u"evil"_s,
                       u"مرحبا بالعالم"_s, u"שלום עולם"_s, u"日本語のテキスト"_s, u"Ünïcödé façade"_s,
                       u"%1 %s {0} ${x}"_s, u"\\n \\t"_s, u"'\"`"_s, u"🇦"_s, u"e\u0301\u0301\u0301"_s};

struct Shown {
    QObject *popup;
    QRectF rect;
    bool menu;
};

struct Failure {
    int step;
    QString kind, detail;
};

} // namespace

class Chaos : public QObject
{
    Q_OBJECT

public:
    Chaos(quint64 seed, int steps, const QString &dir, const QString &out)
        : m_seed(seed), m_steps(steps), m_dir(dir), m_out(out), m_rng(seed)
    {
    }
    ~Chaos() override;

    int run();

public slots:
    // Every URL the app opens lands here: nothing leaves the test.
    void openUrl(const QUrl &url);

private:
    struct Action {
        const char *name;
        int weight;
        void (Chaos::*run)();
    };

    bool setUp();
    bool startDaemon();
    QList<Action> actions();
    void finish();

    // The actions.
    void clickPoint();
    void clickRow();
    void rightClickMessage();
    void rightClickRow();
    void hoverMessage();
    void hoverRow();
    void keyEscape();
    void keyEnter();
    void keyShiftEnter();
    void keyArrows();
    void keyCtrlK();
    void typeRandom();
    void pasteText();
    void pasteImage();
    void replyCancel() { reply(false); }
    void replySend() { reply(true); }
    void reply(bool send);
    void attachMenu();
    void settings();
    void filterChip();
    void wheelMessages() { wheel(messageView()); }
    void wheelChats() { wheel(chatView()); }
    void wheel(QQuickItem *view);
    void resize();
    void call();
    void restartDaemon();

    // The checks.
    void checkStep();
    void checkComposer();
    void checkModels();
    void deepCheck();
    QList<std::pair<QString, QString>> compareChats(const QJsonArray &chats) const;
    QList<std::pair<QString, QString>> compareMessages(const QJsonArray &messages) const;
    void takeWarnings();
    void fail(const QString &kind, const QString &detail);
    qint64 measureRss();

    // Input.
    void click(QPointF at, Qt::MouseButton button = Qt::LeftButton);
    void key(int k, Qt::KeyboardModifiers mods = {}) { QTest::keyClick(m_window, Qt::Key(k), mods); }
    void typeText(const QString &text);

    // Finding things.
    QQuickItem *root() const { return m_window->contentItem(); }
    QQuickItem *chatView();
    QQuickItem *messageView();
    QList<std::pair<QQuickItem *, int>> visibleRows(QQuickItem *view);
    QList<Shown> visiblePopups() const;
    QList<QQuickItem *> floating();
    bool covered(QPointF p);
    QString describeAt(QPointF p);
    bool linked() const { return m_rpc->isConnected() && m_rpc->authState() == "paired"_L1; }
    QRectF windowRect() const { return QRectF(0, 0, m_window->width(), m_window->height()); }
    QPoint clamp(QPointF p) const;

    // Randomness.
    int below(int n) { return n <= 1 ? 0 : int(m_rng() % quint64(n)); }
    int between(int lo, int hi) { return lo + below(hi - lo + 1); }
    bool chance(int percent) { return below(100) < percent; }
    const QString &oneOf(const QStringList &l) { return l[below(int(l.size()))]; }
    QPointF pointIn(const QRectF &r) { return {r.left() + below(int(r.width())), r.top() + below(int(r.height()))}; }
    QString randomWords(int n);
    QString randomText();

    // Waiting.
    void frame(); // until the events are in and the items laid out
    bool waitFor(const std::function<bool()> &done, int ms);
    void idle(int ms);

    void note(const QString &detail);

    const quint64 m_seed;
    const int m_steps;
    const QString m_dir, m_out;
    std::mt19937_64 m_rng;
    int m_step = 0;
    QString m_action;
    QStringList m_recent;
    QList<Failure> m_failures;
    QHash<QString, int> m_kinds;
    qint64 m_rssMark = 0;
    qint64 m_eventTime = 0;
    QSize m_startSize;

    QProcess m_daemon;
    bool m_restarting = false;
    QQmlApplicationEngine *m_engine = nullptr;
    QQuickWindow *m_window = nullptr;
    RpcClient *m_rpc = RpcClient::instance();
    ChatListModel *m_chats = ChatListModel::instance();
    ConversationController *m_conv = ConversationController::instance();
    EmojiModel *m_emoji = nullptr;
    QPointer<QQuickItem> m_chatView, m_messageView, m_suggest;
};

Chaos::~Chaos()
{
    delete m_engine;
    m_restarting = true;
    m_daemon.terminate();
    m_daemon.waitForFinished();
}

void Chaos::openUrl(const QUrl &url)
{
    const QString line = u"%1/%2/opened/%3"_s.arg(m_seed).arg(m_step).arg(url.toString());
    std::printf("%s\n", qUtf8Printable(line));
    std::fflush(stdout);
}

void Chaos::note(const QString &detail)
{
    const QString line = u"%1/%2/%3/%4"_s.arg(m_seed).arg(m_step).arg(m_action, detail);
    std::printf("%s\n", qUtf8Printable(line));
    std::fflush(stdout);
    m_recent.append(line);
    if (m_recent.size() > kKeep)
        m_recent.removeFirst();
    g_recent[g_recentNext] = line.toStdString();
    g_recentNext = (g_recentNext + 1) % kKeep;
}

void Chaos::fail(const QString &kind, const QString &detail)
{
    const int n = ++m_kinds[kind];
    m_failures.append({m_step, kind, detail});
    const QString line = u"FAIL %1/%2 %3: %4"_s.arg(m_seed).arg(m_step).arg(kind, detail);
    if (n <= kPrinted) {
        std::printf("%s\n", qUtf8Printable(line));
        std::fflush(stdout);
    }
    if (n > kScreenshots)
        return;
    const QString base = m_out + u"/%1-%2"_s.arg(m_step, 4, 10, u'0').arg(kind);
    if (m_window)
        m_window->grabWindow().save(base + ".png"_L1);
    QFile f(base + ".txt"_L1);
    if (f.open(QIODevice::WriteOnly))
        f.write((line + "\n\nThe last steps:\n"_L1 + m_recent.join(u'\n') + u'\n').toUtf8());
}

void Chaos::takeWarnings()
{
    QStringList warnings;
    {
        QMutexLocker lock(&g_lock);
        warnings.swap(g_warnings);
    }
    for (const QString &w : std::as_const(warnings))
        fail(u"warning"_s, w);
}

bool Chaos::waitFor(const std::function<bool()> &done, int ms)
{
    QElapsedTimer clock;
    clock.start();
    while (!done()) {
        if (clock.elapsed() > ms)
            return false;
        QTest::qWait(10);
    }
    return true;
}

void Chaos::frame()
{
    QCoreApplication::processEvents();
    QQuickTest::qWaitForPolish(m_window, 1000);
}

// Waits in the event loop, without polling, so the app's own CPU use shows.
void Chaos::idle(int ms)
{
    QEventLoop loop;
    QTimer::singleShot(ms, &loop, &QEventLoop::quit);
    loop.exec();
}

QPoint Chaos::clamp(QPointF p) const
{
    // QTest moves a click at 0,0 to the window's centre.
    return {std::clamp(qRound(p.x()), 1, m_window->width() - 2), std::clamp(qRound(p.y()), 1, m_window->height() - 2)};
}

QQuickItem *Chaos::chatView()
{
    if (!m_chatView)
        m_chatView = findView(root(), m_chats);
    return m_chatView;
}

QQuickItem *Chaos::messageView()
{
    if (!m_messageView)
        m_messageView = findView(root(), m_conv->messages());
    return m_messageView;
}

// The delegates of view that show in it, with their rows.
QList<std::pair<QQuickItem *, int>> Chaos::visibleRows(QQuickItem *view)
{
    QList<std::pair<QQuickItem *, int>> rows;
    if (!shown(view))
        return rows;
    QMetaObject::invokeMethod(view, "forceLayout");
    const QRectF area = sceneRect(view).intersected(windowRect());
    const int count = view->property("count").toInt();
    bool seen = false; // the created delegates are one run of rows
    for (int row = 0; row < count; ++row) {
        QQuickItem *item = itemAt(view, row);
        if (!item) {
            if (seen)
                break;
            continue;
        }
        seen = true;
        if (item->isVisible() && sceneRect(item).intersects(area))
            rows.append({item, row});
    }
    return rows;
}

QList<Shown> Chaos::visiblePopups() const
{
    QList<Shown> out;
    const QList<QObject *> all = m_window->findChildren<QObject *>();
    for (QObject *o : all) {
        if (!o->inherits("QQuickPopup") || !o->property("visible").toBool())
            continue;
        if (QQuickItem *item = popupItem(o))
            out.append({o, sceneRect(item), o->inherits("QQuickMenu")});
    }
    return out;
}

// Items that float over the others like popups.
QList<QQuickItem *> Chaos::floating()
{
    if (!m_suggest && m_emoji)
        if (QQuickItem *list = findView(root(), m_emoji))
            m_suggest = list->parentItem();
    QList<QQuickItem *> items{m_suggest, named(root(), u"mediaPreview"_s), named(root(), u"mentionPicker"_s), m_window->findChild<QQuickItem *>(u"chevron"_s)};
    for (const char *part : {"callDecline", "callEnd"})
        if (QQuickItem *button = named(root(), QString::fromLatin1(part)))
            items << button->parentItem();
    items.removeAll(nullptr);
    return items;
}

// Whether something lies over the lists at p.
bool Chaos::covered(QPointF p)
{
    for (const Shown &s : visiblePopups())
        if (s.rect.contains(p))
            return true;
    for (QQuickItem *item : floating())
        if (shown(item) && sceneRect(item).contains(p))
            return true;
    if (QQuickItem *view = messageView(); shown(view)) {
        const QRectF list = sceneRect(view);
        if (QRectF(list.right() - 66, list.bottom() - 62, 50, 50).contains(p)) // the jump-to-newest button
            return true;
    }
    return false;
}

QString Chaos::describeAt(QPointF p)
{
    for (const Shown &s : visiblePopups())
        if (s.rect.contains(p))
            return typeName(s.popup).isEmpty() ? u"a popup"_s : typeName(s.popup);
    QString name;
    for (QQuickItem *item = root(); item;) {
        if (const QString t = typeName(item); !t.isEmpty())
            name = t;
        QQuickItem *next = nullptr;
        for (QQuickItem *child : item->childItems())
            if (shown(child) && sceneRect(child).contains(p) && (!next || child->z() >= next->z()))
                next = child;
        item = next;
    }
    return name.isEmpty() ? u"nothing"_s : name;
}

void Chaos::click(QPointF at, Qt::MouseButton button)
{
    const QPoint p = clamp(at);
    QHash<QObject *, QRectF> before;
    for (const Shown &s : visiblePopups())
        before.insert(s.popup, s.rect);
    QTest::mouseMove(m_window, p);
    QTest::mouseClick(m_window, button, {}, p);
    frame();
    // A menu this click opened (or moved) opens at it, flipped and clamped to fit.
    for (const Shown &s : visiblePopups()) {
        if (!s.menu || (before.contains(s.popup) && before.value(s.popup) == s.rect))
            continue;
        if (const qreal d = distance(s.rect, p); d > kNear)
            fail(u"popup-far"_s, u"%1 opened at %2, %3 px from the click at %4"_s
                     .arg(typeName(s.popup), str(s.rect)).arg(qRound(d)).arg(str(QPointF(p))));
    }
}

void Chaos::typeText(const QString &text)
{
    // Printable ASCII is typed as keys; the rest arrives as an input method
    // commits it, and only where one can (a text field).
    const auto ascii = [](QChar c) { return c.unicode() >= 0x20 && c.unicode() < 0x7f; };
    QString run;
    const auto flush = [&] {
        if (run.isEmpty())
            return;
        if (ascii(run.front())) {
            for (const QChar c : std::as_const(run))
                QTest::keyClick(m_window, char(c.unicode()));
        } else if (QObject *target = m_window->focusObject()) {
            QInputMethodQueryEvent query(Qt::ImEnabled);
            QCoreApplication::sendEvent(target, &query);
            if (query.value(Qt::ImEnabled).toBool()) {
                QInputMethodEvent commit;
                commit.setCommitString(run);
                QCoreApplication::sendEvent(target, &commit);
            }
        }
        run.clear();
    };
    for (const QChar c : text) {
        if (!run.isEmpty() && ascii(c) != ascii(run.front()))
            flush();
        run.append(c);
    }
    flush();
}

QString Chaos::randomWords(int n)
{
    QStringList words;
    for (int i = 0; i < n; ++i)
        words << oneOf(kWords);
    return words.join(u' ');
}

QString Chaos::randomText()
{
    switch (below(9)) {
    case 0: {
        QString s;
        for (int i = between(1, 6); i > 0; --i)
            s += oneOf(kEmoji) + (chance(30) ? u" "_s : QString());
        return s;
    }
    case 1:
        return (chance(50) ? randomWords(between(1, 3)) + u' ' : QString()) + oneOf(kCodes);
    case 2: { // a long line
        QString s;
        for (const int want = between(150, 500); s.size() < want;)
            s += oneOf(kWords) + u' ';
        return s;
    }
    case 3: // one long word
        return chance(50) ? QString(between(80, 300), u'x') : u"https://example.com/"_s + QString(between(80, 200), u'a');
    case 4:
        return oneOf(kOdd);
    default:
        return randomWords(between(1, 5));
    }
}

bool Chaos::startDaemon()
{
    const QString socket = harness::socketPath(m_dir);
    QFile::remove(socket); // left behind by a killed daemon
    harness::startMock(m_daemon, m_dir, kDaemonArgs);
    return m_daemon.waitForStarted() && waitFor([&] { return QFileInfo::exists(socket); }, 5000);
}

bool Chaos::setUp()
{
    QDir(m_out).removeRecursively();
    QDir().mkpath(m_out);
    g_crashFile = QFile::encodeName(m_out + "/crash.txt"_L1).toStdString();
    for (const int sig : {SIGSEGV, SIGABRT, SIGBUS, SIGFPE, SIGILL})
        std::signal(sig, onCrash);
    for (const char *scheme : {"file", "http", "https", "mailto", "tel"})
        QDesktopServices::setUrlHandler(QString::fromLatin1(scheme), this, "openUrl");

    m_daemon.setStandardOutputFile(QProcess::nullDevice());
    m_daemon.setStandardErrorFile(m_dir + "/wispd.log"_L1, QIODevice::Append);
    connect(&m_daemon, &QProcess::finished, this, [this](int code, QProcess::ExitStatus status) {
        if (m_restarting)
            return;
        fail(u"wispd-exit"_s, u"wispd exited (%1 %2); restarting it"_s
                 .arg(status == QProcess::CrashExit ? "crash"_L1 : "code"_L1).arg(code));
        QTimer::singleShot(0, this, [this] { startDaemon(); });
    });
    if (!startDaemon()) {
        std::printf("chaos: wispd did not start: %s\n", qUtf8Printable(m_daemon.errorString()));
        return false;
    }
    // "true" stands in for wispd, so the app never starts a daemon of its own.
    m_rpc->start({false, harness::socketPath(m_dir), QStandardPaths::findExecutable(u"true"_s), {}});

    m_engine = new QQmlApplicationEngine;
    m_engine->addImageProvider(u"wisp"_s, new ImageProvider);
    m_engine->loadFromModule("Wisp", "Main");
    m_window = m_engine->rootObjects().isEmpty() ? nullptr : qobject_cast<QQuickWindow *>(m_engine->rootObjects().constFirst());
    if (!m_window) {
        std::printf("chaos: the app did not load\n");
        return false;
    }
    m_emoji = m_engine->singletonInstance<EmojiModel *>("Wisp", "EmojiSearch");
    m_startSize = QSize(between(m_window->minimumWidth(), 1920), between(m_window->minimumHeight(), 1200));
    m_window->resize(m_startSize);
    m_window->requestActivate();
    if (!QTest::qWaitForWindowActive(m_window)
        || !waitFor([this] { return linked() && !m_chats->loading() && m_chats->count() > 0 && chatView(); }, 20000)) {
        std::printf("chaos: the chat list did not load\n");
        return false;
    }
    m_action = u"start"_s;
    note(u"%1x%2, wispd --mock %3"_s.arg(m_startSize.width()).arg(m_startSize.height()).arg(kDaemonArgs.join(u' ')));
    return true;
}

QList<Chaos::Action> Chaos::actions()
{
    const bool chat = !m_conv->jid().isEmpty() && shown(messageView());
    const bool ringing = shown(named(root(), u"callDecline"_s));
    const bool calling = shown(named(root(), u"callEnd"_s));
    return {
        {"click", 48, &Chaos::clickPoint},
        {"click-row", 36, &Chaos::clickRow},
        {"right-click-message", chat ? 20 : 0, &Chaos::rightClickMessage},
        {"right-click-row", 16, &Chaos::rightClickRow},
        {"hover-message", chat ? 24 : 0, &Chaos::hoverMessage},
        {"hover-row", 16, &Chaos::hoverRow},
        {"esc", 24, &Chaos::keyEscape},
        {"enter", 12, &Chaos::keyEnter},
        {"shift-enter", 8, &Chaos::keyShiftEnter},
        {"arrows", 16, &Chaos::keyArrows},
        {"ctrl-k", 8, &Chaos::keyCtrlK},
        {"type", 40, &Chaos::typeRandom},
        {"paste-text", 8, &Chaos::pasteText},
        {"paste-image", 6, &Chaos::pasteImage},
        {"reply-cancel", chat ? 12 : 0, &Chaos::replyCancel},
        {"reply-send", chat ? 12 : 0, &Chaos::replySend},
        {"attach", chat ? 12 : 0, &Chaos::attachMenu},
        {"settings", 8, &Chaos::settings},
        {"filter", 12, &Chaos::filterChip},
        {"wheel-messages", chat ? 24 : 0, &Chaos::wheelMessages},
        {"wheel-chats", 16, &Chaos::wheelChats},
        {"resize", 10, &Chaos::resize},
        {"call", ringing ? 80 : calling ? 8 : 0, &Chaos::call},
        {"restart-wispd", m_step > 20 ? 1 : 0, &Chaos::restartDaemon},
    };
}

int Chaos::run()
{
    if (!setUp())
        return 2;
    m_step = 0;
    takeWarnings();
    for (m_step = 1; m_step <= m_steps; ++m_step) {
        const QList<Action> all = actions();
        int total = 0;
        for (const Action &a : all)
            total += a.weight;
        int r = below(total);
        for (const Action &a : all) {
            if ((r -= a.weight) >= 0)
                continue;
            m_action = QString::fromLatin1(a.name);
            (this->*a.run)();
            break;
        }
        QTest::qWait(kStepMs);
        frame();
        checkStep();
        if (m_step % kDeepEvery == 0)
            deepCheck();
        if (m_step == std::min(kLeakMark, m_steps))
            m_rssMark = measureRss();
    }
    m_step = m_steps;
    finish();

    std::printf("chaos seed %llu: %d steps, %d failures\n", static_cast<unsigned long long>(m_seed), m_steps,
                int(m_failures.size()));
    QStringList kinds = m_kinds.keys();
    kinds.sort();
    for (const QString &kind : std::as_const(kinds)) {
        const auto first = std::find_if(m_failures.cbegin(), m_failures.cend(), [&](const Failure &f) { return f.kind == kind; });
        std::printf("  %s x%d, first at step %d: %s\n", qUtf8Printable(kind), m_kinds.value(kind), first->step,
                    qUtf8Printable(first->detail));
    }
    if (!m_failures.isEmpty())
        std::printf("  details in %s\n", qUtf8Printable(m_out));
    std::fflush(stdout);
    return m_failures.isEmpty() ? 0 : 1;
}

// Resized to the start size and settled, so two measurements compare.
qint64 Chaos::measureRss()
{
    const QString saved = m_action;
    m_action = u"measure"_s;
    m_window->resize(m_startSize);
    QTest::qWait(300);
    m_engine->collectGarbage();
    QCoreApplication::sendPostedEvents(nullptr, QEvent::DeferredDelete);
    QTest::qWait(100);
    const qint64 rss = rssBytes();
    note(u"RSS %1 MB at %2x%3"_s.arg(rss / double(1 << 20), 0, 'f', 1).arg(m_startSize.width()).arg(m_startSize.height()));
    m_action = saved;
    return rss;
}

void Chaos::finish()
{
    m_action = u"end"_s;
    note(u"hang up, pause wispd, 5 s quiet"_s);
    for (const char *button : {"callEnd", "callDecline"})
        if (QQuickItem *item = named(root(), QString::fromLatin1(button)); shown(item))
            click(sceneRect(item).center());
    waitFor([this] { return !shown(named(root(), u"callEnd"_s)) && !shown(named(root(), u"callDecline"_s)); }, 3000);
    takeWarnings();

    const qint64 rss = measureRss();
    if (m_rssMark > 0 && rss - m_rssMark > kLeakBytes)
        fail(u"leak"_s, u"RSS grew %1 MB since step %2"_s.arg((rss - m_rssMark) / double(1 << 20), 0, 'f', 1)
                            .arg(std::min(kLeakMark, m_steps)));

    const qint64 pid = m_daemon.processId();
    if (pid > 0)
        ::kill(pid_t(pid), SIGSTOP); // no more traffic
    idle(5000);
    const qint64 rssQuiet = rssBytes();
    const qint64 cpu = cpuMicros();
    QElapsedTimer clock;
    clock.start();
    idle(3000);
    const double used = double(cpuMicros() - cpu) / (clock.nsecsElapsed() / 1000.0);
    note(u"idle CPU %1%, RSS %2 MB"_s.arg(used * 100, 0, 'f', 2).arg(rssQuiet / double(1 << 20), 0, 'f', 1));
    if (used > kIdleCpu)
        fail(u"idle-cpu"_s, u"%1% of a core with nothing happening"_s.arg(used * 100, 0, 'f', 2));
    if (pid > 0)
        ::kill(pid_t(pid), SIGCONT);
    takeWarnings();

    // Quitting must be quiet too.
    note(u"quit"_s);
    delete m_engine;
    m_engine = nullptr;
    m_window = nullptr;
    QCoreApplication::sendPostedEvents(nullptr, QEvent::DeferredDelete);
    takeWarnings();
}

// --- the actions ---

void Chaos::clickPoint()
{
    const QPointF p(below(m_window->width()), below(m_window->height()));
    const bool right = chance(15);
    note(u"%1 %2 on %3"_s.arg(right ? "right"_L1 : "left"_L1, str(p), describeAt(p)));
    click(p, right ? Qt::RightButton : Qt::LeftButton);
}

void Chaos::clickRow()
{
    const auto rows = visibleRows(chatView());
    if (rows.isEmpty()) {
        note(u"skip: no rows"_s);
        return;
    }
    const auto [item, row] = rows[below(int(rows.size()))];
    const QPointF p = pointIn(sceneRect(item).intersected(sceneRect(chatView())));
    note(u"row %1 %2 at %3"_s.arg(row).arg(shorten(item->property("name").toString()), str(p)));
    click(p);
}

void Chaos::rightClickRow()
{
    const auto rows = visibleRows(chatView());
    if (rows.isEmpty()) {
        note(u"skip: no rows"_s);
        return;
    }
    const auto [item, row] = rows[below(int(rows.size()))];
    const QPointF p = pointIn(sceneRect(item).intersected(sceneRect(chatView())));
    note(u"row %1 %2 at %3"_s.arg(row).arg(shorten(item->property("name").toString()), str(p)));
    click(p, Qt::RightButton);
}

void Chaos::rightClickMessage()
{
    QQuickItem *view = messageView();
    const auto rows = visibleRows(view);
    if (rows.isEmpty()) {
        note(u"skip: no messages"_s);
        return;
    }
    const auto [item, row] = rows[below(int(rows.size()))];
    QQuickItem *bubble = item->property("bubbleItem").value<QQuickItem *>();
    const QRectF r = sceneRect(shown(bubble) ? bubble : item).intersected(sceneRect(view));
    if (r.isEmpty()) {
        note(u"skip: row %1 is hidden"_s.arg(row));
        return;
    }
    const QPointF p = pointIn(r);
    note(u"row %1 (%2) at %3"_s.arg(row).arg(item->property("type").toString(), str(p)));
    click(p, Qt::RightButton);
}

void Chaos::hoverMessage()
{
    QQuickItem *view = messageView();
    QList<std::pair<QQuickItem *, QRectF>> bubbles;
    for (const auto &[item, row] : visibleRows(view)) {
        QQuickItem *bubble = item->property("bubbleItem").value<QQuickItem *>();
        if (shown(bubble) && !item->property("isSystem").toBool() && !item->property("isDeleted").toBool()
                && !item->property("bare").toBool())
            if (const QRectF r = sceneRect(bubble).intersected(sceneRect(view)).adjusted(2, 2, -2, -2); r.isValid())
                bubbles.append({item, r});
    }
    if (bubbles.isEmpty()) {
        note(u"skip: no bubbles"_s);
        return;
    }
    const auto [item, r] = bubbles[below(int(bubbles.size()))];
    const QPointF p = pointIn(r);
    const bool press = chance(70);
    note(u"%1 at %2%3"_s.arg(item->property("msgId").toString(), str(p), press ? ", click the chevron"_L1 : ""_L1));
    const bool expect = visiblePopups().isEmpty() && !covered(p) && !view->property("moving").toBool();
    QTest::mouseMove(m_window, clamp(QPointF(r.left(), p.y())));
    QTest::mouseMove(m_window, clamp(p));
    QQuickItem *chevron = m_window->findChild<QQuickItem *>(u"chevron"_s);
    if (expect && chevron && r.height() + 4 < chevron->height() + 6) {
        if (shown(chevron))
            fail(u"chevron"_s, u"chevron shown on a clipped sliver of message %1"_s.arg(item->property("msgId").toString()));
        return;
    }
    if (!shown(chevron)) {
        if (expect)
            fail(u"chevron"_s, u"no chevron while hovering message %1 at %2"_s.arg(item->property("msgId").toString(), str(p)));
        return;
    }
    if (press)
        click(sceneRect(chevron).center());
}

void Chaos::hoverRow()
{
    QQuickItem *view = chatView();
    const auto rows = visibleRows(view);
    if (rows.isEmpty()) {
        note(u"skip: no rows"_s);
        return;
    }
    const auto [item, row] = rows[below(int(rows.size()))];
    const QRectF r = sceneRect(item).intersected(sceneRect(view)).adjusted(1, 1, -1, -1);
    const QPointF p = pointIn(r);
    const bool press = chance(70);
    note(u"row %1 %2 at %3%4"_s.arg(row).arg(shorten(item->property("name").toString()), str(p),
                                           press ? ", click the chevron"_L1 : ""_L1));
    const bool expect = visiblePopups().isEmpty() && !covered(p) && !view->property("moving").toBool();
    QTest::mouseMove(m_window, clamp(QPointF(r.left(), p.y())));
    QTest::mouseMove(m_window, clamp(p));
    QQuickItem *chevron = named(item, u"rowMenuButton"_s);
    if (!shown(chevron)) {
        if (expect)
            fail(u"row-chevron"_s, u"no chevron while hovering row %1 at %2"_s.arg(row).arg(str(p)));
        return;
    }
    if (press)
        click(sceneRect(chevron).center());
}

void Chaos::keyEscape()
{
    note(u"Esc"_s);
    key(Qt::Key_Escape);
}

void Chaos::keyEnter()
{
    note(u"Enter"_s);
    key(Qt::Key_Return);
}

void Chaos::keyShiftEnter()
{
    note(u"Shift+Enter"_s);
    key(Qt::Key_Return, Qt::ShiftModifier);
}

void Chaos::keyArrows()
{
    const bool up = chance(50);
    const int n = between(1, 5);
    note(u"%1 x%2"_s.arg(up ? "Up"_L1 : "Down"_L1).arg(n));
    for (int i = 0; i < n; ++i)
        key(up ? Qt::Key_Up : Qt::Key_Down);
}

void Chaos::keyCtrlK()
{
    note(u"Ctrl+K"_s);
    key(Qt::Key_K, Qt::ControlModifier);
}

void Chaos::typeRandom()
{
    const QString text = randomText();
    const QObject *focus = m_window->focusObject();
    const QString target = !focus ? u"nothing"_s : typeName(focus).isEmpty() ? QString::fromLatin1(focus->metaObject()->className()) : typeName(focus);
    note(u"%1 into %2"_s.arg(shorten(text), target));
    typeText(text);
}

void Chaos::pasteText()
{
    QString text;
    switch (below(4)) {
    case 0: // many lines
        for (int i = between(2, 30); i > 0; --i)
            text += randomWords(between(1, 8)) + u'\n';
        break;
    case 1: // a lot
        while (text.size() < 8000)
            text += randomWords(10) + (chance(10) ? u"\n"_s : u" "_s);
        break;
    case 2:
        text = oneOf(kOdd) + u' ' + oneOf(kEmoji);
        break;
    default:
        text = randomWords(between(1, 6));
    }
    note(u"%1, %2 lines"_s.arg(shorten(text)).arg(text.count(u'\n') + 1));
    QGuiApplication::clipboard()->setText(text);
    key(Qt::Key_V, Qt::ControlModifier);
}

void Chaos::pasteImage()
{
    const QSize size(between(1, 1600), between(1, 1200));
    QImage image(size, QImage::Format_RGB32);
    image.fill(QColor::fromRgb(QRgb(m_rng() & 0xffffff)));
    note(u"%1x%2 image"_s.arg(size.width()).arg(size.height()));
    QGuiApplication::clipboard()->setImage(image);
    key(Qt::Key_V, Qt::ControlModifier);
}

// Reply from the message menu, by click or keys, then cancel (Esc or the
// quote's close button) or type and send.
void Chaos::reply(bool send)
{
    QQuickItem *view = messageView();
    QList<std::pair<QQuickItem *, QRectF>> targets;
    for (const auto &[item, row] : visibleRows(view)) {
        QQuickItem *bubble = item->property("bubbleItem").value<QQuickItem *>();
        if (shown(bubble) && !item->property("isSystem").toBool() && !item->property("isDeleted").toBool())
            if (const QRectF r = sceneRect(bubble).intersected(sceneRect(view)).adjusted(1, 1, -1, -1); r.isValid())
                targets.append({item, r});
    }
    if (targets.isEmpty() || m_conv->readOnly()) {
        note(m_conv->readOnly() ? u"skip: read-only chat"_s : u"skip: no messages"_s);
        return;
    }
    const auto [item, r] = targets[below(int(targets.size()))];
    const QString id = item->property("msgId").toString();
    const QPointF p = pointIn(r);
    const bool keys = chance(50);
    const bool button = chance(50);
    const QString text = u" "_s + randomWords(between(1, 4));
    note(u"%1 at %2 by %3, then %4"_s.arg(id, str(p), keys ? "keys"_L1 : "click"_L1,
                                          send ? "send " + shorten(text) : button ? "close the quote"_L1 : "Esc"_L1));
    const bool free = visiblePopups().isEmpty() && !covered(p);
    if (!free) {
        note(u"skip: message covered by another popup"_s);
        return;
    }
    click(p, Qt::RightButton);
    QObject *menu = m_window->findChild<QObject *>(u"messageMenu"_s);
    if (!menu || !waitFor([menu] { return menu->property("visible").toBool(); }, 500)) {
        if (free)
            fail(u"message-menu"_s, u"right-clicking message %1 at %2 opened no menu"_s.arg(id, str(p)));
        return;
    }
    frame();
    QQuickItem *entry = nullptr;
    QMetaObject::invokeMethod(menu, "itemAt", Q_RETURN_ARG(QQuickItem *, entry), Q_ARG(int, 0));
    const QString where = entry ? u"Reply at %1"_s.arg(str(sceneRect(entry))) : u"no Reply"_s;
    if (keys) {
        for (int n = 0; menu->property("currentIndex").toInt() > 0 && n < menu->property("count").toInt(); ++n)
            key(Qt::Key_Up);
        if (menu->property("currentIndex").toInt() < 0)
            key(Qt::Key_Down);
        if (menu->property("currentIndex").toInt() != 0) {
            fail(u"reply"_s, u"could not select Reply with the arrow keys"_s);
            return;
        }
        key(Qt::Key_Return);
    } else if (entry) {
        click(sceneRect(entry).center());
    }
    if (!waitFor([&] { return m_conv->replyTo().toMap().value(u"id"_s).toString() == id; }, 500)) {
        fail(u"reply"_s, u"choosing Reply on %1 by %2 started no reply (%3)"_s.arg(id, keys ? "keys"_L1 : "click"_L1, where));
        return;
    }
    if (send) {
        typeText(text);
        key(Qt::Key_Return);
        QCoreApplication::processEvents();
        if (!m_conv->replyTo().toMap().isEmpty())
            fail(u"reply"_s, u"the quote stayed after sending the reply to %1"_s.arg(id));
    } else {
        QQuickItem *close = named(root(), u"cancelReply"_s);
        const bool esc = !button || !shown(close);
        const bool suggesting = shown(m_suggest); // then Esc closes the emoji suggestions first
        if (esc)
            key(Qt::Key_Escape);
        else
            click(sceneRect(close).center());
        if (!m_conv->replyTo().toMap().isEmpty() && !(esc && suggesting))
            fail(u"reply"_s, u"the reply to %1 was not cancelled"_s.arg(id));
    }
}

void Chaos::attachMenu()
{
    QQuickItem *button = named(root(), u"attachButton"_s);
    if (!shown(button) || !button->isEnabled()) {
        note(u"skip: no attach button"_s);
        return;
    }
    const int how = below(3);
    const QPointF elsewhere(below(m_window->width()), below(m_window->height()));
    note(how == 0 ? u"open, Esc"_s : how == 1 ? u"open, the button again"_s : u"open, click "_s + str(elsewhere));
    click(sceneRect(button).center());
    if (how == 0)
        key(Qt::Key_Escape);
    else if (how == 1)
        click(sceneRect(button).center());
    else
        click(elsewhere);
}

void Chaos::settings()
{
    const bool open = m_window->property("showSettings").toBool();
    const QString section = open || chance(50) ? QString() : chance(50) ? u"settingsNotifications"_s : u"settingsPrivacy"_s;
    note(open ? u"close"_s : section.isEmpty() ? u"open"_s : u"open, then "_s + section);
    QQuickItem *button = named(root(), open ? u"navChats"_s : u"navSettings"_s);
    if (!shown(button))
        return;
    click(sceneRect(button).center());
    if (QQuickItem *entry = section.isEmpty() ? nullptr : named(root(), section); shown(entry))
        click(sceneRect(entry).center());
}

void Chaos::filterChip()
{
    static const QStringList chips{u"filterAll"_s, u"filterUnread"_s, u"filterGroups"_s};
    const QString chip = oneOf(chips);
    note(chip);
    if (QQuickItem *item = named(root(), chip); shown(item))
        click(sceneRect(item).center());
}

void Chaos::wheel(QQuickItem *view)
{
    if (!shown(view)) {
        note(u"skip: no list"_s);
        return;
    }
    const QRectF area = sceneRect(view).intersected(windowRect());
    const QPoint p = clamp(pointIn(area));
    const bool pixels = chance(30); // a touchpad
    QList<int> deltas;
    int total = 0;
    for (int i = between(1, 12); i > 0; --i) {
        const int d = (chance(50) ? 1 : -1) * (pixels ? between(1, 200) : 120 * between(1, 10));
        deltas << d;
        total += d;
    }
    note(u"%1 %2 events at %3, total %4"_s.arg(deltas.size()).arg(pixels ? "pixel"_L1 : "wheel"_L1, str(QPointF(p))).arg(total));
    for (qsizetype i = 0; i < deltas.size(); ++i) {
        const Qt::ScrollPhase phase = !pixels ? Qt::NoScrollPhase : i == 0 ? Qt::ScrollBegin : Qt::ScrollUpdate;
        QWheelEvent e(p, m_window->mapToGlobal(QPointF(p)), pixels ? QPoint(0, deltas[i]) : QPoint(),
                      QPoint(0, pixels ? deltas[i] * 2 : deltas[i]), Qt::NoButton, {}, phase, false);
        e.setTimestamp(ulong(m_eventTime += between(1, 8)));
        QGuiApplication::sendEvent(m_window, &e);
        QCoreApplication::processEvents();
    }
    if (pixels) {
        QWheelEvent end(p, m_window->mapToGlobal(QPointF(p)), QPoint(), QPoint(), Qt::NoButton, {}, Qt::ScrollEnd, false);
        end.setTimestamp(ulong(m_eventTime += 1));
        QGuiApplication::sendEvent(m_window, &end);
    }
}

void Chaos::resize()
{
    const QSize min = m_window->minimumSize();
    const QSize size = chance(15) ? min : QSize(between(min.width(), 1920), between(min.height(), 1200));
    note(u"%1x%2"_s.arg(size.width()).arg(size.height()));
    m_window->resize(size);
}

void Chaos::call()
{
    QQuickItem *accept = named(root(), u"callAccept"_s), *decline = named(root(), u"callDecline"_s);
    if (shown(decline)) {
        const bool yes = shown(accept) && chance(50);
        note(yes ? u"accept"_s : u"decline"_s);
        click(sceneRect(yes ? accept : decline).center());
        return;
    }
    QQuickItem *end = named(root(), u"callEnd"_s), *mute = named(root(), u"callMute"_s);
    if (!shown(end))
        return;
    const int what = below(3);
    if (what == 0 && shown(mute)) {
        note(u"mute"_s);
        click(sceneRect(mute).center());
    } else if (what == 1) {
        note(u"open the chat"_s);
        click(sceneRect(end->parentItem()).topLeft() + QPointF(24, 28));
    } else {
        note(u"end"_s);
        click(sceneRect(end).center());
    }
}

void Chaos::restartDaemon()
{
    const bool hard = chance(50);
    note(hard ? u"SIGKILL, start again"_s : u"SIGTERM, start again"_s);
    m_restarting = true;
    if (hard)
        m_daemon.kill();
    else
        m_daemon.terminate();
    if (!m_daemon.waitForFinished(5000)) {
        m_daemon.kill();
        m_daemon.waitForFinished();
    }
    m_restarting = false;
    if (!startDaemon()) {
        fail(u"wispd-start"_s, m_daemon.errorString());
        return;
    }
    waitFor([this] { return !m_rpc->isConnected(); }, 2000);
    if (!waitFor([this] { return linked() && !m_chats->loading(); }, 15000))
        fail(u"reconnect"_s, u"not back 15 s after restarting wispd"_s);
}

// --- the checks ---

void Chaos::checkStep()
{
    takeWarnings();
    const QRectF window = windowRect();
    for (const Shown &s : visiblePopups())
        if (!inside(s.rect, window))
            fail(u"popup-outside"_s, u"%1 at %2 is outside the %3x%4 window"_s
                     .arg(typeName(s.popup), str(s.rect)).arg(m_window->width()).arg(m_window->height()));
    for (QQuickItem *item : floating())
        if (shown(item) && !inside(sceneRect(item), window))
            fail(u"popup-outside"_s, u"%1 at %2 is outside the %3x%4 window"_s
                     .arg(typeName(item), str(sceneRect(item))).arg(m_window->width()).arg(m_window->height()));
    checkComposer();
}

// The composer's field, and its text where the cursor is, show in full.
void Chaos::checkComposer()
{
    QQuickItem *composer = m_conv->jid().isEmpty() ? nullptr : named(root(), u"composer"_s);
    if (!shown(composer))
        return;
    QQuickItem *field = named(composer, u"composerField"_s);
    QQuickItem *input = named(composer, u"composerInput"_s);
    if (!field || !input) {
        fail(u"composer"_s, u"the composer has no field or input"_s);
        return;
    }
    QQuickItem *viewport = ancestor(input, "QQuickScrollView");
    if (!viewport)
        viewport = field;
    const QRectF c = sceneRect(composer), f = sceneRect(field), v = sceneRect(viewport);
    QStringList bad;
    if (!inside(f, c))
        bad << u"the field %1 is outside the composer %2"_s.arg(str(f), str(c));
    if (!inside(f, windowRect()))
        bad << u"the field %1 is outside the window"_s.arg(str(f));
    if (!inside(v, f))
        bad << u"the text area %1 is outside the field %2"_s.arg(str(v), str(f));
    if (const qreal line = field->property("lineH").toReal(); v.height() + 0.5 < line)
        bad << u"the text area is %1 px high, under a line (%2)"_s.arg(v.height()).arg(line);
    if (viewport != field && input->width() > viewport->width() + 0.5)
        bad << u"the text is %1 px wide in a %2 px area"_s.arg(input->width()).arg(viewport->width());
    if (input->hasActiveFocus()) {
        const QRectF cursor = input->mapRectToScene(input->property("cursorRectangle").toRectF());
        if (!inside(cursor, v.adjusted(-1, -1, 1, 1)))
            bad << u"the cursor %1 is out of the text area %2"_s.arg(str(cursor), str(v));
    }
    if (!bad.isEmpty())
        fail(u"composer"_s, bad.join("; "_L1));
}

void Chaos::checkModels()
{
    const MessageModel *messages = m_conv->messages();
    QSet<QString> ids;
    for (int row = 0; row < messages->rowCount(); ++row) {
        const MessageModel::Msg &m = messages->at(row);
        if (ids.contains(m.id))
            fail(u"messages-duplicate"_s, u"%1 is in the open chat twice (row %2)"_s.arg(m.id).arg(row));
        ids.insert(m.id);
        if (row > 0 && messages->at(row - 1).timestamp < m.timestamp)
            fail(u"messages-order"_s, u"row %1 (%2) is newer than row %3 (%4)"_s
                     .arg(row).arg(m.timestamp).arg(row - 1).arg(messages->at(row - 1).timestamp));
    }

    QSet<QString> jids;
    const ChatListModel::Chat *prev = nullptr;
    for (int row = 0; row < m_chats->rowCount(); ++row) {
        const QString jid = m_chats->index(row).data(ChatListModel::JidRole).toString();
        if (jids.contains(jid))
            fail(u"chats-duplicate"_s, u"%1 is in the chat list twice (row %2)"_s.arg(jid).arg(row));
        jids.insert(jid);
        const ChatListModel::Chat *c = m_chats->chat(jid);
        if (prev && c && (prev->pinned < c->pinned || (prev->pinned == c->pinned && prev->lastActivity < c->lastActivity)))
            fail(u"chats-order"_s, u"row %1 %2 (pinned %3, active %4) belongs above %5 (pinned %6, active %7)"_s
                     .arg(row).arg(shorten(c->name)).arg(c->pinned).arg(c->lastActivity)
                     .arg(shorten(prev->name)).arg(prev->pinned).arg(prev->lastActivity));
        prev = c;
    }
}

QList<std::pair<QString, QString>> Chaos::compareChats(const QJsonArray &chats) const
{
    QList<std::pair<QString, QString>> issues;
    const QString folded = fold(m_chats->filterText().trimmed());
    QStringList want;
    QStringList unread;
    QString openName;
    for (const QJsonValue &v : chats) {
        const QJsonObject o = v.toObject();
        const QString jid = o["jid"_L1].toString();
        QString name = o["name"_L1].toString();
        if (name.isEmpty())
            name = jid.section(u'@', 0, 0);
        const int count = std::max(o["unread"_L1].toInt(), o["markedUnread"_L1].toBool() ? 1 : 0);
        if (jid == m_conv->jid())
            openName = name;
        const ChatListModel::Chat *c = m_chats->chat(jid);
        if (c && c->unread != count)
            unread << u"%1 shows %2, the daemon has %3"_s.arg(shorten(name)).arg(c->unread).arg(count);
        if ((m_chats->filter() == ChatListModel::Unread && count == 0)
            || (m_chats->filter() == ChatListModel::Groups && o["kind"_L1].toString() != "group"_L1)
            || (!folded.isEmpty() && !fold(name).contains(folded) && !jid.startsWith(folded)))
            continue;
        want << jid;
    }
    if (!unread.isEmpty())
        issues.append({u"unread"_s, unread.join("; "_L1)});

    QStringList have;
    for (int row = 0; row < m_chats->rowCount(); ++row)
        have << m_chats->index(row).data(ChatListModel::JidRole).toString();
    if (have != want) {
        const QSet<QString> h(have.cbegin(), have.cend()), w(want.cbegin(), want.cend());
        QStringList extra = (h - w).values(), missing = (w - h).values();
        extra.sort();
        missing.sort();
        qsizetype at = 0;
        while (at < have.size() && at < want.size() && have[at] == want[at])
            ++at;
        issues.append({u"chats-daemon"_s, u"%1 rows, the daemon has %2 (filter %3 %4); first difference at row %5; extra %6; missing %7"_s
                           .arg(have.size()).arg(want.size()).arg(int(m_chats->filter())).arg(shorten(m_chats->filterText()))
                           .arg(at).arg(extra.join(u','), missing.join(u','))});
    }

    if (!m_conv->jid().isEmpty()) {
        QQuickItem *header = named(root(), u"chatName"_s);
        const QString shownName = header ? header->property("text").toString() : QString();
        const ChatListModel::Chat *c = m_chats->chat(m_conv->jid());
        const QString listed = c ? c->name : QString();
        if (shownName != m_conv->name() || (c && shownName != listed) || (!openName.isEmpty() && shownName != openName))
            issues.append({u"header"_s, u"the header shows %1 for %2; the chat list has %3, the daemon %4"_s
                               .arg(shorten(shownName), m_conv->jid(), shorten(listed), shorten(openName))});
    }
    return issues;
}

QList<std::pair<QString, QString>> Chaos::compareMessages(const QJsonArray &messages) const
{
    const MessageModel *model = m_conv->messages();
    QStringList have, want;
    for (const QJsonValue &v : messages)
        want << v["id"_L1].toString();
    for (int row = 0; row < model->rowCount() && row < want.size(); ++row)
        have << model->at(row).id;
    if (have == want.mid(0, have.size()) && have.size() == std::min<qsizetype>(want.size(), model->rowCount()))
        return {};
    qsizetype at = 0;
    while (at < have.size() && at < want.size() && have[at] == want[at])
        ++at;
    return {{u"messages-daemon"_s, u"the open chat %1 differs from the daemon's newest %2 at row %3: %4, the daemon %5"_s
                                       .arg(m_conv->jid()).arg(want.size()).arg(at)
                                       .arg(have.value(at, u"none"_s), want.value(at, u"none"_s))}};
}

// Every 50 steps, settled: the models' own order, then the chat list, the
// unread counts, the header and the open chat against the daemon. A
// difference must last three tries, so traffic in flight doesn't count.
void Chaos::deepCheck()
{
    waitFor([this] { return !m_chats->loading() && !m_conv->loading(); }, 3000);
    checkModels();
    if (!linked())
        return;
    QList<std::pair<QString, QString>> issues;
    for (int attempt = 0; attempt < 3; ++attempt) {
        auto result = std::make_shared<std::pair<bool, QList<std::pair<QString, QString>>>>();
        m_rpc->call(u"chats.list"_s, {{u"limit"_s, 1000}}, [this, result](const QJsonValue &r, const QJsonObject &e) {
            result->first = true;
            if (e.isEmpty())
                result->second = compareChats(r["chats"_L1].toArray());
        });
        const QString jid = m_conv->jid();
        const int rows = std::min(m_conv->messages()->rowCount(), 200);
        auto messages = std::make_shared<std::pair<bool, QList<std::pair<QString, QString>>>>();
        messages->first = jid.isEmpty() || rows == 0 || m_conv->loading();
        if (!messages->first)
            m_rpc->call(u"messages.list"_s, {{u"chat"_s, jid}, {u"limit"_s, rows}},
                        [this, jid, messages](const QJsonValue &r, const QJsonObject &e) {
                            messages->first = true;
                            if (e.isEmpty() && m_conv->jid() == jid && !m_conv->loading())
                                messages->second = compareMessages(r["messages"_L1].toArray());
                        });
        if (!waitFor([&] { return result->first && messages->first; }, 5000)) {
            fail(u"daemon"_s, u"no answer to chats.list or messages.list in 5 s"_s);
            return;
        }
        issues = result->second + messages->second;
        if (issues.isEmpty())
            return;
        QTest::qWait(100);
    }
    for (const auto &[kind, detail] : std::as_const(issues))
        fail(kind, detail);
}

int main(int argc, char *argv[])
{
    const quint64 seed = qEnvironmentVariableIsSet("SEED") ? qEnvironmentVariable("SEED").toULongLong()
                                                           : std::random_device{}() % 100000;
    const int steps = qEnvironmentVariableIsSet("STEPS") ? qEnvironmentVariableIntValue("STEPS") : 2000;
    const QString out = QDir::cleanPath(QStringLiteral(WISP_SOURCE_DIR "/../build/chaos/") + QString::number(seed));
    qputenv("WISP_BIN", QFile::encodeName(harness::binDir())); // absolute, before the chdir below

    QTemporaryDir dir(harness::scratchRoot() + "/wisp-chaos-XXXXXX"_L1);
    harness::isolate(dir.path());
    QDir::setCurrent(dir.path()); // where a file dialog opens
    g_print = qInstallMessageHandler(onMessage);

    QGuiApplication app(argc, argv);
    app.setApplicationName(u"wisp"_s);
    app.setOrganizationName(u"wisp"_s);
    harness::useAppStyle();

    std::printf("chaos seed %llu, %d steps\n", static_cast<unsigned long long>(seed), steps);
    std::fflush(stdout);
    Chaos chaos(seed, steps, dir.path(), out);
    return chaos.run();
}

#include "chaos.moc"
