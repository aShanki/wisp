// Drives the real QML app offscreen against a private wispd --mock: keys,
// mouse and clipboard in; the models and daemon events are checked.
// WISP_BIN is the directory with wispd (default: the repo's bin/).
#include "chatlistmodel.h"
#include "conversation.h"
#include "emojimodel.h"
#include "format.h"
#include "harness.h"
#include "imageprovider.h"
#include "membermodel.h"
#include "rpcclient.h"

#include <QClipboard>
#include <QDir>
#include <QDateTime>
#include <QDragEnterEvent>
#include <QDropEvent>
#include <QElapsedTimer>
#include <QFileInfo>
#include <QGuiApplication>
#include <QImage>
#include <QJsonArray>
#include <QMimeData>
#include <QPalette>
#include <QProcess>
#include <QQmlApplicationEngine>
#include <QQmlComponent>
#include <QQuickItem>
#include <QQuickTextDocument>
#include <QQuickWindow>
#include <QRegularExpression>
#include <QTemporaryDir>
#include <QTest>
#include <QTextBlock>
#include <QTextDocument>
#include <QTextLayout>
#include <QtQml/QQmlExtensionPlugin>
#include <QtQuickTest/quicktest.h>

#include <algorithm>
#include <functional>
#include <memory>

Q_IMPORT_QML_PLUGIN(WispPlugin)

using namespace Qt::StringLiterals;

namespace {

using harness::findView;
using harness::itemAt;
using harness::named;

const QString kBig = u"61400009999@s.whatsapp.net"_s;

QRectF sceneRect(const QQuickItem *item)
{
    return QRectF(item->mapToScene(QPointF(0, 0)), item->size());
}

bool near(const QRectF &a, const QRectF &b)
{
    return std::abs(a.left() - b.left()) < 0.5 && std::abs(a.top() - b.top()) < 0.5
        && std::abs(a.right() - b.right()) < 0.5 && std::abs(a.bottom() - b.bottom()) < 0.5;
}

QString str(const QRectF &r)
{
    return u"%1,%2 %3x%4"_s.arg(r.x()).arg(r.y()).arg(r.width()).arg(r.height());
}

QQuickItem *bubbleOf(const QQuickItem *message)
{
    return message->property("bubbleItem").value<QQuickItem *>();
}

// Where a menu size long goes along one side of a window room long: at first
// if it fits there, else at second if that fits, else first moved inside.
qreal place(qreal first, qreal second, qreal size, qreal room)
{
    const auto fits = [&](qreal v) { return v >= 0 && v + size <= room; };
    return std::clamp(fits(first) || !fits(second) ? first : second, 0.0, std::max(0.0, room - size));
}

QStringList ids(const QAbstractItemModel *m)
{
    QStringList l;
    for (int i = 0; i < m->rowCount(); ++i)
        l << m->index(i, 0).data(MessageModel::MsgIdRole).toString();
    return l;
}

} // namespace

class UiTest : public QObject
{
    Q_OBJECT

public:
    explicit UiTest(const QString &dir) : m_dir(dir) {}

private slots:
    void initTestCase();
    void init();
    void cleanupTestCase();

    void chatListLoads();
    void chatRowLayout();
    void rapidSearch();
    void listKeys();
    void shell();
    void notificationWindowState();
    void searchOpensChat();
    void sendText();
    void reply();
    void hoverMenu();
    void pasteImage();
    void attachmentCaptions();
    void escapeClosesChat();
    void readOnlyChat();
    void imageViewer();
    void olderPages();
    void scrollMotion();
    void mouseDrag();
    void wheelDistance();
    void menuPlacement();
    void voiceNote();
    void composerReply();
    void emojiSuggest();
    void emojiPanel();
    void mentionPicker();
    void mentionRendering();
    void replyJump();
    void narrowWhileLoading();
    void messageActions();
    void unlinkClosesChat();

private:
    QQuickItem *focused() const { return m_window->activeFocusItem(); }
    void frame() {
        QCoreApplication::processEvents();
        QVERIFY(QQuickTest::qWaitForPolish(m_window));
    }
    void key(int k, Qt::KeyboardModifiers mods = {}) {
        frame();
        QTest::keyClick(m_window, Qt::Key(k), mods);
        frame();
    }
    void type(const QString &text)
    {
        for (const QChar c : text)
            QTest::keyClick(m_window, c.toLatin1());
    }
    // Where a menu belongs, given its size: its top-left in the window.
    using Placement = std::function<QPointF(const QSizeF &)>;
    Placement atPoint(const QPointF &p) const;
    Placement fromButton(const QRectF &button, bool up, bool alignRight, qreal gap) const;
    // Waits for menu to open where it belongs, inside the window: "" when it
    // does, else what is wrong. Its card's rect goes to card.
    QString menuPlaced(QObject *menu, const Placement &want, QRectF *card = nullptr);
    bool closeMenu(QObject *menu);
    // With WISP_SHOTS set, saves the window there in the light and dark themes.
    void shoot(const QString &name);
    // The id of the first message in the open chat past its first pages, and
    // well before the oldest loaded, that matches pred; or "".
    QString deepMessage(const std::function<bool(const QModelIndex &)> &pred) const;
    // Scrolls view until the bubble of message id starts at y in the window,
    // and returns its delegate (null if it never gets there).
    QQuickItem *scrollTo(QQuickItem *view, const QString &id, qreal y);
    // The newest message event in the Big Chat matching pred, or {}.
    QJsonObject event(const QString &name, const std::function<bool(const QJsonObject &)> &pred) const;
    void openBigChat();

    QString m_dir;
    QProcess m_daemon;
    QQmlApplicationEngine *m_engine = nullptr;
    QQuickWindow *m_window = nullptr;
    QQuickItem *m_chatView = nullptr;
    ChatListModel *m_chats = ChatListModel::instance();
    ConversationController *m_conv = ConversationController::instance();
    QList<std::pair<QString, QJsonObject>> m_events;
};

QJsonObject UiTest::event(const QString &name, const std::function<bool(const QJsonObject &)> &pred) const
{
    for (auto it = m_events.crbegin(); it != m_events.crend(); ++it)
        if (it->first == name && it->second["chat"_L1].toString() == kBig && pred(it->second))
            return it->second;
    return {};
}

void UiTest::initTestCase()
{
    const QString socket = harness::socketPath(m_dir);
    m_daemon.setProcessChannelMode(QProcess::ForwardedErrorChannel);
    harness::startMock(m_daemon, m_dir);
    QVERIFY2(m_daemon.waitForStarted(), qPrintable(m_daemon.errorString()));
    QTRY_VERIFY(QFileInfo::exists(socket));

    RpcClient *rpc = RpcClient::instance();
    connect(rpc, &RpcClient::event, this, [this](const QString &name, const QJsonObject &p) {
        if (name.startsWith("message."_L1))
            m_events.append({name, p["message"_L1].toObject()});
    });
    rpc->start({false, socket, m_dir + "/no-wispd"_L1, {}}); // never spawns a daemon

    m_engine = new QQmlApplicationEngine;
    m_engine->addImageProvider(u"wisp"_s, new ImageProvider);
    m_engine->loadFromModule("Wisp", "Main");
    QVERIFY(!m_engine->rootObjects().isEmpty());
    m_window = qobject_cast<QQuickWindow *>(m_engine->rootObjects().constFirst());
    QVERIFY(m_window);
    m_window->requestActivate();
    QVERIFY(QTest::qWaitForWindowActive(m_window));
}

void UiTest::init()
{
    m_events.clear();
    QTest::failOnWarning(QRegularExpression(u"Error:|TypeError|ReferenceError|Cannot read property|is not a type|DelegateModel::cancel"_s));
}

void UiTest::cleanupTestCase()
{
    delete m_engine;
    m_daemon.terminate();
    m_daemon.waitForFinished();
}

void UiTest::chatListLoads()
{
    QTRY_VERIFY_WITH_TIMEOUT(m_chats->count() > 40 && !m_chats->loading(), 10000);
    m_chatView = findView(m_window->contentItem(), m_chats);
    QVERIFY(m_chatView);
    QTRY_COMPARE(m_chatView->property("count").toInt(), m_chats->count());
    QVERIFY(itemAt(m_chatView, 0));
    QVERIFY(m_chatView->hasActiveFocus());
}

void UiTest::rapidSearch()
{
    const int all = m_chats->count();
    const QSize original = m_window->size();
    for (const QSize size : {QSize(1562, 737), m_window->minimumSize(), original}) {
        m_window->resize(size);
        QTest::keyClick(m_window, Qt::Key_K, Qt::ControlModifier);
        type(u"no worries Dinner? tomorrow nope lol brb Can you send it again"_s);
        QCOMPARE(m_chats->count(), 0);
        key(Qt::Key_Escape);
        QTRY_COMPARE(m_chats->count(), all);
    }
    m_chatView->setProperty("currentIndex", -1);
    m_chatView->forceActiveFocus();
    frame();
}

void UiTest::listKeys()
{
    key(Qt::Key_Down);
    key(Qt::Key_Down);
    QCOMPARE(m_chatView->property("currentIndex").toInt(), 1);
    key(Qt::Key_Up);
    key(Qt::Key_Up);
    QVERIFY(focused()->inherits("QQuickTextInput")); // the search field
    key(Qt::Key_Down);
    QCOMPARE(m_chatView->property("currentIndex").toInt(), 0);
    key(Qt::Key_Return);
    QTRY_COMPARE(m_conv->jid(), m_chats->index(0).data(ChatListModel::JidRole).toString());
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit")); // the composer
    key(Qt::Key_Escape);
    QCOMPARE(m_conv->jid(), QString());
    QVERIFY(m_chatView->hasActiveFocus());
    QCOMPARE(m_chatView->property("currentIndex").toInt(), 0);

    // Typing in the list searches.
    type(u"b"_s);
    QVERIFY(focused()->inherits("QQuickTextInput"));
    QCOMPARE(m_chats->filterText(), u"b"_s);
    key(Qt::Key_Escape);
    QCOMPARE(m_chats->filterText(), QString());

    // A click opens a chat, too.
    QQuickItem *row = itemAt(m_chatView, 1);
    QTest::mouseClick(m_window, Qt::LeftButton, {}, row->mapToScene(QPointF(row->width() / 2, row->height() / 2)).toPoint());
    QCOMPARE(m_conv->jid(), m_chats->index(1).data(ChatListModel::JidRole).toString());
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));
    key(Qt::Key_Escape);
    QVERIFY(m_chatView->hasActiveFocus());
}

// The filter chips, a row's menu, and Settings in the list's place.
void UiTest::shell()
{
    const auto click = [this](QQuickItem *item, Qt::MouseButton button = Qt::LeftButton) {
        QVERIFY(item && item->isVisible());
        QTest::mouseClick(m_window, button, {}, item->mapToScene(QPointF(item->width() / 2, item->height() / 2)).toPoint());
    };
    QQuickItem *root = m_window->contentItem();
    const auto shown = [root](const QString &name) {
        QQuickItem *item = named(root, name);
        return item && item->isVisible();
    };

    const int all = m_chats->count();
    int unread = 0, groups = 0;
    for (int i = 0; i < all; ++i) {
        unread += m_chats->index(i).data(ChatListModel::UnreadRole).toInt() > 0;
        groups += m_chats->index(i).data(ChatListModel::IsGroupRole).toBool();
    }
    QVERIFY(unread > 0 && unread < all && groups > 0 && groups < all);
    click(named(root, u"filterUnread"_s));
    QCOMPARE(m_chats->count(), unread);
    QTRY_COMPARE(m_chatView->property("count").toInt(), unread);
    const QString read = m_chats->index(0).data(ChatListModel::JidRole).toString();
    m_chats->markRead(read);
    QCOMPARE(m_chats->indexOf(read), -1);
    QCOMPARE(m_chats->count(), unread - 1);
    click(named(root, u"filterGroups"_s));
    QCOMPARE(m_chats->count(), groups);
    click(named(root, u"filterAll"_s));
    QCOMPARE(m_chats->count(), all);
    frame();
    QTRY_VERIFY(itemAt(m_chatView, 0));

    // Right-click, Mute, and back.
    const QString jid = m_chats->index(0).data(ChatListModel::JidRole).toString();
    const bool muted = m_chats->index(0).data(ChatListModel::MutedRole).toBool();
    for (const bool want : {!muted, muted}) {
        frame();
        QTRY_VERIFY(itemAt(m_chatView, 0));
        click(itemAt(m_chatView, 0), Qt::RightButton);
        QTRY_VERIFY(shown(u"menuMute"_s));
        click(named(root, u"menuMute"_s));
        QCOMPARE(m_chats->index(m_chats->indexOf(jid)).data(ChatListModel::MutedRole).toBool(), want);
        QTRY_VERIFY(!shown(u"menuMute"_s));
    }

    click(named(root, u"navSettings"_s));
    QVERIFY(!m_chatView->isVisible());
    click(named(root, u"settingsNotifications"_s));
    QVERIFY(!shown(u"settingsNotifications"_s));
    click(named(root, u"navChats"_s));
    QVERIFY(m_chatView->isVisible());
    QVERIFY(m_chatView->hasActiveFocus());
}

