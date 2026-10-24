#include "conversation.h"

#include "chatlistmodel.h"
#include "format.h"
#include "perflog.h"
#include "rpcclient.h"

#include <QClipboard>
#include <QCoreApplication>
#include <QDateTime>
#include <QDesktopServices>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QGuiApplication>
#include <QImage>
#include <QImageReader>
#include <QJSEngine>
#include <QJsonArray>
#include <QLocale>
#include <QMimeData>
#include <QTemporaryFile>
#include <QStandardPaths>
#include <QTimer>
#include <QUrl>

#include <algorithm>
#include <utility>

using namespace Qt::StringLiterals;

namespace {

constexpr int kPageSize = 50;
constexpr int kTypingIdleMs = 5000;

QString lastSeen(qint64 ts)
{
    if (ts <= 0)
        return {};
    const QDateTime t = QDateTime::fromSecsSinceEpoch(ts);
    const qint64 days = t.date().daysTo(QDate::currentDate());
    if (days <= 0)
        return u"last seen today at %1"_s.arg(Format::time(ts));
    if (days == 1)
        return u"last seen yesterday at %1"_s.arg(Format::time(ts));
    return u"last seen %1"_s.arg(QLocale().toString(t.date(), QLocale::ShortFormat));
}

} // namespace

ConversationController *ConversationController::instance()
{
    static ConversationController *s = new ConversationController(QCoreApplication::instance());
    return s;
}

ConversationController *ConversationController::create(QQmlEngine *, QJSEngine *)
{
    QJSEngine::setObjectOwnership(instance(), QJSEngine::CppOwnership);
    return instance();
}

ConversationController::ConversationController(QObject *parent)
    : QObject(parent)
    , m_model(new MessageModel(this))
    , m_attachments(new AttachmentModel(this))
    , m_typingIdle(new QTimer(this))
{
    m_typingIdle->setSingleShot(true);
    m_typingIdle->setInterval(kTypingIdleMs);
    connect(m_typingIdle, &QTimer::timeout, this, &ConversationController::stopTyping);

    RpcClient *rpc = RpcClient::instance();
    connect(rpc, &RpcClient::event, this, &ConversationController::onEvent);
    connect(rpc, &RpcClient::windowActiveChanged, this, &ConversationController::markRead);
    connect(rpc, &RpcClient::connectedChanged, this, [this, rpc] {
        if (!rpc->isConnected() || m_jid.isEmpty())
            return;
        loadNewest();
        if (!m_group)
            rpc->call(u"presence.subscribe"_s, {{u"jid"_s, m_jid}});
    });
    connect(ChatListModel::instance(), &ChatListModel::typingChanged, this, [this](const QString &jid) {
        if (jid == m_jid)
            emit subtitleChanged();
    });
}

QString ConversationController::subtitle() const
{
    const ChatListModel::Chat *c = ChatListModel::instance()->chat(m_jid);
    return c && !c->typingText.isEmpty() ? c->typingText : m_presence;
}

QVariant ConversationController::replyTo() const
{
    return m_reply.isEmpty() ? QVariant::fromValue(nullptr) : QVariant(m_reply);
}

void ConversationController::setLoading(bool loading)
{
    if (loading != m_loading) {
        m_loading = loading;
        emit loadingChanged();
    }
}

void ConversationController::setHasMore(bool hasMore)
{
    if (hasMore != m_hasMore) {
        m_hasMore = hasMore;
        emit hasMoreChanged();
    }
}

void ConversationController::setPresence(const QString &text)
{
    if (text != m_presence) {
        m_presence = text;
        emit subtitleChanged();
    }
}

void ConversationController::fail(const QJsonObject &error, const QString &what)
{
    RpcClient::instance()->reportError(what + u": "_s + error["message"_L1].toString());
}

void ConversationController::open(const QString &jid)
{
    if (jid == m_jid) {
        markRead();
        return;
    }
    m_openTimer.start();
    switchTo(jid);
}

void ConversationController::close()
{
    switchTo({});
}

void ConversationController::switchTo(const QString &jid)
{
    stopTyping();
    m_attachments->clear();
    ++m_gen;
    m_jid = jid;
    m_presence.clear();
    m_reply.clear();
    m_early.clear();
    m_loaded = false;
    m_model->reset(jid);
    setLoading(false);
    setHasMore(false);

    // Header from the chat list now; chats.get refines it.
    const ChatListModel::Chat *c = ChatListModel::instance()->chat(jid);
    m_name = c ? c->name : jid.section(u'@', 0, 0);
    m_group = c ? c->group : jid.endsWith("@g.us"_L1);
    m_muted = c && c->muted;
    m_readOnly = c && c->readOnly;
    m_unread = c ? c->unread : 0;
    m_unreadCount = m_unread;
    m_unreadFrom.clear();

    emit jidChanged();
    emit unreadFromChanged();
    emit headerChanged();
    emit subtitleChanged();
    emit replyToChanged();

    RpcClient *rpc = RpcClient::instance();
    rpc->setActiveChat(jid);
    if (jid.isEmpty())
        return;

    loadNewest();
    const quint64 gen = m_gen;
    rpc->call(u"chats.get"_s, {{u"chat"_s, jid}}, [this, gen](const QJsonValue &r, const QJsonObject &e) {
        if (gen != m_gen || !e.isEmpty())
            return;
        ChatListModel::instance()->applyChat(r.toObject()); // keeps archived chats' typing too
        applyChat(r.toObject());
    });
    if (!m_group)
        rpc->call(u"presence.subscribe"_s, {{u"jid"_s, jid}});
    markRead();
}

// The newest page: the first page after a switch, or a merge after
// reconnecting or a history sync.
void ConversationController::loadNewest()
{
    const quint64 gen = m_gen;
    setLoading(true);
    RpcClient::instance()->call(
        u"messages.list"_s, {{u"chat"_s, m_jid}, {u"limit"_s, kPageSize}},
        [this, gen](const QJsonValue &result, const QJsonObject &error) {
            if (gen != m_gen)
                return;
            setLoading(false);
            if (!error.isEmpty()) {
                fail(error, u"Could not load messages"_s);
                return;
            }
            const QJsonArray page = result["messages"_L1].toArray();
            if (m_loaded) {
                const bool overlaps = std::any_of(page.begin(), page.end(), [this](const QJsonValue &v) {
                    return m_model->indexOf(v["id"_L1].toString()) >= 0;
                });
                if (overlaps) {
                    for (qsizetype i = page.size() - 1; i >= 0; --i)
                        m_model->upsert(page[i].toObject());
                    if (result["hasMore"_L1].toBool())
                        setHasMore(true);
                    return;
                }
                m_model->reset(m_jid); // a gap: start over
            }
            m_model->appendOlder(page);
            setHasMore(result["hasMore"_L1].toBool());
            for (const QJsonObject &m : std::exchange(m_early, {}))
                m_model->upsert(m);
            if (!m_loaded) {
                m_loaded = true;
                markUnreadFrom();
                PerfLog::print("WISP_CHAT_OPEN", m_openTimer.elapsed());
                emit opened();
            }
        });
}

void ConversationController::loadOlder()
{
    if (m_loading || !m_hasMore || !m_model->oldest())
        return;
    const quint64 gen = m_gen;
    setLoading(true);
    RpcClient::instance()->call(
        u"messages.list"_s,
        {{u"chat"_s, m_jid}, {u"before"_s, m_model->oldest()->id}, {u"limit"_s, kPageSize}},
        [this, gen](const QJsonValue &result, const QJsonObject &error) {
            if (gen != m_gen)
                return;
            setLoading(false);
            if (!error.isEmpty()) {
                fail(error, u"Could not load messages"_s);
                return;
            }
            m_model->appendOlder(result["messages"_L1].toArray());
            setHasMore(result["hasMore"_L1].toBool());
        });
}

void ConversationController::applyChat(const QJsonObject &c)
{
    if (c["jid"_L1].toString() != m_jid)
        return;
    const QString name = c["name"_L1].toString();
    const bool group = c["kind"_L1].toString() == "group"_L1;
    const bool muted = c["muted"_L1].toBool();
    const bool readOnly = c["readOnly"_L1].toBool();
    m_unread = std::max(c["unread"_L1].toInt(), c["markedUnread"_L1].toBool() ? 1 : 0);
    if ((!name.isEmpty() && name != m_name) || group != m_group || muted != m_muted || readOnly != m_readOnly) {
        if (!name.isEmpty())
            m_name = name;
        m_group = group;
        m_muted = muted;
        m_readOnly = readOnly;
        emit headerChanged();
    }
    markRead();
}

// The oldest of the messages that were unread on opening, for the divider.
void ConversationController::markUnreadFrom()
{
    int left = m_unreadCount;
    for (int row = 0; row < m_model->rowCount() && left > 0; ++row) {
        const MessageModel::Msg &m = m_model->at(row);
        if (m.fromMe || m.type == "system"_L1)
            continue;
        m_unreadFrom = m.id;
        --left;
    }
    if (!m_unreadFrom.isEmpty())
        emit unreadFromChanged();
}

// Read while open in the active window.
void ConversationController::markRead()
{
    if (m_jid.isEmpty() || m_unread <= 0 || !RpcClient::instance()->windowActive())
        return;
    m_unread = 0;
    ChatListModel::instance()->markRead(m_jid);
}