void UiTest::notificationWindowState()
{
    const QSize originalSize = m_window->size();
    const auto originalVisibility = m_window->visibility();
    auto *rpc = RpcClient::instance();
    const auto activate = [&] {
        QTest::ignoreMessage(QtWarningMsg, "This plugin does not support raise()");
        emit rpc->openChatRequested(QString());
        frame();
    };

    m_window->showMaximized();
    QTRY_COMPARE(m_window->visibility(), QWindow::Maximized);
    frame();
    const QSize maximizedSize = m_window->size();
    activate();
    QCOMPARE(m_window->visibility(), QWindow::Maximized);
    QCOMPARE(m_window->size(), maximizedSize);

    for (bool minimized : {true, false}) {
        if (minimized) m_window->showMinimized();
        else m_window->hide();
        frame();
        activate();
        QTRY_COMPARE(m_window->visibility(), QWindow::Maximized);
        QCOMPARE(m_window->size(), maximizedSize);
    }

    m_window->showNormal();
    m_window->resize(QSize(1000, 640));
    frame();
    activate();
    QCOMPARE(m_window->visibility(), QWindow::Windowed);
    QCOMPARE(m_window->size(), QSize(1000, 640));
    m_window->showMinimized();
    frame();
    activate();
    QTRY_COMPARE(m_window->visibility(), QWindow::Windowed);
    QCOMPARE(m_window->size(), QSize(1000, 640));

    m_window->showFullScreen();
    frame();
    activate();
    QCOMPARE(m_window->visibility(), QWindow::FullScreen);
    m_window->setVisibility(originalVisibility);
    m_window->resize(originalSize);
    frame();
}

void UiTest::chatRowLayout()
{
    QQmlComponent component(m_engine);
    component.setData("import QtQuick\nimport Wisp\nChatRow {}", QUrl());
    const QString person = u"61400000001@s.whatsapp.net"_s;
    const QString preview = u"@You Yes, we already reuse long-running threads. I checked every layout and all the spacing."_s;
    const QVariantMap values {
        {u"index"_s, 0}, {u"jid"_s, u"layout@g.us"_s}, {u"name"_s, u"Design team"_s},
        {u"isGroup"_s, true}, {u"avatarSource"_s, qEnvironmentVariable("WISP_ROW_AVATAR")},
        {u"preview"_s, preview},
        {u"richPreview"_s, u"<a class=\"m\" href=\"mention:"_s + person + u"\">@You</a> "_s + preview.sliced(5)},
        {u"previewSender"_s, u"Sam"_s}, {u"timeText"_s, u"16:54"_s},
        {u"unread"_s, 0}, {u"muted"_s, false}, {u"pinned"_s, false},
        {u"lastStatus"_s, QString()}, {u"typingText"_s, QString()}, {u"current"_s, true}
    };
    std::unique_ptr<QQuickItem> row(qobject_cast<QQuickItem *>(component.createWithInitialProperties(values)));
    QVERIFY2(row, qPrintable(component.errorString()));
    row->setParentItem(m_window->contentItem());
    row->setPosition(QPointF(0, 0));
    row->setZ(1000);
    QQuickItem *body = named(row.get(), u"chatPreview"_s);
    QQuickItem *name = named(row.get(), u"chatName"_s);
    QQuickItem *time = named(row.get(), u"chatTime"_s);
    QQuickItem *meta = named(row.get(), u"chatMeta"_s);
    QVERIFY(body && name && time && meta);
    for (const int width : {340, 420, 510}) {
        row->setWidth(width);
        for (const bool icons : {false, true}) {
            row->setProperty("muted", icons);
            row->setProperty("pinned", icons);
            row->setProperty("unread", icons ? 12 : 0);
            frame();
            QCOMPARE(name->x(), 83.0);
            QCOMPARE(body->x(), name->x());
            QCOMPARE(body->y() - (name->y() + name->height()), 2.0);
            QCOMPARE(width - time->x() - time->width(), 25.0);
            QCOMPARE(time->y() - name->y(), 5.0);
            QVERIFY(body->x() + body->width() <= meta->x() + 0.5);
            const QString html = body->property("text").toString();
            QTextDocument document;
            document.setHtml(html);
            QVERIFY2(document.toPlainText().endsWith(QChar(0x2026)), qPrintable(html));
            QVERIFY(body->property("contentWidth").toDouble() <= body->width() + 1);
            QVERIFY(body->property("contentHeight").toDouble() <= body->height() + 1);
            bool linked = false;
            for (qreal x = 0; x < body->width(); x += 2) {
                QString link;
                QMetaObject::invokeMethod(body, "linkAt", Q_RETURN_ARG(QString, link), Q_ARG(qreal, x), Q_ARG(qreal, body->height() / 2));
                linked |= link == u"mention:"_s + person;
            }
            QVERIFY(linked);
            if (!icons && width == 510) {
                if (const QString dir = qEnvironmentVariable("WISP_SHOTS"); !dir.isEmpty())
                    QVERIFY(m_window->grabWindow().copy(QRect(0, 0, width, 72)).save(dir + "/chat-row.png"_L1));
            }
        }
    }
}

void UiTest::openBigChat()
{
    key(Qt::Key_K, Qt::ControlModifier);
    QVERIFY(focused()->inherits("QQuickTextInput"));
    type(u"big ch"_s);
    QCOMPARE(m_chats->count(), 1);
    key(Qt::Key_Return);
    QCOMPARE(m_conv->jid(), kBig);
    QCOMPARE(m_chats->filterText(), QString());
    QTRY_VERIFY(m_conv->messages()->rowCount() > 0);
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));
}

void UiTest::searchOpensChat()
{
    openBigChat();
    // From the composer: Ctrl+K, then Esc returns to it.
    key(Qt::Key_K, Qt::ControlModifier);
    QVERIFY(focused()->inherits("QQuickTextInput"));
    key(Qt::Key_Escape);
    QVERIFY(focused()->inherits("QQuickTextEdit"));
    // Choosing the open chat again also returns to it.
    key(Qt::Key_K, Qt::ControlModifier);
    type(u"big"_s);
    key(Qt::Key_Return);
    QVERIFY(focused()->inherits("QQuickTextEdit"));
    QCOMPARE(focused()->property("text").toString(), QString());
}

void UiTest::sendText()
{
    type(u"hello from"_s);
    key(Qt::Key_Return, Qt::ShiftModifier); // a new line
    type(u"the ui test"_s);
    key(Qt::Key_Return);
    QCOMPARE(focused()->property("text").toString(), QString());
    const QString text = u"hello from\nthe ui test"_s;
    QJsonObject sent;
    QTRY_VERIFY((sent = event(u"message.new"_s, [&](const QJsonObject &m) { return m["text"_L1] == text; })).size());
    QVERIFY(sent["fromMe"_L1].toBool());
    const QString id = sent["id"_L1].toString();
    QTRY_VERIFY_WITH_TIMEOUT(!event(u"message.update"_s, [&](const QJsonObject &m) {
        return m["id"_L1] == id && m["status"_L1] == "read"_L1;
    }).isEmpty(), 10000);
    const MessageModel *model = m_conv->messages();
    QTRY_COMPARE(model->index(model->indexOf(id), 0).data(MessageModel::StatusRole).toString(), u"read"_s);
    // The mock answers after a few seconds; let that settle before the next step.
    QJsonObject answer;
    QTRY_VERIFY_WITH_TIMEOUT((answer = event(u"message.new"_s, [&](const QJsonObject &m) {
        return !m["fromMe"_L1].toBool() && m["timestamp"_L1].toInteger() >= sent["timestamp"_L1].toInteger();
    })).size(), 10000);
    QTRY_COMPARE(model->indexOf(answer["id"_L1].toString()), 0);
    QTest::qWait(100);
}

void UiTest::reply()
{
    QQuickItem *view = findView(m_window->contentItem(), m_conv->messages());
    QVERIFY(view);
    QQuickItem *target = nullptr;
    for (int row = 0; row < 20 && !target; ++row) {
        QQuickItem *item = itemAt(view, row);
        if (item && !item->property("isSystem").toBool() && !item->property("isDeleted").toBool()
            && view->contains(item->mapToItem(view, QPointF(item->width() / 2, item->height() / 2))))
            target = item;
    }
    QVERIFY(target);
    const QString id = target->property("msgId").toString();
    const QPointF at = target->mapToScene(QPointF(target->width() / 2, target->height() / 2));
    // Esc dismisses the menu and leaves the composer focused.
    QTest::mouseClick(m_window, Qt::RightButton, {}, at.toPoint());
    QVERIFY(!focused()->inherits("QQuickTextEdit"));
    key(Qt::Key_Escape);
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));
    QCOMPARE(m_conv->jid(), kBig);

    QTest::mouseClick(m_window, Qt::RightButton, {}, at.toPoint());
    key(Qt::Key_Down); // "Reply"
    key(Qt::Key_Return);
    QTRY_COMPARE(m_conv->replyTo().toMap().value(u"id"_s).toString(), id);
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));

    const QString text = u"a reply from the ui test"_s;
    type(text);
    key(Qt::Key_Return);
    QJsonObject sent;
    QTRY_VERIFY((sent = event(u"message.new"_s, [&](const QJsonObject &m) { return m["text"_L1] == text; })).size());
    QCOMPARE(sent["replyTo"_L1]["id"_L1].toString(), id);
    QVERIFY(m_conv->replyTo().toMap().isEmpty());
}

void UiTest::pasteImage()
{
    QImage image(64, 48, QImage::Format_RGB32);
    image.fill(Qt::darkCyan);
    const QString draft = u"Photo caption"_s;
    type(draft);
    QGuiApplication::clipboard()->setImage(image);
    key(Qt::Key_V, Qt::ControlModifier);
    QTRY_COMPARE(m_conv->attachments()->count(), 1);
    QQuickItem *root = m_window->contentItem();
    QQuickItem *preview = named(root, u"mediaPreview"_s);
    QQuickItem *caption = named(root, u"attachmentCaption"_s);
    QVERIFY(preview && caption);
    QTRY_VERIFY(caption->hasActiveFocus());
    QCOMPARE(caption->property("text").toString(), draft);
    const QString temporary = m_conv->attachments()->index(0).data(AttachmentModel::SourceRole).toUrl().toLocalFile();
    QVERIFY(QFileInfo::exists(temporary));
    QVERIFY(event(u"message.new"_s, [](const QJsonObject &m) { return m["type"_L1] == "image"_L1; }).isEmpty());
    type(u" edited"_s);
    key(Qt::Key_Return, Qt::ShiftModifier);
    type(u"second line"_s);
    const QString text = draft + u" edited\nsecond line"_s;
    QCOMPARE(m_conv->attachments()->index(0).data(AttachmentModel::CaptionRole).toString(), text);
    shoot(u"photo-caption"_s);
    key(Qt::Key_Return);
    QTRY_VERIFY(!event(u"message.new"_s, [&](const QJsonObject &m) {
        return m["type"_L1] == "image"_L1 && m["fromMe"_L1].toBool() && m["text"_L1] == text;
    }).isEmpty());
    QCOMPARE(m_conv->attachments()->count(), 0);
    QTRY_VERIFY(!QFileInfo::exists(temporary));
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));
    QCOMPARE(focused()->property("text").toString(), QString());

    // Cancel preserves the chat draft and reply, and removes the pasted file.
    m_events.clear();
    type(draft);
    const QString reply = m_conv->messages()->index(0).data(MessageModel::MsgIdRole).toString();
    m_conv->startReply(reply);
    key(Qt::Key_V, Qt::ControlModifier);
    QTRY_COMPARE(m_conv->attachments()->count(), 1);
    const QString cancelled = m_conv->attachments()->index(0).data(AttachmentModel::SourceRole).toUrl().toLocalFile();
    key(Qt::Key_Escape);
    QCOMPARE(m_conv->attachments()->count(), 0);
    QVERIFY(!QFileInfo::exists(cancelled));
    QCOMPARE(m_conv->jid(), kBig);
    QCOMPARE(m_conv->replyTo().toMap().value(u"id"_s).toString(), reply);
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));
    QCOMPARE(focused()->property("text").toString(), draft);
    QVERIFY(event(u"message.new"_s, [](const QJsonObject &m) { return m["type"_L1] == "image"_L1; }).isEmpty());
    m_conv->cancelReply();
    key(Qt::Key_A, Qt::ControlModifier);
    key(Qt::Key_Backspace);

    // Text pastes as text, also with an image beside it (office apps offer both).
    QGuiApplication::clipboard()->setText(u"pasted"_s);
    key(Qt::Key_V, Qt::ControlModifier);
    QCOMPARE(focused()->property("text").toString(), u"pasted"_s);
    key(Qt::Key_A, Qt::ControlModifier);
    key(Qt::Key_Backspace);
    auto *mixed = new QMimeData;
    mixed->setText(u"cells"_s);
    mixed->setImageData(image);
    QGuiApplication::clipboard()->setMimeData(mixed);
    key(Qt::Key_V, Qt::ControlModifier);
    QCOMPARE(focused()->property("text").toString(), u"cells"_s);
    key(Qt::Key_A, Qt::ControlModifier);
    key(Qt::Key_Backspace);
}

void UiTest::attachmentCaptions()
{
    QImage image(640, 480, QImage::Format_RGB32);
    image.fill(QColor(u"#147d64"_s));
    const QString first = m_dir + "/first.png"_L1, second = m_dir + "/second.png"_L1;
    QVERIFY(image.save(first));
    image.fill(QColor(u"#435c82"_s));
    QVERIFY(image.save(second));
    m_conv->stageFiles({first, second}, u"First photo"_s);
    QTRY_COMPARE(m_conv->attachments()->count(), 2);
    QQuickItem *root = m_window->contentItem();
    QQuickItem *caption = named(root, u"attachmentCaption"_s);
    QQuickItem *thumbs = named(root, u"attachmentThumbnails"_s);
    QVERIFY(caption && thumbs);
    frame();
    QQuickItem *secondThumb = itemAt(thumbs, 1);
    QVERIFY(secondThumb);
    QTest::mouseClick(m_window, Qt::LeftButton, {}, sceneRect(secondThumb).center().toPoint());
    QTRY_COMPARE(caption->property("text").toString(), QString());
    type(u"Second photo"_s);
    QTest::mouseClick(m_window, Qt::LeftButton, {}, sceneRect(itemAt(thumbs, 0)).center().toPoint());
    QTRY_COMPARE(caption->property("text").toString(), u"First photo"_s);
    const QSize size = m_window->size();
    m_window->resize(m_window->minimumSize());
    frame();
    for (const QString &part : {u"captionField"_s, u"attachmentThumbnails"_s, u"sendAttachments"_s})
        QVERIFY2(QRectF(QPointF(), m_window->size()).contains(sceneRect(named(root, part))), qPrintable(part));
    shoot(u"photos-small"_s);
    m_window->resize(size);
    frame();
    QQuickItem *send = named(root, u"sendAttachments"_s);
    QTest::mouseClick(m_window, Qt::LeftButton, {}, sceneRect(send).center().toPoint());
    for (const QString &text : {u"First photo"_s, u"Second photo"_s})
        QTRY_VERIFY(!event(u"message.new"_s, [&](const QJsonObject &m) {
            return m["type"_L1] == "image"_L1 && m["text"_L1] == text;
        }).isEmpty());
    QVERIFY(QFileInfo::exists(first) && QFileInfo::exists(second));
    QMimeData files;
    files.setUrls({QUrl::fromLocalFile(first)});
    const QPointF at = sceneRect(findView(root, m_conv->messages())).center();
    QDragEnterEvent enter(at.toPoint(), Qt::CopyAction, &files, Qt::LeftButton, {});
    QGuiApplication::sendEvent(m_window, &enter);
    QVERIFY(enter.isAccepted());
    QDropEvent drop(at, Qt::CopyAction, &files, Qt::LeftButton, {});
    QGuiApplication::sendEvent(m_window, &drop);
    QVERIFY(drop.isAccepted());
    QTRY_COMPARE(m_conv->attachments()->count(), 1);
    m_conv->stageFiles({second});
    QCOMPARE(m_conv->attachments()->count(), 2);
    m_conv->attachments()->remove(0);
    QCOMPARE(m_conv->attachments()->count(), 1);
    QCOMPARE(m_conv->attachments()->index(0).data(AttachmentModel::FileNameRole).toString(), u"second.png"_s);
    m_conv->close();
    QCOMPARE(m_conv->attachments()->count(), 0);
    QVERIFY(QFileInfo::exists(first) && QFileInfo::exists(second));
    openBigChat();
}

void UiTest::escapeClosesChat()
{
    m_conv->startReply(m_conv->messages()->index(0, 0).data(MessageModel::MsgIdRole).toString());
    QVERIFY(!m_conv->replyTo().toMap().isEmpty());
    key(Qt::Key_Escape); // cancels the reply first
    QVERIFY(m_conv->replyTo().toMap().isEmpty());
    QCOMPARE(m_conv->jid(), kBig);
    type(u"draft"_s);
    key(Qt::Key_Escape);
    QCOMPARE(m_conv->jid(), QString());
    QVERIFY(m_chatView->hasActiveFocus());
    QCOMPARE(m_chatView->property("currentItem").value<QObject *>()->property("jid").toString(), kBig);
}

void UiTest::readOnlyChat()
{
    key(Qt::Key_K, Qt::ControlModifier);
    type(u"wisp ann"_s);
    key(Qt::Key_Return);
    QVERIFY(m_conv->readOnly());
    key(Qt::Key_Escape);
    QCOMPARE(m_conv->jid(), QString());
    QVERIFY(m_chatView->hasActiveFocus());
}

void UiTest::imageViewer()
{
    key(Qt::Key_K, Qt::ControlModifier);
    type(u"housemates"_s);
    key(Qt::Key_Return);
    QTRY_VERIFY(m_conv->messages()->rowCount() > 0);
    QQuickItem *view = findView(m_window->contentItem(), m_conv->messages());
    QQuickItem *image = nullptr; // the bubble of an image message
    QTRY_VERIFY([&] {
        for (int row = 0; row < m_conv->messages()->rowCount() && !image; ++row) {
            QQuickItem *item = itemAt(view, row);
            QQuickItem *bubble = item && item->property("isVisual").toBool()
                ? item->property("bubbleItem").value<QQuickItem *>() : nullptr;
            if (bubble && view->contains(bubble->mapToItem(view, QPointF(bubble->width() / 2, bubble->height() / 2))))
                image = bubble;
        }
        if (!image)
            view->setProperty("contentY", view->property("contentY").toReal() - 200);
        return image != nullptr;
    }());
    // Its right-click menu copies the image without opening it.
    QObject *messageMenu = m_window->findChild<QObject *>(u"messageMenu"_s);
    QVERIFY(messageMenu);
    QGuiApplication::clipboard()->clear();
    const QPointF imageCentre = image->mapToScene(QPointF(image->width() / 2, image->height() / 2));
    QTest::mouseClick(m_window, Qt::RightButton, {}, imageCentre.toPoint());
    QCOMPARE(menuPlaced(messageMenu, atPoint(imageCentre.toPoint())), QString());
    QQuickItem *copyEntry = nullptr;
    QStringList shown;
    for (int i = 0; i < messageMenu->property("count").toInt(); ++i) {
        QQuickItem *entry = nullptr;
        QMetaObject::invokeMethod(messageMenu, "itemAt", Q_RETURN_ARG(QQuickItem *, entry), Q_ARG(int, i));
        if (!entry || !entry->isVisible())
            continue;
        shown << entry->property("text").toString();
        if (shown.last() == "Copy image"_L1)
            copyEntry = entry;
    }
    QVERIFY2(shown.contains(u"Reply"_s) && shown.contains(u"Open"_s) && copyEntry, qPrintable(shown.join(u", "_s)));
    QVERIFY(QMetaObject::invokeMethod(copyEntry, "click"));
    QTRY_VERIFY(!messageMenu->property("visible").toBool());
    QTRY_VERIFY(!QGuiApplication::clipboard()->image().isNull());

    QTest::mouseClick(m_window, Qt::LeftButton, {}, image->mapToScene(QPointF(image->width() / 2, image->height() / 2)).toPoint());
    QTRY_VERIFY(!focused()->inherits("QQuickTextEdit")); // the viewer

    // A right click on the image offers to copy it, at full size.
    QObject *menu = m_window->findChild<QObject *>(u"imageMenu"_s);
    QVERIFY(menu);
    QGuiApplication::clipboard()->clear();
    const QPoint centre(m_window->width() / 2, m_window->height() / 2);
    QTest::mouseClick(m_window, Qt::RightButton, {}, centre);
    QVERIFY2(menuPlaced(menu, atPoint(centre)).isEmpty(), "The image menu did not open at the click");
    QQuickItem *copy = nullptr;
    QMetaObject::invokeMethod(menu, "itemAt", Q_RETURN_ARG(QQuickItem *, copy), Q_ARG(int, 0));
    QVERIFY(copy);
    QCOMPARE(copy->property("text").toString(), u"Copy image"_s);
    QTest::mouseClick(m_window, Qt::LeftButton, {}, copy->mapToScene(QPointF(copy->width() / 2, copy->height() / 2)).toPoint());
    QTRY_VERIFY(!menu->property("visible").toBool());
    QTRY_VERIFY(!QGuiApplication::clipboard()->image().isNull());
    QVERIFY(QGuiApplication::clipboard()->image().width() > image->width());
    QVERIFY(!focused()->inherits("QQuickTextEdit")); // still the viewer
    key(Qt::Key_Escape);
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));
    key(Qt::Key_Escape);
    QCOMPARE(m_conv->jid(), QString());
}

// A bubble's hover chevron opens its menu.
void UiTest::hoverMenu()
{
    QQuickItem *view = findView(m_window->contentItem(), m_conv->messages());
    QVERIFY(view);
    QQuickItem *bubble = nullptr;
    for (int row = 0; row < 20 && !bubble; ++row) {
        QQuickItem *item = itemAt(view, row);
        QQuickItem *b = item && !item->property("isSystem").toBool() && !item->property("bare").toBool()
            ? item->property("bubbleItem").value<QQuickItem *>() : nullptr;
        if (b && view->contains(b->mapToItem(view, QPointF(b->width() / 2, b->height() / 2))))
            bubble = b;
    }
    QVERIFY(bubble);
    QTest::mouseMove(m_window, bubble->mapToScene(QPointF(4, bubble->height() / 2)).toPoint());
    QTest::mouseMove(m_window, bubble->mapToScene(QPointF(bubble->width() / 2, bubble->height() / 2)).toPoint());
    QQuickItem *chevron = m_window->findChild<QQuickItem *>(u"chevron"_s);
    QVERIFY(chevron);
    QTRY_VERIFY(chevron->isVisible());
    QObject *menu = m_window->findChild<QObject *>(u"messageMenu"_s);
    QVERIFY(menu);
    QTest::mouseClick(m_window, Qt::LeftButton, {}, chevron->mapToScene(QPointF(chevron->width() / 2, chevron->height() / 2)).toPoint());
    QTRY_VERIFY(menu->property("visible").toBool());
    QMetaObject::invokeMethod(menu, "close");
    QTRY_VERIFY(!menu->property("visible").toBool());
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));
}