void ConversationController::onEvent(const QString &name, const QJsonObject &params)
{
    if (m_jid.isEmpty())
        return;
    if (name == "message.new"_L1 || name == "message.update"_L1) {
        const QJsonObject msg = params["message"_L1].toObject();
        if (msg["chat"_L1].toString() != m_jid)
            return;
        // Messages older than the loaded pages arrive with paging instead.
        const MessageModel::Msg *oldest = m_model->oldest();
        const bool unloaded = m_hasMore && oldest && msg["timestamp"_L1].toInteger() < oldest->timestamp;
        if (name == "message.update"_L1)
            m_model->update(msg);
        else if (!m_loaded)
            m_early.append(msg);
        else if (!unloaded && m_model->upsert(msg))
            emit newMessage(msg["fromMe"_L1].toBool());
    } else if (name == "message.remove"_L1) {
        if (params["chat"_L1].toString() != m_jid)
            return;
        const QString id = params["id"_L1].toString();
        if (m_reply.value(u"id"_s).toString() == id)
            cancelReply();
        m_model->remove(id);
    } else if (name == "chat.update"_L1) {
        applyChat(params["chat"_L1].toObject());
    } else if (name == "historySync.progress"_L1) {
        if (params["done"_L1].toBool())
            loadNewest();
    } else if (name == "presence"_L1) {
        if (params["jid"_L1].toString() == m_jid && !m_group)
            setPresence(params["online"_L1].toBool() ? u"online"_s : lastSeen(params["lastSeen"_L1].toInteger()));
    }
}

// tmp is a file to delete once the daemon has its copy.
void ConversationController::sendMessage(const QString &method, QJsonObject params, const QString &tmp)
{
    params.insert(u"chat"_s, m_jid);
    params.insert(u"replyTo"_s, m_reply.value(u"id"_s).toString());
    cancelReply();
    const quint64 gen = m_gen;
    RpcClient::instance()->call(method, params, [this, gen, tmp](const QJsonValue &result, const QJsonObject &e) {
        if (!tmp.isEmpty())
            QFile::remove(tmp);
        if (!e.isEmpty()) {
            fail(e, u"Not sent"_s);
            return;
        }
        if (gen == m_gen && m_loaded && m_model->upsert(result["message"_L1].toObject()))
            emit newMessage(true);
    });
}

void ConversationController::send(const QString &text, const QVariantMap &mentions)
{
    const QString typed = text.trimmed();
    if (m_jid.isEmpty() || typed.isEmpty())
        return;
    stopTyping();
    // On the wire a mention is "@" and the JID's user part.
    QString body;
    QJsonArray jids;
    qsizetype done = 0;
    for (const auto &[at, length] : Format::nameTokens(typed, mentions.keys())) {
        const QString jid = mentions.value(typed.sliced(at + 1, length - 1)).toString();
        body += QStringView(typed).sliced(done, at - done);
        body += u'@' + Format::user(jid);
        if (!jids.contains(jid))
            jids.append(jid);
        done = at + length;
    }
    body += QStringView(typed).sliced(done);
    sendMessage(u"messages.send"_s, {{u"text"_s, body}, {u"mentions"_s, jids}});
}

// Text with an image (office apps offer both) pastes as text.
bool ConversationController::stageClipboardImage(const QString &caption)
{
    const QMimeData *md = QGuiApplication::clipboard()->mimeData();
    if (m_jid.isEmpty() || m_readOnly || m_attachments->count() >= 100 || !md || !md->hasImage() || md->hasText())
        return false;
    const QImage image = qvariant_cast<QImage>(md->imageData());
    if (image.isNull())
        return false;
    const QString cache = QStandardPaths::writableLocation(QStandardPaths::CacheLocation);
    QDir().mkpath(cache);
    QTemporaryFile tmp(QDir(cache).filePath(u"wisp-paste-XXXXXX.png"_s));
    tmp.setAutoRemove(false);
    if (!tmp.open() || !image.save(&tmp, "PNG")) {
        tmp.remove();
        RpcClient::instance()->reportError(u"Could not save the pasted image"_s);
        return false;
    }
    tmp.close();
    m_attachments->add(tmp.fileName(), caption, true);
    return true;
}

void ConversationController::stageFiles(const QStringList &files, const QString &caption)
{
    if (m_jid.isEmpty() || m_readOnly)
        return;
    for (const QString &path : files) {
        const QUrl url(path);
        const QFileInfo info(url.isLocalFile() ? url.toLocalFile() : path);
        if (!info.isFile() || !info.isReadable()) {
            RpcClient::instance()->reportError(u"Cannot read %1"_s.arg(info.fileName()));
            continue;
        }
        if (m_attachments->count() >= 100) {
            RpcClient::instance()->reportError(u"You can send up to 100 attachments at once"_s);
            break;
        }
        m_attachments->add(info.absoluteFilePath(), m_attachments->count() == 0 ? caption : QString());
    }
}

void ConversationController::sendAttachments()
{
    if (m_jid.isEmpty() || m_readOnly)
        return;
    stopTyping();
    for (const auto &file : m_attachments->take())
        sendMessage(u"messages.sendMedia"_s, {{u"path"_s, file.path}, {u"caption"_s, file.caption.trimmed()}},
                    file.temporary ? file.path : QString());
}

void ConversationController::sendSticker(const QString &id)
{
    if (m_jid.isEmpty() || m_readOnly || id.isEmpty())
        return;
    stopTyping();
    sendMessage(u"messages.sendSticker"_s, {{u"sticker"_s, id}});
}

void ConversationController::startReply(const QString &id)
{
    const QVariantMap q = m_model->quote(id);
    if (q.isEmpty())
        return;
    m_reply = q;
    emit replyToChanged();
}

void ConversationController::cancelReply()
{
    if (m_reply.isEmpty())
        return;
    m_reply.clear();
    emit replyToChanged();
}

// typing=true on the first keystroke, false after 5 s idle, on send, or on switching chats.
void ConversationController::userTyping()
{
    if (m_jid.isEmpty())
        return;
    if (m_typingChat != m_jid) {
        stopTyping();
        m_typingChat = m_jid;
        RpcClient::instance()->call(u"chats.setTyping"_s, {{u"chat"_s, m_jid}, {u"typing"_s, true}});
    }
    m_typingIdle->start();
}

void ConversationController::stopTyping()
{
    m_typingIdle->stop();
    if (m_typingChat.isEmpty())
        return;
    RpcClient::instance()->call(u"chats.setTyping"_s, {{u"chat"_s, m_typingChat}, {u"typing"_s, false}});
    m_typingChat.clear();
}

// Calls done with the media's local path, downloading it first if needed.
void ConversationController::withMediaPath(const QString &id, std::function<void(const QString &)> done)
{
    const MessageModel::Msg *m = m_model->find(id);
    if (!m || !m->hasMedia)
        return;
    if (!m->path.isEmpty() && QFileInfo::exists(m->path)) {
        done(m->path);
        return;
    }
    m_model->setMediaLoading(id, true);
    const quint64 gen = m_gen;
    RpcClient::instance()->call(
        u"media.fetch"_s, {{u"chat"_s, m_jid}, {u"id"_s, id}},
        [this, gen, id, done = std::move(done)](const QJsonValue &result, const QJsonObject &e) {
            if (gen != m_gen)
                return;
            if (!e.isEmpty()) {
                m_model->setMediaLoading(id, false);
                fail(e, u"Download failed"_s);
                return;
            }
            const QString path = result["path"_L1].toString();
            m_model->setMediaPath(id, path);
            if (!path.isEmpty())
                done(path);
        });
}

// A call on one message of this chat; the result arrives as events.
void ConversationController::act(const QString &method, QJsonObject params, const QString &what)
{
    if (m_jid.isEmpty())
        return;
    params.insert(u"chat"_s, m_jid);
    RpcClient::instance()->call(method, params, [this, what](const QJsonValue &, const QJsonObject &e) {
        if (!e.isEmpty())
            fail(e, what);
    });
}

void ConversationController::react(const QString &id, const QString &emoji)
{
    act(u"messages.react"_s, {{u"id"_s, id}, {u"emoji"_s, emoji}}, u"Reaction not sent"_s);
}

void ConversationController::deleteForEveryone(const QString &id)
{
    act(u"messages.revoke"_s, {{u"id"_s, id}}, u"Not deleted"_s);
}

void ConversationController::deleteForMe(const QString &id)
{
    act(u"messages.deleteForMe"_s, {{u"id"_s, id}}, u"Not deleted"_s);
}

// Opens the media, downloading it first if needed.
void ConversationController::openMedia(const QString &id)
{
    withMediaPath(id, [](const QString &path) { QDesktopServices::openUrl(QUrl::fromLocalFile(path)); });
}

// Puts the full-size image on the clipboard.
void ConversationController::copyImage(const QString &id)
{
    withMediaPath(id, [](const QString &path) {
        QImageReader reader(path);
        reader.setAutoTransform(true);
        const QImage image = reader.read();
        if (image.isNull()) {
            RpcClient::instance()->reportError(u"Could not copy the image"_s);
            return;
        }
        QGuiApplication::clipboard()->setImage(image);
    });
}