// React, Delete for everyone and Delete for me from a message's menu. Last
// but one: it changes the Big Chat the other tests count on.
void UiTest::messageActions()
{
    key(Qt::Key_Escape); // back to the newest messages
    openBigChat();
    QTRY_VERIFY(m_conv->jid() == kBig && !m_conv->loading() && m_conv->messages()->rowCount() > 0);
    QQuickItem *view = findView(m_window->contentItem(), m_conv->messages());
    QObject *menu = m_window->findChild<QObject *>(u"messageMenu"_s);
    QObject *picker = m_window->findChild<QObject *>(u"reactionPicker"_s);
    QObject *confirm = m_window->findChild<QObject *>(u"deleteConfirm"_s);
    QVERIFY(view && menu && picker && confirm);
    const MessageModel *model = m_conv->messages();
    // A visible message, mine or theirs, that is on WhatsApp.
    const auto visible = [&](bool mine) -> QQuickItem * {
        for (int row = 0; row < 20; ++row) {
            QQuickItem *item = itemAt(view, row);
            if (item && !item->property("isSystem").toBool() && !item->property("isDeleted").toBool()
                && item->property("fromMe").toBool() == mine
                && (!mine || item->property("status").toString() != "pending"_L1)
                && view->contains(item->mapToItem(view, QPointF(item->width() / 2, item->height() / 2))))
                return item;
        }
        return nullptr;
    };
    // Right-clicks message, returning its menu's visible entries by text.
    const auto open = [&](QQuickItem *message) {
        QMap<QString, QQuickItem *> shown;
        frame();
        const QPointF at = sceneRect(bubbleOf(message)).center();
        QTest::mouseClick(m_window, Qt::RightButton, {}, at.toPoint());
        if (!menuPlaced(menu, atPoint(at)).isEmpty())
            return shown;
        for (int i = 0; i < menu->property("count").toInt(); ++i) {
            QQuickItem *entry = nullptr;
            QMetaObject::invokeMethod(menu, "itemAt", Q_RETURN_ARG(QQuickItem *, entry), Q_ARG(int, i));
            if (entry && entry->isVisible())
                shown.insert(entry->property("text").toString(), entry);
        }
        return shown;
    };
    const auto reactions = [&](const QString &id) {
        return model->index(model->indexOf(id), 0).data(MessageModel::ReactionsRole).toList();
    };
    const auto mineReacted = [&](const QString &id, const QString &emoji) {
        for (const QVariant &r : reactions(id))
            if (r.toMap().value(u"mine"_s).toBool())
                return r.toMap().value(u"emoji"_s).toString() == emoji;
        return emoji.isEmpty();
    };
    const auto pick = [&](const QString &emoji) {
        QQuickItem *content = picker->property("contentItem").value<QQuickItem *>();
        QTRY_VERIFY(picker->property("opened").toBool());
        for (QQuickItem *cell : content->childItems().value(0)->childItems()) // the Row's
            if (cell->property("modelData").toString() == emoji) {
                QTest::mouseClick(m_window, Qt::LeftButton, {}, sceneRect(cell).center().toPoint());
                return;
            }
        QFAIL("no such reaction");
    };

    QQuickItem *mine = visible(true);
    QVERIFY(mine);
    const QString mineId = mine->property("msgId").toString();
    QMap<QString, QQuickItem *> entries = open(mine);
    shoot(u"message-menu"_s);
    QStringList want = {u"Reply"_s, u"React"_s, u"Delete for me"_s, u"Delete for everyone"_s};
    if (!mine->property("richText").toString().isEmpty())
        want << u"Copy"_s;
    for (const QString &w : want)
        QVERIFY2(entries.contains(w), qPrintable(QStringList(entries.keys()).join(u", "_s)));

    // React, then pick the same emoji again to take it back.
    QVERIFY(QMetaObject::invokeMethod(entries[u"React"_s], "click"));
    pick(u"👍"_s);
    QTRY_VERIFY(mineReacted(mineId, u"👍"_s));
    QTRY_VERIFY(!picker->property("visible").toBool());
    QVERIFY(QMetaObject::invokeMethod(open(mine).value(u"React"_s), "click"));
    QTRY_COMPARE(picker->property("mine").toString(), u"👍"_s);
    pick(u"👍"_s);
    QTRY_VERIFY(mineReacted(mineId, {}));
    QTRY_VERIFY(!picker->property("visible").toBool());

    // The picker has a card behind it, and + offers every emoji.
    QVERIFY(QMetaObject::invokeMethod(open(mine).value(u"React"_s), "click"));
    QTRY_VERIFY(picker->property("opened").toBool());
    QVERIFY(picker->property("width").toReal() > 7 * 40);
    shoot(u"reactions"_s);
    QQuickItem *more = m_window->findChild<QQuickItem *>(u"moreReactions"_s);
    QVERIFY(more);
    QTest::mouseClick(m_window, Qt::LeftButton, {}, sceneRect(more).center().toPoint());
    QObject *panel = m_window->findChild<QObject *>(u"reactPanel"_s);
    QVERIFY(panel);
    QTRY_VERIFY(panel->property("opened").toBool());
    shoot(u"reaction-panel"_s);
    QVERIFY(!picker->property("visible").toBool());
    QVERIFY(QMetaObject::invokeMethod(panel, "pick", Q_ARG(QVariant, u"🎉"_s)));
    QTRY_VERIFY(mineReacted(mineId, u"🎉"_s));
    QTRY_VERIFY(!panel->property("visible").toBool());

    // Delete for everyone, after confirming.
    QVERIFY(QMetaObject::invokeMethod(open(mine).value(u"Delete for everyone"_s), "click"));
    QTRY_VERIFY(confirm->property("opened").toBool());
    QVERIFY(QMetaObject::invokeMethod(confirm, "accept"));
    QTRY_VERIFY(model->index(model->indexOf(mineId), 0).data(MessageModel::DeletedRole).toBool());

    // Theirs can't be deleted for everyone; deleting it for me drops it.
    QQuickItem *theirs = visible(false);
    QVERIFY(theirs);
    const QString theirId = theirs->property("msgId").toString();
    entries = open(theirs);
    QVERIFY(entries.contains(u"Delete for me"_s) && !entries.contains(u"Delete for everyone"_s));
    QVERIFY(QMetaObject::invokeMethod(entries[u"Delete for me"_s], "click"));
    QTRY_VERIFY(confirm->property("opened").toBool());
    const int rows = model->rowCount();
    QVERIFY(QMetaObject::invokeMethod(confirm, "accept"));
    QTRY_COMPARE(model->indexOf(theirId), -1);
    QCOMPARE(model->rowCount(), rows - 1);
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));
}

QString UiTest::deepMessage(const std::function<bool(const QModelIndex &)> &pred) const
{
    const MessageModel *m = m_conv->messages();
    for (int row = 60; row < m->rowCount() - 60; ++row)
        if (const QModelIndex i = m->index(row, 0); pred(i))
            return i.data(MessageModel::MsgIdRole).toString();
    return {};
}

QQuickItem *UiTest::scrollTo(QQuickItem *view, const QString &id, qreal y)
{
    QQuickItem *row = nullptr;
    const bool there = QTest::qWaitFor([&] {
        const int i = m_conv->messages()->indexOf(id);
        row = i < 0 ? nullptr : itemAt(view, i);
        if (!row) {
            if (i >= 0)
                QMetaObject::invokeMethod(view, "positionViewAtIndex", Q_ARG(int, i), Q_ARG(int, 1)); // ListView.Center
            return false;
        }
        const qreal off = sceneRect(bubbleOf(row)).top() - y;
        if (std::abs(off) < 0.5)
            return true;
        view->setProperty("contentY", view->property("contentY").toReal() + off);
        return false;
    });
    return there ? row : nullptr;
}

UiTest::Placement UiTest::atPoint(const QPointF &p) const
{
    return [this, p](const QSizeF &s) {
        return QPointF(place(p.x(), p.x() - s.width(), s.width(), m_window->width()),
                       place(p.y(), p.y() - s.height(), s.height(), m_window->height()));
    };
}

UiTest::Placement UiTest::fromButton(const QRectF &b, bool up, bool alignRight, qreal gap) const
{
    return [this, b, up, alignRight, gap](const QSizeF &s) {
        const qreal start = b.left(), end = b.right() - s.width();
        const qreal below = b.bottom() + gap, above = b.top() - gap - s.height();
        return QPointF(place(alignRight ? end : start, alignRight ? start : end, s.width(), m_window->width()),
                       place(up ? above : below, up ? below : above, s.height(), m_window->height()));
    };
}

QString UiTest::menuPlaced(QObject *menu, const Placement &want, QRectF *card)
{
    QString why;
    const bool placed = QTest::qWaitFor([&] {
        const auto *background = menu->property("background").value<QQuickItem *>();
        if (!menu->property("opened").toBool() || !background) {
            why = u"not open"_s;
            return false;
        }
        const QRectF r = sceneRect(background);
        const QPointF at = want(r.size());
        if (card)
            *card = r;
        if (std::abs(r.x() - at.x()) >= 1 || std::abs(r.y() - at.y()) >= 1)
            why = u"at %1,%2 instead of %3,%4"_s.arg(r.x()).arg(r.y()).arg(at.x()).arg(at.y());
        else if (!QRectF(QPointF(), m_window->size()).adjusted(-0.5, -0.5, 0.5, 0.5).contains(r))
            why = u"outside the window"_s;
        else
            why.clear();
        return why.isEmpty();
    });
    return placed ? QString() : why;
}

bool UiTest::closeMenu(QObject *menu)
{
    key(Qt::Key_Escape);
    return QTest::qWaitFor([menu] { return !menu->property("visible").toBool(); });
}

void UiTest::shoot(const QString &name)
{
    const QString dir = qEnvironmentVariable("WISP_SHOTS");
    if (dir.isEmpty())
        return;
    const QPalette light = QGuiApplication::palette();
    QPalette dark = light;
    dark.setColor(QPalette::Window, QColor(0x16, 0x17, 0x17));
    for (const auto &[palette, theme] : {std::pair{dark, "dark"_L1}, std::pair{light, "light"_L1}}) {
        QGuiApplication::setPalette(palette);
        frame();
        QVERIFY(QTest::qWaitFor([&] {
            for (auto *item : m_window->findChildren<QQuickItem *>())
                if (item->inherits("QQuickImage") && item->property("status").toInt() == 2)
                    return false;
            return true;
        }));
        QVERIFY(m_window->grabWindow().save(dir + '/' + name + '-' + theme + ".png"_L1));
    }
}

// Every menu opens where it is asked to, also deep in a long scrolled chat,
// and flips to the other side of the click or button where the window ends.
void UiTest::menuPlacement()
{
    QCOMPARE(m_conv->jid(), kBig);
    QQuickItem *root = m_window->contentItem();
    QQuickItem *view = findView(root, m_conv->messages());
    QObject *menu = m_window->findChild<QObject *>(u"messageMenu"_s);
    QQuickItem *chevron = m_window->findChild<QQuickItem *>(u"chevron"_s);
    QVERIFY(view && menu && chevron);
    const auto rightClick = [this](QPointF &p) {
        frame();
        p = p.toPoint();
        QTest::mouseClick(m_window, Qt::RightButton, {}, p.toPoint());
    };
    const auto click = [this](const QQuickItem *item) {
        QTest::mouseClick(m_window, Qt::LeftButton, {}, sceneRect(item).center().toPoint());
    };
    const auto target = [menu] { return menu->property("target").value<QQuickItem *>(); };
    QRectF card;

    // Text messages past the first pages: one short line of theirs and of
    // mine, and one of several lines.
    const auto text = [](const QModelIndex &i) {
        return i.data(MessageModel::TypeRole).toString() == "text"_L1 && !i.data(MessageModel::DeletedRole).toBool()
            && !i.data(MessageModel::EmojiOnlyRole).toBool();
    };
    const auto lines = [](const QModelIndex &i) { return i.data(MessageModel::RichTextRole).toString().count("<br>"_L1) + 1; };
    const auto oneLine = [&](bool mine) {
        return deepMessage([&](const QModelIndex &i) {
            return text(i) && lines(i) == 1 && i.data(MessageModel::RichTextRole).toString().size() < 40
                && i.data(MessageModel::FromMeRole).toBool() == mine;
        });
    };
    QString theirs, mine, tall;
    while (true) {
        theirs = oneLine(false);
        mine = oneLine(true);
        tall = deepMessage([&](const QModelIndex &i) { return text(i) && lines(i) >= 4; });
        if ((!theirs.isEmpty() && !mine.isEmpty() && !tall.isEmpty())
                || !m_conv->hasMore() || m_conv->messages()->rowCount() >= 1000)
            break;
        const int before = m_conv->messages()->rowCount();
        m_conv->loadOlder();
        QTRY_VERIFY(!m_conv->loading() && m_conv->messages()->rowCount() > before);
    }
    QVERIFY(!theirs.isEmpty() && !mine.isEmpty() && !tall.isEmpty());
    const QRectF list = sceneRect(view);

    // Right-clicks: at the click, also under the header; leftwards at the right
    // edge, upwards above the composer.
    QQuickItem *row = scrollTo(view, theirs, list.center().y());
    QVERIFY(row);
    QPointF at = sceneRect(bubbleOf(row)).center();
    rightClick(at);
    QCOMPARE(menuPlaced(menu, atPoint(at)), QString());
    QCOMPARE(target(), row);
    shoot(u"menu-deep"_s);
    QVERIFY(closeMenu(menu));
    at.setX(list.right() - 16);
    rightClick(at);
    QCOMPARE(menuPlaced(menu, atPoint(at), &card), QString());
    QCOMPARE(card.right(), at.x());
    QVERIFY(closeMenu(menu));
    row = scrollTo(view, theirs, list.top() - 10);
    QVERIFY(row);
    at = QPointF(sceneRect(bubbleOf(row)).center().x(), list.top() + 2);
    rightClick(at);
    QCOMPARE(menuPlaced(menu, atPoint(at)), QString());
    QCOMPARE(target(), row);
    QVERIFY(closeMenu(menu));
    row = scrollTo(view, theirs, list.bottom() - 10);
    QVERIFY(row);
    at = list.bottomRight() - QPointF(16, 2);
    rightClick(at);
    QCOMPARE(menuPlaced(menu, atPoint(at), &card), QString());
    QCOMPARE(card.bottomRight(), at);
    QCOMPARE(target(), row);
    shoot(u"menu-corner"_s);
    QVERIFY(closeMenu(menu));

    // A bubble's chevron sits at the top right of the part of it that shows
    // and drops the menu from its left edge, or its right edge for a message
    // of mine; near the composer the menu rises.
    const auto hoverBubble = [&](QQuickItem *row) {
        const QRectF part = sceneRect(bubbleOf(row)).intersected(list);
        QTest::mouseMove(m_window, QPointF(part.left() + 4, part.center().y()).toPoint());
        QTest::mouseMove(m_window, part.center().toPoint());
        return QTest::qWaitFor([&] { return chevron->property("row").value<QQuickItem *>() == row; });
    };
    const auto viaChevron = [&](QQuickItem *row) {
        if (!hoverBubble(row))
            return u"the bubble is not hovered"_s;
        const QRectF part = sceneRect(bubbleOf(row)).intersected(list);
        const QRectF want(part.right() - 3 - chevron->width(), part.top() + 3, chevron->width(), chevron->height());
        if (!QTest::qWaitFor([&] { return chevron->isVisible() && near(sceneRect(chevron), want); }))
            return u"the chevron is at %1 instead of %2"_s.arg(str(sceneRect(chevron)), str(want));
        click(chevron);
        return menuPlaced(menu, fromButton(sceneRect(chevron), false, row->property("fromMe").toBool(), 4), &card);
    };
    for (const QString &id : {theirs, mine}) {
        row = scrollTo(view, id, list.center().y());
        QVERIFY(row);
        QCOMPARE(viaChevron(row), QString());
        QCOMPARE(target(), row);
        QVERIFY(closeMenu(menu));
    }
    row = scrollTo(view, theirs, list.bottom() - 30);
    QVERIFY(row);
    QCOMPARE(viaChevron(row), QString());
    QCOMPARE(card.bottom(), sceneRect(chevron).top() - 4);
    shoot(u"menu-chevron"_s);
    QVERIFY(closeMenu(menu));
    // Under the header it stays in the list, and it goes when too little of
    // the bubble shows to hold it.
    row = scrollTo(view, tall, list.top() - 40);
    QVERIFY(row);
    QCOMPARE(viaChevron(row), QString());
    QVERIFY(closeMenu(menu));
    row = scrollTo(view, tall, list.top() + 12 - bubbleOf(row)->height());
    QVERIFY(row);
    QVERIFY(hoverBubble(row));
    QVERIFY(!chevron->isVisible());

    // A chat row: at a right-click by the window's left edge, upwards at the
    // bottom; the hover chevron's menu is below it, from its right edge.
    QObject *rowMenu = m_window->findChild<QObject *>(u"rowMenu"_s);
    QVERIFY(rowMenu);
    QQuickItem *first = itemAt(m_chatView, 0);
    QVERIFY(first);
    at = sceneRect(first).topLeft() + QPointF(3, first->height() / 2);
    rightClick(at);
    QCOMPARE(menuPlaced(rowMenu, atPoint(at)), QString());
    QVERIFY(closeMenu(rowMenu));
    at = QPointF(sceneRect(m_chatView).center().x(), m_window->height() - 2);
    rightClick(at);
    QCOMPARE(menuPlaced(rowMenu, atPoint(at), &card), QString());
    QCOMPARE(card.bottom(), at.y());
    QVERIFY(closeMenu(rowMenu));
    QTest::mouseMove(m_window, sceneRect(first).center().toPoint());
    QQuickItem *rowChevron = named(first, u"rowMenuButton"_s);
    QVERIFY(rowChevron);
    // Shown on hover, after the row's other icons.
    QTRY_VERIFY(rowChevron->isVisible() && rowChevron->x() + rowChevron->width() == rowChevron->parentItem()->width());
    const QRectF button = sceneRect(rowChevron);
    click(rowChevron);
    QCOMPARE(menuPlaced(rowMenu, fromButton(button, false, true, 4)), QString());
    shoot(u"menu-row"_s);
    QVERIFY(closeMenu(rowMenu));

    // The headers' menus drop from their buttons, ending at the button's right
    // edge; the attach menu rises from +.
    const auto viaButton = [&](const QString &button, const QString &name, bool up, bool alignRight, qreal gap) {
        QQuickItem *b = named(root, button);
        QObject *m = m_window->findChild<QObject *>(name);
        if (!b || !m)
            return u"no "_s + name;
        click(b);
        const QString why = menuPlaced(m, fromButton(sceneRect(b), up, alignRight, gap));
        return closeMenu(m) ? why : name + u" stays open"_s;
    };
    QCOMPARE(viaButton(u"listMenuButton"_s, u"listMenu"_s, false, true, 4), QString());
    QCOMPARE(viaButton(u"chatMenuButton"_s, u"chatMenu"_s, false, true, 4), QString());
    QCOMPARE(viaButton(u"attachButton"_s, u"attachMenu"_s, true, false, 8), QString());

    // The composer's text menu opens at a right-click, as the browser's does
    // in Web, with what applies to the selection.
    QQuickItem *input = named(root, u"composerInput"_s);
    QObject *textLoader = m_window->findChild<QObject *>(u"textMenu"_s);
    QVERIFY(input && textLoader);
    const QString draft = input->property("text").toString();
    input->forceActiveFocus();
    type(u"see you"_s);
    key(Qt::Key_A, Qt::ControlModifier);
    at = sceneRect(input).center();
    rightClick(at);
    QObject *textMenu = nullptr;
    QTRY_VERIFY((textMenu = textLoader->property("item").value<QObject *>()));
    QCOMPARE(menuPlaced(textMenu, atPoint(at)), QString());
    QStringList entries;
    for (int i = 0; i < textMenu->property("count").toInt(); ++i) {
        QQuickItem *entry = nullptr;
        QMetaObject::invokeMethod(textMenu, "itemAt", Q_RETURN_ARG(QQuickItem *, entry), Q_ARG(int, i));
        if (entry && entry->isVisible())
            entries << entry->property("text").toString();
    }
    QVERIFY2(entries.contains(u"Undo"_s) && entries.contains(u"Cut"_s) && entries.contains(u"Copy"_s)
             && !entries.contains(u"Select all"_s), qPrintable(entries.join(u", "_s)));
    shoot(u"menu-composer"_s);
    QVERIFY(closeMenu(textMenu));
    QCOMPARE(input->property("selectedText").toString(), draft + u"see you"_s);
    input->setProperty("text", draft);

    // The smallest window.
    const QSize size = m_window->size();
    m_window->resize(m_window->minimumSize());
    QTRY_COMPARE(root->size(), QSizeF(m_window->minimumSize()));
    frame();
    const QRectF small = sceneRect(view);
    row = scrollTo(view, theirs, small.bottom() - 10);
    QVERIFY(row);
    at = small.bottomRight() - QPointF(16, 2);
    rightClick(at);
    QCOMPARE(menuPlaced(menu, atPoint(at), &card), QString());
    QCOMPARE(card.bottomRight(), at);
    QVERIFY(closeMenu(menu));
    QCOMPARE(viaButton(u"chatMenuButton"_s, u"chatMenu"_s, false, true, 4), QString());
    QCOMPARE(viaButton(u"attachButton"_s, u"attachMenu"_s, true, false, 8), QString());
    m_window->resize(size);
    QTRY_COMPARE(root->size(), QSizeF(size));
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));
}

// A voice note is WhatsApp Web's 336 px player: the waveform between the
// play button and the sender's photo, which is right of theirs and left of
// ours.
void UiTest::voiceNote()
{
    QCOMPARE(m_conv->jid(), kBig);
    QQuickItem *view = findView(m_window->contentItem(), m_conv->messages());
    QVERIFY(view);
    const auto voice = [](bool mine) {
        return [mine](const QModelIndex &i) {
            return i.data(MessageModel::TypeRole).toString() == "voice"_L1 && i.data(MessageModel::FromMeRole).toBool() == mine
                && !i.data(MessageModel::MediaWaveformRole).toByteArray().isEmpty();
        };
    };
    QString theirs, mine;
    while ((theirs = deepMessage(voice(false))).isEmpty() || (mine = deepMessage(voice(true))).isEmpty()) {
        QVERIFY(m_conv->hasMore());
        const int before = m_conv->messages()->rowCount();
        m_conv->loadOlder();
        QTRY_VERIFY(!m_conv->loading() && m_conv->messages()->rowCount() > before);
    }
    for (const QString &id : {theirs, mine}) {
        QQuickItem *row = scrollTo(view, id, sceneRect(view).center().y());
        QVERIFY(row);
        const bool fromMe = row->property("fromMe").toBool();
        const QRectF bubble = sceneRect(bubbleOf(row));
        QCOMPARE(bubble.size(), QSizeF(336, 67));
        QQuickItem *waveform = nullptr;
        for (QQuickItem *item : row->findChildren<QQuickItem *>())
            if (item->inherits("Waveform"))
                waveform = item;
        QVERIFY(waveform && waveform->isVisible());
        const QRectF w = sceneRect(waveform).translated(-bubble.topLeft());
        QCOMPARE(w, fromMe ? QRectF(122, 19, 200, 24) : QRectF(60, 19, 192, 24));
    }
}

// A reply grows the composer upwards, as in WhatsApp Web: the quote card, its
// close button 8 px inside its top right, then the input row, 12 px plus its
// text and at least 52 px, with + and send 6 px inside its bottom corners.
// Nothing clips with one line or five, in a large window or the smallest,
// and the emoji suggestions stay in the window.
void UiTest::composerReply()
{
    QCOMPARE(m_conv->jid(), kBig);
    QQuickItem *root = m_window->contentItem();
    QQuickItem *field = named(root, u"composerField"_s);
    QQuickItem *input = named(root, u"composerInput"_s);
    QQuickItem *quote = named(root, u"composerQuote"_s);
    QQuickItem *cancel = named(root, u"cancelReply"_s);
    QQuickItem *attach = named(root, u"attachButton"_s);
    QVERIFY(field && input && quote && cancel && attach);
    QTRY_VERIFY(input->hasActiveFocus());
    key(Qt::Key_A, Qt::ControlModifier);
    key(Qt::Key_Backspace);
    QVERIFY(!quote->isVisible());
    frame();
    QCOMPARE(field->height(), 52.0);
    type(u"line 1"_s);
    frame();
    QCOMPARE(field->height(), 52.0);
    const qreal line = input->property("contentHeight").toReal();
    key(Qt::Key_Return, Qt::ShiftModifier);
    type(u"line 2"_s);
    frame();
    const qreal advance = input->property("contentHeight").toReal() - line;
    key(Qt::Key_A, Qt::ControlModifier);
    key(Qt::Key_Backspace);

    // The conversation keeps following the newest message as the composer grows.
    QQuickItem *list = findView(root, m_conv->messages());
    QMetaObject::invokeMethod(list->parentItem(), "scrollToBottom");
    frame();
    const auto newestShows = [list] {
        QQuickItem *newest = itemAt(list, 0);
        return newest && list->property("pinned").toBool()
            && newest->y() + newest->height() - list->property("contentY").toReal() <= list->height() + 1;
    };
    QTRY_VERIFY(newestShows());
    m_conv->startReply(m_conv->messages()->index(0, 0).data(MessageModel::MsgIdRole).toString());
    QTRY_VERIFY(quote->isVisible() && cancel->isVisible());
    QTRY_VERIFY(newestShows());
    const QSize size = m_window->size();
    for (const QSize window : {size, m_window->minimumSize()}) {
        m_window->resize(window);
        frame();
        QTRY_COMPARE(sceneRect(field).right(), m_window->width() - 12.0);
        for (const int lines : {1, 5}) {
            type(u"line 1"_s);
            for (int i = 2; i <= lines; ++i) {
                key(Qt::Key_Return, Qt::ShiftModifier);
                type(u"line "_s + QString::number(i));
            }
            const auto laidOut = [&]() -> QString {
                const QRectF f = sceneRect(field), q = sceneRect(quote), x = sceneRect(cancel), in = sceneRect(input);
                const QRectF port = sceneRect(input->parentItem()->parentItem()); // what the scroll view shows
                const qreal row = 12 + std::max(40.0, line + (lines - 1) * advance);
                if (!QRectF(QPointF(), m_window->size()).contains(f))
                    return u"composer outside the window"_s;
                if (in.height() < input->implicitHeight() || !port.adjusted(-0.5, -0.5, 0.5, 0.5).contains(in) || !f.contains(port))
                    return u"input clipped"_s;
                if (!f.contains(q) || !f.contains(x) || q.bottom() > port.top() || x.bottom() > port.top())
                    return u"quote over the input"_s;
                if (std::abs(f.bottom() - q.bottom() - row) > 0.5)
                    return u"input row squeezed: actual %1, expected %2 (%3 lines, line %4, content %5)"_s
                        .arg(f.bottom() - q.bottom()).arg(row).arg(lines).arg(line)
                        .arg(input->property("contentHeight").toReal());
                if (std::abs(x.top() - q.top() - 8) > 0.5 || std::abs(q.right() - x.right() - 8) > 0.5)
                    return u"close button misplaced"_s;
                if (std::abs(f.bottom() - sceneRect(attach).bottom() - 6) > 0.5 || std::abs(sceneRect(attach).left() - f.left() - 6) > 0.5)
                    return u"+ misplaced"_s;
                return {};
            };
            QTRY_COMPARE(laidOut(), QString());
            shoot(u"reply-%1-lines-%2"_s.arg(lines).arg(window.height()));
            if (lines == 5 && window == m_window->minimumSize()) {
                auto *emoji = m_engine->singletonInstance<EmojiModel *>("Wisp", "EmojiSearch");
                QQuickItem *popup = findView(root, emoji)->parentItem();
                type(u" :sm"_s);
                QTRY_VERIFY(popup->isVisible());
                QVERIFY(emoji->rowCount() > 4);
                QVERIFY(QRectF(QPointF(), m_window->size()).contains(sceneRect(popup)));
                QVERIFY(sceneRect(popup).top() >= sceneRect(findView(root, m_conv->messages())).top()); // under the header
                QVERIFY(sceneRect(popup).bottom() <= sceneRect(field).top());
                shoot(u"reply-emoji"_s);
            }
            key(Qt::Key_A, Qt::ControlModifier);
            key(Qt::Key_Backspace);
        }
    }
    m_window->resize(size);
    frame();
    QTest::mouseClick(m_window, Qt::LeftButton, {}, sceneRect(cancel).center().toPoint());
    QTRY_VERIFY(!quote->isVisible());
    QVERIFY(m_conv->replyTo().toMap().isEmpty());
    QTRY_COMPARE(field->height(), 52.0);
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));
}

void UiTest::scrollMotion()
{
    const QSize original = m_window->size();
    for (const QSize size : {original, QSize(1920, 1026)}) {
        m_window->resize(size);
        m_conv->close();
        m_conv->open(kBig);
        QTRY_VERIFY(m_conv->messages()->rowCount() >= 50 && !m_conv->loading());
        auto *view = findView(m_window->contentItem(), m_conv->messages());
        QVERIFY(view);
        frame();
        QTRY_VERIFY(view->property("pinned").toBool());
        QTest::qWait(100); // finish the asynchronous delegate buffer
        const QPointF at = sceneRect(view).center();
        QElapsedTimer clock;
        clock.start();
        const auto wheel = [&](int angle) {
            QWheelEvent event(at, m_window->mapToGlobal(at), {}, {0, angle}, Qt::NoButton, {}, Qt::NoScrollPhase, false);
            event.setTimestamp(clock.msecsSinceReference() + clock.elapsed());
            QGuiApplication::sendEvent(m_window, &event);
        };
        // Ordinary notches and high-resolution wheel steps must both leave
        // the bottom, even when Qt discovers differently-sized cached rows.
        for (int angle : {120, 120, 30, 30, 120, 120}) {
            const qreal before = view->property("contentY").toReal();
            wheel(angle);
            qreal previous = before;
            for (int frame = 0; frame < 25; ++frame) {
                QTest::qWait(10);
                const qreal now = view->property("contentY").toReal();
                QVERIFY2(now <= previous + 1, "Scrolling up snapped back towards newer messages");
                previous = now;
            }
            QTRY_VERIFY(!view->property("moving").toBool());
            QVERIFY2(view->property("contentY").toReal() < before - 1,
                     qPrintable(u"Wheel step %1 at width %2: %3 -> %4"_s.arg(angle).arg(size.width())
                         .arg(before).arg(view->property("contentY").toReal())));
            QVERIFY(!view->property("pinned").toBool());
        }
        QString anchor;
        for (int row = 0; row < m_conv->messages()->rowCount(); ++row) {
            auto *item = itemAt(view, row);
            if (item && sceneRect(item).contains(at)) {
                anchor = item->property("msgId").toString();
                break;
            }
        }
        QVERIFY(!anchor.isEmpty());
        const auto anchorY = [&] {
            auto *item = itemAt(view, m_conv->messages()->indexOf(anchor));
            return item ? sceneRect(item).top() : -1e6;
        };
        const qreal beforePage = anchorY();
        const int count = m_conv->messages()->rowCount();
        m_conv->loadOlder();
        QTRY_VERIFY(!m_conv->loading() && m_conv->messages()->rowCount() > count);
        frame();
        QVERIFY2(std::abs(anchorY() - beforePage) < 1, "Loading history moved the visible message");

        // Exercise the incoming-event path while reading history and again
        // at the bottom; neither case may lose the user's scroll position.
        const auto incoming = [&](const QString &id) {
            RpcClient::instance()->event(u"message.new"_s, {{u"message"_s, QJsonObject{
                {u"id"_s, id}, {u"chat"_s, kBig}, {u"sender"_s, kBig},
                {u"senderName"_s, u"Big Chat"_s}, {u"type"_s, u"text"_s},
                {u"text"_s, u"New message while scrolling"_s},
                {u"timestamp"_s, QDateTime::currentSecsSinceEpoch() + 100}}}});
        };
        incoming(u"scroll-history"_s);
        frame();
        QVERIFY2(std::abs(anchorY() - beforePage) < 1, "An incoming message moved the visible history");
        QVERIFY(!view->property("pinned").toBool());
        shoot(u"scroll-history-%1"_s.arg(size.width()));
        QVERIFY(QMetaObject::invokeMethod(view->parentItem(), "scrollToBottom"));
        frame();
        QTRY_VERIFY(view->property("pinned").toBool());
        incoming(u"scroll-bottom"_s);
        frame();
        QTRY_VERIFY(view->property("pinned").toBool());
        auto *newest = itemAt(view, 0);
        QVERIFY(newest);
        QVERIFY(std::abs(sceneRect(newest).bottom() - sceneRect(view).bottom()) <= view->property("bottomMargin").toReal() + 1);
    }
    m_window->resize(original);
    m_conv->close();
    m_conv->open(kBig);
    QTRY_VERIFY(m_conv->messages()->rowCount() >= 50 && !m_conv->loading());
    frame();
}

// A mouse drag on the messages doesn't scroll them; the wheel does.
void UiTest::mouseDrag()
{
    QQuickItem *view = findView(m_window->contentItem(), m_conv->messages());
    QVERIFY(view);
    view->setProperty("contentY", view->property("contentY").toReal() - 400);
    frame();
    const qreal before = view->property("contentY").toReal();
    const QPointF at = sceneRect(view).center();
    QTest::mousePress(m_window, Qt::LeftButton, {}, at.toPoint());
    for (int dy = 10; dy <= 200; dy += 10) {
        QTest::mouseMove(m_window, (at + QPointF(0, dy)).toPoint());
        QTest::qWait(5);
    }
    QTest::mouseRelease(m_window, Qt::LeftButton, {}, (at + QPointF(0, 200)).toPoint());
    QTest::qWait(100);
    QCOMPARE(view->property("contentY").toReal(), before);
    QVERIFY(!view->property("moving").toBool());
    QVERIFY(QMetaObject::invokeMethod(view->parentItem(), "scrollToBottom"));
    frame();
}

// A mouse-wheel notch scrolls as far as a browser's, faster with the
// system's acceleration; trackpad swipes still follow the fingers.
void UiTest::wheelDistance()
{
    auto *view = findView(m_window->contentItem(), m_conv->messages());
    QVERIFY(view);
    const QPointF at = sceneRect(view).center();
    QElapsedTimer clock;
    clock.start();
    const auto moved = [&](QPoint pixel, QPoint angle, Qt::ScrollPhase phase, int events) -> qreal {
        QMetaObject::invokeMethod(view->parentItem(), "scrollToBottom");
        frame();
        QTest::qWait(100);
        const qreal before = view->property("contentY").toReal();
        for (int i = 0; i < events; ++i) {
            const Qt::ScrollPhase p = phase == Qt::NoScrollPhase ? phase : i == 0 ? Qt::ScrollBegin : Qt::ScrollUpdate;
            QWheelEvent e(at, m_window->mapToGlobal(at), pixel, angle, Qt::NoButton, {}, p, false);
            e.setTimestamp(clock.msecsSinceReference() + clock.elapsed());
            QGuiApplication::sendEvent(m_window, &e);
            QTest::qWait(phase == Qt::NoScrollPhase ? 40 : 8);
        }
        if (phase != Qt::NoScrollPhase) {
            QWheelEvent e(at, m_window->mapToGlobal(at), {}, {}, Qt::NoButton, {}, Qt::ScrollEnd, false);
            QGuiApplication::sendEvent(m_window, &e);
        }
        QTest::qWait(300);
        QTest::qWaitFor([&] { return !view->property("moving").toBool(); });
        return before - view->property("contentY").toReal();
    };
    const qreal notch = moved({}, {0, 120}, Qt::NoScrollPhase, 3);
    QVERIFY2(notch >= 3 * 99 && notch <= 3 * 101, qPrintable(QString::number(notch)));
#ifdef Q_OS_MACOS
    // One line, then the same notch accelerated to four lines.
    const qreal line = moved({0, 20}, {0, 120}, Qt::NoScrollPhase, 3);
    QVERIFY2(line >= 3 * 39 && line <= 3 * 41, qPrintable(QString::number(line)));
    const qreal fast = moved({0, 80}, {0, 120}, Qt::NoScrollPhase, 3);
    QVERIFY2(fast >= 3 * 159 && fast <= 3 * 161, qPrintable(QString::number(fast)));
#else
    // A wheel's pixel delta (Wayland's 15 px) doesn't shorten its notch.
    const qreal wayland = moved({0, 15}, {0, 120}, Qt::NoScrollPhase, 3);
    QVERIFY2(wayland >= 3 * 99 && wayland <= 3 * 101, qPrintable(QString::number(wayland)));
#endif
    const qreal swipe = moved({0, 10}, {0, 40}, Qt::ScrollUpdate, 20);
    QVERIFY2(swipe > 150, qPrintable(QString::number(swipe)));
    QVERIFY(!view->property("pinned").toBool());
    // Back down to the newest message, which pins the view again.
    QVERIFY(moved({}, {0, -120 * 50}, Qt::NoScrollPhase, 1) < 0);
    QTRY_VERIFY(view->property("pinned").toBool());
}

// Narrowing the mention list while it is still building rows, here to no
// match in a 30-member group, leaves no stale delegate requests behind
// (init() fails on Qt's warning about them).
void UiTest::narrowWhileLoading()
{
    const QString group = u"120363000000000007@g.us"_s; // Sydney Runners
    auto *members = m_engine->singletonInstance<QAbstractItemModel *>("Wisp", "Members");
    QQuickItem *input = named(m_window->contentItem(), u"composerInput"_s);
    QVERIFY(members && input);
    m_conv->open(group);
    QTRY_VERIFY(m_conv->jid() == group && !m_conv->loading());
    input->forceActiveFocus();
    for (int i = 0; i < 24; ++i) {
        input->setProperty("text", QString());
        type(u"@"_s);
        QTRY_VERIFY(members->rowCount() > 20);
        QTest::qWait(i % 6);
        type(u"qz"_s);
        QTRY_COMPARE(members->rowCount(), 0);
    }
    input->setProperty("text", QString());
    m_conv->open(kBig);
    QTRY_VERIFY(m_conv->messages()->rowCount() >= 50 && !m_conv->loading());
}

void UiTest::olderPages()
{
    openBigChat();
    QCOMPARE(focused()->property("text").toString(), u"draft"_s);
    QQuickItem *view = findView(m_window->contentItem(), m_conv->messages());
    QVERIFY(view);
    const QPointF at = view->mapToScene(QPointF(view->width() / 2, view->height() / 2));
    const int want = 4 * 50 + 1; // past four pages
    QElapsedTimer clock;
    clock.start();
    for (int i = 0; i < 400 && m_conv->messages()->rowCount() < want; ++i) {
        QWheelEvent wheel(at, m_window->mapToGlobal(at), {}, {0, 3 * 120}, Qt::NoButton, {}, Qt::NoScrollPhase, false);
        wheel.setTimestamp(clock.elapsed());
        QGuiApplication::sendEvent(m_window, &wheel);
        QTest::qWait(20);
    }
    QVERIFY2(m_conv->messages()->rowCount() >= want, qPrintable(QString::number(m_conv->messages()->rowCount())));
    QTRY_VERIFY(!m_conv->loading());

    // The same ids, in the same order, as the daemon pages them.
    const QStringList shown = ids(m_conv->messages());
    QStringList stored;
    while (stored.size() < shown.size()) {
        const qsizetype had = stored.size();
        RpcClient::instance()->call(u"messages.list"_s,
                                    {{u"chat"_s, kBig}, {u"before"_s, stored.value(had - 1)}, {u"limit"_s, 100}},
                                    [&](const QJsonValue &r, const QJsonObject &) {
                                        for (const QJsonValue &m : r["messages"_L1].toArray())
                                            stored << m["id"_L1].toString();
                                    });
        QTRY_VERIFY(stored.size() > had);
    }
    stored.resize(shown.size());
    QCOMPARE(QSet<QString>(shown.begin(), shown.end()).size(), shown.size());
    QCOMPARE(shown, stored);
}

// ":sku" suggests 💀 first; Enter inserts it and only the next Enter sends.
void UiTest::emojiSuggest()
{
    QCOMPARE(m_conv->jid(), kBig);
    key(Qt::Key_A, Qt::ControlModifier);
    key(Qt::Key_Backspace);
    auto *emoji = m_engine->singletonInstance<EmojiModel *>("Wisp", "EmojiSearch");
    QVERIFY(emoji);
    QQuickItem *list = findView(m_window->contentItem(), emoji);
    QVERIFY(list);
    QQuickItem *popup = list->parentItem();
    QVERIFY(!popup->isVisible());
    const auto text = [this] { return focused()->property("text").toString(); };

    type(u"hi :s"_s);
    QVERIFY(!popup->isVisible());
    type(u"ku"_s);
    QVERIFY(popup->isVisible());
    QCOMPARE(emoji->index(0).data(EmojiModel::EmojiRole).toString(), u"💀"_s);
    QCOMPARE(emoji->index(0).data(EmojiModel::NameRole).toString(), u"skull"_s);
    QCOMPARE(list->property("currentIndex").toInt(), 0);
    if (const QString dir = qEnvironmentVariable("WISP_SHOTS"); !dir.isEmpty())
        QVERIFY(m_window->grabWindow().save(dir + "/emoji-suggest.png"_L1));
    key(Qt::Key_Return);
    QCOMPARE(text(), u"hi 💀"_s);
    QVERIFY(!popup->isVisible());

    // Down moves the highlight; Esc closes the popup but not the chat; the
    // closing colon replaces a complete alias.
    type(u" :thumbs"_s);
    QVERIFY(popup->isVisible());
    key(Qt::Key_Down);
    QCOMPARE(list->property("currentIndex").toInt(), 1);
    key(Qt::Key_Escape);
    QVERIFY(!popup->isVisible());
    QCOMPARE(m_conv->jid(), kBig);
    type(u"up:"_s);
    QCOMPARE(text(), u"hi 💀 👍"_s);

    // A click inserts the clicked row.
    type(u" :heart"_s);
    QTRY_VERIFY(itemAt(list, 1));
    const QString second = emoji->index(1).data(EmojiModel::EmojiRole).toString();
    QQuickItem *row = itemAt(list, 1);
    QTest::mouseClick(m_window, Qt::LeftButton, {}, row->mapToScene(QPointF(row->width() / 2, row->height() / 2)).toPoint());
    QCOMPARE(text(), u"hi 💀 👍 "_s + second);
    QVERIFY(focused()->inherits("QQuickTextEdit"));
    // Tab inserts too.
    type(u" :joy"_s);
    key(Qt::Key_Tab);
    const QString sent = u"hi 💀 👍 "_s + second + u" 😂"_s;
    QCOMPARE(text(), sent);

    key(Qt::Key_Return);
    QCOMPARE(text(), QString());
    QTRY_VERIFY(!event(u"message.new"_s, [&](const QJsonObject &m) { return m["text"_L1] == sent; }).isEmpty());

    // Ranking, and a query takes well under a millisecond.
    EmojiModel model;
    model.query(u"thumbsup"_s);
    QCOMPARE(model.index(0).data(EmojiModel::EmojiRole).toString(), u"👍"_s);
    QCOMPARE(model.index(0).data(EmojiModel::NameRole).toString(), u"thumbsup"_s);
    model.query(u"crossbones"_s);
    QCOMPARE(model.index(0).data(EmojiModel::NameRole).toString(), u"skull_and_crossbones"_s);
    QElapsedTimer clock;
    clock.start();
    for (int i = 0; i < 100; ++i)
        model.query(i % 2 ? u"sm"_s : u"e"_s);
    const qint64 ns = clock.nsecsElapsed() / 100;
    QVERIFY2(ns < 250000, qPrintable(QString::number(ns) + " ns per query"_L1));
}

// The smiley button opens the emoji panel with its search focused; Enter
// inserts the best match into the composer and keeps the panel open; Esc
// closes it, back to the composer, and the emoji is first in Recent. The
// sticker tab sends stickers.
void UiTest::emojiPanel()
{
    QCOMPARE(m_conv->jid(), kBig);
    QQuickItem *root = m_window->contentItem();
    QQuickItem *button = named(root, u"emojiButton"_s);
    QVERIFY(button);
    const auto click = [this](QQuickItem *item) {
        QTest::mouseClick(m_window, Qt::LeftButton, {}, item->mapToScene(QPointF(item->width() / 2, item->height() / 2)).toPoint());
    };
    click(button);
    QTRY_VERIFY(focused() && focused()->objectName() == "emojiSearch"_L1);
    QQuickItem *search = focused();
    if (const QString dir = qEnvironmentVariable("WISP_SHOTS"); !dir.isEmpty())
        QVERIFY(m_window->grabWindow().save(dir + "/emoji-panel.png"_L1));

    type(u"skull"_s);
    key(Qt::Key_Return);
    QQuickItem *input = named(root, u"composerInput"_s);
    QCOMPARE(input->property("text").toString(), u"💀"_s);
    QVERIFY(search->isVisible());
    key(Qt::Key_Escape);
    QTRY_VERIFY(!search->isVisible());
    QCOMPARE(m_conv->jid(), kBig);
    QTRY_VERIFY(focused() == input);

    click(button);
    QTRY_VERIFY(focused() == search);
    QCOMPARE(search->property("text").toString(), QString());
    if (const QString dir = qEnvironmentVariable("WISP_SHOTS"); !dir.isEmpty())
        QVERIFY(m_window->grabWindow().save(dir + "/emoji-panel-recent.png"_L1));

    // The sticker tab lists recent stickers, and favourites behind the star;
    // a click sends one, and the panel reopens on the tab last used.
    click(named(root, u"stickerTab"_s));
    QQuickItem *grid = named(root, u"stickerGrid"_s);
    QVERIFY(grid);
    QTRY_COMPARE(grid->property("count").toInt(), 6);
    click(named(root, u"favoriteStickers"_s));
    QTRY_COMPARE(grid->property("count").toInt(), 2);
    QTRY_VERIFY(itemAt(grid, 0));
    QQuickItem *image = itemAt(grid, 0)->childItems().value(0);
    QVERIFY(image && image->inherits("QQuickImage"));
    QTRY_COMPARE(image->property("status").toInt(), 1); // Image.Ready
    if (const QString dir = qEnvironmentVariable("WISP_SHOTS"); !dir.isEmpty())
        QVERIFY(m_window->grabWindow().save(dir + "/sticker-panel.png"_L1));
    click(itemAt(grid, 0));
    QTRY_VERIFY(!event(u"message.new"_s, [&](const QJsonObject &m) {
        return m["fromMe"_L1].toBool() && m["type"_L1] == "sticker"_L1;
    }).isEmpty());
    QVERIFY(grid->isVisible());
    key(Qt::Key_Escape);
    QTRY_VERIFY(!grid->isVisible());
    click(button);
    QTRY_VERIFY(grid->isVisible());
    click(named(root, u"emojiTab"_s));
    QTRY_VERIFY(focused() == search);
    click(button);
    QTRY_VERIFY(!search->isVisible());
    key(Qt::Key_A, Qt::ControlModifier);
    key(Qt::Key_Backspace);
}

// In a group, "@" opens the mention picker: letters narrow it, Up and Down
// move, Enter, Tab and a click choose without sending, Esc closes it. Sending
// writes the chosen names as "@<user>" with their JIDs as mentions; a name
// edited away is no mention.
void UiTest::mentionPicker()
{
    QQuickItem *root = m_window->contentItem();
    key(Qt::Key_K, Qt::ControlModifier);
    type(u"uni mates"_s);
    key(Qt::Key_Return);
    QTRY_VERIFY(m_conv->isGroup() && m_conv->messages()->rowCount() > 0);
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));
    const QString chat = m_conv->jid();
    QQuickItem *input = focused();
    key(Qt::Key_A, Qt::ControlModifier);
    key(Qt::Key_Backspace);
    QQuickItem *picker = named(root, u"mentionPicker"_s);
    QQuickItem *list = picker ? named(picker, u"mentionList"_s) : nullptr;
    QVERIFY(list && !picker->isVisible());
    MemberModel *members = MemberModel::instance();
    const auto text = [input] { return input->property("text").toString(); };
    const auto name = [members](int row) { return members->index(row).data(MemberModel::NameRole).toString(); };
    const auto jid = [members](int row) { return members->index(row).data(MemberModel::JidRole).toString(); };
    const auto current = [list] { return list->property("currentIndex").toInt(); };
    const auto sent = [&](const std::function<bool(const QJsonObject &)> &pred) {
        for (auto it = m_events.crbegin(); it != m_events.crend(); ++it)
            if (it->first == "message.new"_L1 && it->second["chat"_L1] == chat && it->second["fromMe"_L1].toBool()
                && pred(it->second))
                return it->second;
        return QJsonObject();
    };

    // "@" lists the members but us, the first highlighted, in about six rows
    // over the messages; Down scrolls the highlight into view.
    type(u"@"_s);
    QTRY_VERIFY(picker->isVisible());
    QVERIFY(members->count() > 7);
    QCOMPARE(current(), 0);
    const QRectF card = sceneRect(picker);
    QVERIFY(QRectF(QPointF(), m_window->size()).contains(card));
    QVERIFY(card.bottom() <= sceneRect(named(root, u"composerField"_s)).top());
    QVERIFY(card.height() <= 6 * 60 + 12);
    shoot(u"mention-picker"_s);
    for (int i = 0; i < 7; ++i)
        key(Qt::Key_Down);
    QCOMPARE(current(), 7);
    QTRY_VERIFY(sceneRect(list).adjusted(0, -0.5, 0, 0.5).contains(
        sceneRect(list->property("currentItem").value<QQuickItem *>())));
    for (int i = 0; i < 7; ++i)
        key(Qt::Key_Up);
    QCOMPARE(current(), 0);

    // Letters narrow it to the names with a word starting with them; Enter
    // chooses the highlighted one and sends nothing.
    const QString first = name(2);
    const QString prefix = first.left(2);
    type(prefix);
    QTRY_VERIFY(members->count() < 14);
    int at = -1;
    for (int row = 0; row < members->count(); ++row) {
        QVERIFY2(QRegularExpression(u"(^|[^\\p{L}\\p{N}])"_s + QRegularExpression::escape(prefix),
                                    QRegularExpression::CaseInsensitiveOption).match(name(row)).hasMatch(),
                 qPrintable(name(row)));
        if (name(row) == first)
            at = row;
    }
    QVERIFY(at >= 0);
    const QString firstJid = jid(at);
    for (int i = 0; i < at; ++i)
        key(Qt::Key_Down);
    key(Qt::Key_Return);
    QCOMPARE(text(), u"@"_s + first + u' ');
    QVERIFY(!picker->isVisible());
    QVERIFY(sent([](const QJsonObject &) { return true; }).isEmpty());

    // The chosen name is coloured while composing.
    const QTextBlock block = input->property("textDocument").value<QQuickTextDocument *>()->textDocument()->firstBlock();
    QTRY_VERIFY(!block.layout()->formats().isEmpty());
    QCOMPARE(block.layout()->formats().constFirst().start, 0);
    QCOMPARE(block.layout()->formats().constFirst().length, first.size() + 1);

    // Tab chooses as well, and so does a click.
    type(u"and @"_s);
    QTRY_VERIFY(picker->isVisible());
    key(Qt::Key_Down);
    const QString second = name(1), secondJid = jid(1);
    key(Qt::Key_Tab);
    type(u"@"_s);
    QTRY_VERIFY(picker->isVisible() && harness::itemAt(list, 0));
    const QString third = name(0), thirdJid = jid(0);
    QTest::mouseClick(m_window, Qt::LeftButton, {}, sceneRect(harness::itemAt(list, 0)).center().toPoint());
    const QString typed = u"@%1 and @%2 @%3 "_s.arg(first, second, third);
    QCOMPARE(text(), typed);
    QVERIFY(focused() == input);

    // Esc closes it, and it stays closed for that "@", with the chat open.
    type(u"@"_s);
    QTRY_VERIFY(picker->isVisible());
    key(Qt::Key_Escape);
    QVERIFY(!picker->isVisible());
    QCOMPARE(m_conv->jid(), chat);
    type(u"x"_s);
    QVERIFY(!picker->isVisible());
    key(Qt::Key_Backspace);
    key(Qt::Key_Backspace);
    QCOMPARE(text(), typed);

    // Sent: "@<user>" on the wire, the JIDs as mentions; the bubble and the
    // chat list show the names.
    key(Qt::Key_Return);
    QCOMPARE(text(), QString());
    const auto user = [](const QString &j) { return j.section(u'@', 0, 0); };
    const QString wire = u"@%1 and @%2 @%3"_s.arg(user(firstJid), user(secondJid), user(thirdJid));
    QJsonObject msg;
    QTRY_VERIFY(!(msg = sent([&](const QJsonObject &m) { return m["text"_L1] == wire; })).isEmpty());
    QStringList jids;
    for (const QJsonValue &v : msg["mentions"_L1].toArray())
        jids << v["jid"_L1].toString();
    QCOMPARE(jids, (QStringList{firstJid, secondJid, thirdJid}));
    const MessageModel *model = m_conv->messages();
    QTRY_VERIFY(model->indexOf(msg["id"_L1].toString()) >= 0);
    const QString rich = model->index(model->indexOf(msg["id"_L1].toString())).data(MessageModel::RichTextRole).toString();
    QVERIFY2(rich.contains(u"href=\"mention:"_s + firstJid + u"\">@"_s) && !rich.contains(u'@' + user(firstJid)),
             qPrintable(rich));
    QTRY_COMPARE(m_chats->index(m_chats->indexOf(chat)).data(ChatListModel::PreviewRole).toString(), typed.trimmed());

    const QModelIndex chatIndex = m_chats->index(m_chats->indexOf(chat));
    QVERIFY(chatIndex.data(ChatListModel::RichPreviewRole).toString().contains(u"mention:"_s + firstJid));
    QMetaObject::invokeMethod(m_chatView, "positionViewAtIndex", Q_ARG(int, chatIndex.row()), Q_ARG(int, 1));
    frame();
    QQuickItem *chatRow = itemAt(m_chatView, chatIndex.row());
    QQuickItem *preview = chatRow ? named(chatRow, u"chatPreview"_s) : nullptr;
    QVERIFY(preview);
    QPointF linkPoint(-1, -1);
    for (qreal y = 2; y < preview->height() && linkPoint.x() < 0; y += 2)
        for (qreal x = 2; x < preview->width() && linkPoint.x() < 0; x += 2) {
            QString link;
            QMetaObject::invokeMethod(preview, "linkAt", Q_RETURN_ARG(QString, link), Q_ARG(qreal, x), Q_ARG(qreal, y));
            if (link == u"mention:"_s + firstJid)
                linkPoint = QPointF(x, y);
        }
    QVERIFY(linkPoint.x() >= 0);
    QTest::mouseClick(m_window, Qt::LeftButton, {}, preview->mapToScene(linkPoint).toPoint());
    QTRY_COMPARE(m_conv->jid(), firstJid);
    QTRY_VERIFY(!m_conv->loading());
    m_conv->open(chat);
    QTRY_VERIFY(!m_conv->loading() && input->hasActiveFocus());

    // A chosen name edited away is plain text.
    type(u"@"_s);
    QTRY_VERIFY(picker->isVisible());
    key(Qt::Key_Return);
    key(Qt::Key_Backspace);
    key(Qt::Key_Backspace);
    type(u" hi"_s);
    const QString edited = text();
    key(Qt::Key_Return);
    QTRY_VERIFY(!(msg = sent([&](const QJsonObject &m) { return m["text"_L1] == edited; })).isEmpty());
    QVERIFY(msg["mentions"_L1].toArray().isEmpty());

    // No picker outside groups.
    openBigChat();
    type(u"@"_s);
    QTest::qWait(50);
    QVERIFY(!picker->isVisible());
    key(Qt::Key_Backspace);
}

// A received mention reads "@Name" in the link colour, not underlined, and
// opens that person's chat; a mention of us reads "@You". Replies quote the names.
void UiTest::mentionRendering()
{
    // The newest received mention of someone with a chat, in a group.
    QJsonObject found;
    for (int row = 0; row < m_chats->rowCount() && found.isEmpty(); ++row) {
        if (!m_chats->index(row).data(ChatListModel::IsGroupRole).toBool())
            continue;
        const QString chat = m_chats->index(row).data(ChatListModel::JidRole).toString();
        bool done = false;
        RpcClient::instance()->call(u"messages.list"_s, {{u"chat"_s, chat}, {u"limit"_s, 12}},
                                    [&](const QJsonValue &r, const QJsonObject &) {
            for (const QJsonValue &m : r["messages"_L1].toArray()) {
                const QJsonArray mentions = m["mentions"_L1].toArray();
                if (found.isEmpty() && !m["fromMe"_L1].toBool() && m["type"_L1] == "text"_L1 && mentions.size() == 1
                    && m_chats->has(mentions[0]["jid"_L1].toString()))
                    found = m.toObject();
            }
            done = true;
        });
        QTRY_VERIFY(done);
    }
    QVERIFY(!found.isEmpty());
    const QString chat = found["chat"_L1].toString(), id = found["id"_L1].toString();
    const QJsonObject mention = found["mentions"_L1][0].toObject();
    const QString person = mention["jid"_L1].toString(), name = mention["name"_L1].toString();
    const QString token = u'@' + person.section(u'@', 0, 0);
    QVERIFY(found["text"_L1].toString().contains(token));

    m_conv->open(chat);
    const MessageModel *model = m_conv->messages();
    QTRY_VERIFY(model->indexOf(id) >= 0);
    const QModelIndex index = model->index(model->indexOf(id));
    QVERIFY(index.data(MessageModel::HasMentionsRole).toBool());
    const QString rich = index.data(MessageModel::RichTextRole).toString();
    QVERIFY2(rich.contains(u"<a class=\"m\" href=\"mention:"_s + person + u"\">@"_s + name.toHtmlEscaped() + u"</a>"_s)
                 && !rich.contains(token), qPrintable(rich));

    // Mentions of us, and the reply quote.
    QCOMPARE(Format::richText(u"@61400000000 hi"_s, {{u"61400000000@s.whatsapp.net"_s, u"You"_s}}),
             u"<a class=\"m\" href=\"mention:61400000000@s.whatsapp.net\">@You</a> hi"_s);
    QCOMPARE(Format::richText(u"*@61400000000* @614000000001 a@b"_s, {{u"61400000000@s.whatsapp.net"_s, u"You"_s}}),
             u"<b><a class=\"m\" href=\"mention:61400000000@s.whatsapp.net\">@You</a></b> @614000000001 a@b"_s);
    // A weekday's name ends in a null: StyledText chips read up to one.
    const QString weekday = Format::day(QDateTime::currentSecsSinceEpoch() - 3 * 86400);
    QVERIFY(weekday.constData()[weekday.size()].isNull());

    m_conv->startReply(id);
    QVERIFY(m_conv->replyTo().toMap().value(u"preview"_s).toString().contains(u'@' + name));
    m_conv->cancelReply();

    // On screen: RichText for the CSS, and a click on the name opens the chat.
    QQuickItem *view = findView(m_window->contentItem(), model);
    QVERIFY(view);
    QQuickItem *body = nullptr;
    QTRY_VERIFY([&] {
        QMetaObject::invokeMethod(view, "positionViewAtIndex", Q_ARG(int, index.row()), Q_ARG(int, 1)); // ListView.Center
        QQuickItem *row = itemAt(view, index.row());
        QQuickItem *bubble = row ? row->property("bubbleItem").value<QQuickItem *>() : nullptr;
        for (QQuickItem *child : bubble ? bubble->childItems() : QList<QQuickItem *>())
            if (child->inherits("QQuickText") && child->property("text").toString().contains(u"mention:"_s))
                body = child;
        return body && sceneRect(view).contains(sceneRect(body));
    }());
    QCOMPARE(body->property("textFormat").toInt(), int(Qt::RichText));
    shoot(u"mention-bubble"_s);
    QPointF at(-1, -1);
    for (qreal y = 2; y < body->height() && at.x() < 0; y += 4)
        for (qreal x = 2; x < body->width() && at.x() < 0; x += 4) {
            QString link;
            QMetaObject::invokeMethod(body, "linkAt", Q_RETURN_ARG(QString, link), Q_ARG(qreal, x), Q_ARG(qreal, y));
            if (link == u"mention:"_s + person)
                at = QPointF(x, y);
        }
    QVERIFY(at.x() >= 0);
    QTest::mouseClick(m_window, Qt::LeftButton, {}, body->mapToScene(at).toPoint());
    QTRY_COMPARE(m_conv->jid(), person);
    QTRY_VERIFY(!m_conv->loading());
    QTRY_VERIFY(focused() && focused()->inherits("QQuickTextEdit"));
    openBigChat();
}

// Unlinked from the phone: the open chat and its draft go.
// A click on a reply's quote scrolls to the original, paging older messages
// in until it is loaded, and flashes it.
void UiTest::replyJump()
{
    m_conv->open(kBig);
    const MessageModel *model = m_conv->messages();
    QTRY_VERIFY(model->rowCount() > 0 && !m_conv->loading());
    QString original;
    bool done = false;
    RpcClient::instance()->call(u"messages.list"_s, {{u"chat"_s, kBig}, {u"limit"_s, 200}},
                                [&](const QJsonValue &r, const QJsonObject &) {
        const QJsonArray page = r["messages"_L1].toArray();
        for (qsizetype i = 150; i < page.size() && original.isEmpty(); ++i)
            if (page[i]["type"_L1] == "text"_L1)
                original = page[i]["id"_L1].toString();
        done = true;
    });
    QTRY_VERIFY(done);
    QVERIFY(!original.isEmpty());
    QCOMPARE(model->indexOf(original), -1);

    const QString text = u"a jump from the ui test"_s;
    QString reply;
    RpcClient::instance()->call(u"messages.send"_s, {{u"chat"_s, kBig}, {u"text"_s, text}, {u"replyTo"_s, original}},
                                [&](const QJsonValue &r, const QJsonObject &) { reply = r["message"_L1]["id"_L1].toString(); });
    QTRY_VERIFY(!reply.isEmpty() && model->indexOf(reply) >= 0);
    QQuickItem *view = findView(m_window->contentItem(), model);
    QVERIFY(view);
    QQuickItem *quote = nullptr;
    QTRY_VERIFY([&] {
        QMetaObject::invokeMethod(view, "positionViewAtIndex", Q_ARG(int, model->indexOf(reply)), Q_ARG(int, 1));
        QQuickItem *row = itemAt(view, model->indexOf(reply));
        for (QQuickItem *child : row ? bubbleOf(row)->childItems() : QList<QQuickItem *>())
            if (child->inherits("QQuickLoader") && child->property("item").value<QQuickItem *>()
                && child->property("item").value<QQuickItem *>()->property("quote").isValid()
                && !child->property("item").value<QQuickItem *>()->property("quote").isNull())
                quote = child->property("item").value<QQuickItem *>();
        return quote && sceneRect(view).contains(sceneRect(quote));
    }());
    frame();
    QTest::mouseClick(m_window, Qt::LeftButton, {}, sceneRect(quote).center().toPoint());

    QQuickItem *row = nullptr;
    QTRY_VERIFY_WITH_TIMEOUT((row = model->indexOf(original) < 0 ? nullptr : itemAt(view, model->indexOf(original)))
                                 && row->property("flash").toBool() && sceneRect(view).contains(sceneRect(bubbleOf(row))), 10000);
}

void UiTest::unlinkClosesChat()
{
    QCOMPARE(m_conv->jid(), kBig);
    RpcClient::instance()->call(u"auth.logout"_s);
    QTRY_COMPARE(m_conv->jid(), QString());
    QVERIFY(RpcClient::instance()->authState() != "paired"_L1);
    QTRY_VERIFY(!findView(m_window->contentItem(), m_conv->messages()));
}

int main(int argc, char *argv[])
{
    QTemporaryDir dir(harness::scratchRoot() + "/wisp-uitest-XXXXXX"_L1);
    harness::isolate(dir.path());

    QGuiApplication app(argc, argv);
    app.setApplicationName(u"wisp"_s);
    app.setOrganizationName(u"wisp"_s);
    harness::useAppStyle();

    UiTest test(dir.path());
    return QTest::qExec(&test, argc, argv);
}

#include "uitest.moc"
