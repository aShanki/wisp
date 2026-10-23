#include "chatlistmodel.h"

#include "format.h"
#include "rpcclient.h"

#include <QCoreApplication>
#include <QDateTime>
#include <QJSEngine>
#include <QJsonArray>
#include <QRegularExpression>
#include <QLocale>
#include <QTimer>

#include <algorithm>

using namespace Qt::StringLiterals;

namespace {

constexpr int kTypingMs = 25000;

QString firstName(const QString &name)
{
    return name.trimmed().section(u' ', 0, 0);
}

QString chatTime(qint64 ts)
{
    if (ts <= 0)
        return {};
    const QDateTime t = QDateTime::fromSecsSinceEpoch(ts);
    const qint64 days = t.date().daysTo(QDate::currentDate());
    if (days <= 0)
        return Format::time(ts);
    if (days == 1)
        return u"Yesterday"_s;
    if (days < 7)
        return QLocale().dayName(t.date().dayOfWeek());
    return QLocale().toString(t.date(), QLocale::ShortFormat);
}

bool lessThan(const ChatListModel::Chat &a, const ChatListModel::Chat &b)
{
    if (a.pinned != b.pinned)
        return a.pinned > b.pinned; // the newest pin first
    if (a.lastActivity != b.lastActivity)
        return a.lastActivity > b.lastActivity;
    return a.jid < b.jid;
}

ChatListModel::Chat parse(const QJsonObject &o)
{
    ChatListModel::Chat c;
    c.jid = o["jid"_L1].toString();
    c.name = o["name"_L1].toString();
    if (c.name.isEmpty())
        c.name = c.jid.section(u'@', 0, 0);
    c.folded = Format::fold(c.name);
    c.group = o["kind"_L1].toString() == "group"_L1;
    const QJsonObject last = o["lastMessage"_L1].toObject();
    c.lastId = last["id"_L1].toString();
    c.lastSender = last["sender"_L1].toString();
    c.senderName = last["senderName"_L1].toString();
    c.lastFromMe = last["fromMe"_L1].toBool();
    c.lastStatus = c.lastFromMe ? last["status"_L1].toString() : QString();
    c.system = last["type"_L1].toString() == "system"_L1;
    c.preview = last["preview"_L1].toString();
    QString plain = c.preview;
    static const QRegularExpression markup(u"(```|[*_~])(\\S(?:.*?\\S)?)\\1"_s);
    plain.replace(markup, u"\\2"_s);
    QStringList names;
    QHash<QString, QString> jids;
    for (const QJsonValue &v : last["mentions"_L1].toArray()) {
        const QString name = v["name"_L1].toString();
        names << name;
        jids.insert(name, v["jid"_L1].toString());
    }
    if (!names.isEmpty()) {
        qsizetype done = 0;
        for (const auto &[at, length] : Format::nameTokens(plain, names)) {
            c.richPreview += plain.sliced(done, at - done).toHtmlEscaped();
            c.richPreview += u"<a class=\"m\" href=\"mention:"_s
                + jids.value(plain.sliced(at + 1, length - 1)).toHtmlEscaped() + u"\">"_s
                + plain.sliced(at, length).toHtmlEscaped() + u"</a>"_s;
            done = at + length;
        }
        c.richPreview += plain.sliced(done).toHtmlEscaped();
    }
    c.lastActivity = o["lastActivity"_L1].toInteger();
    c.lastTime = last.isEmpty() ? c.lastActivity : last["timestamp"_L1].toInteger();
    c.unread = std::max(o["unread"_L1].toInt(), o["markedUnread"_L1].toBool() ? 1 : 0);
    c.muted = o["muted"_L1].toBool();
    c.pinned = o["pinned"_L1].toInteger();
    c.archived = o["archived"_L1].toBool();
    c.readOnly = o["readOnly"_L1].toBool();
    return c;
}

QVariant roleData(const ChatListModel::Chat &c, int role)
{
    using M = ChatListModel;
    switch (role) {
    case M::JidRole: return c.jid;
    case M::NameRole: return c.name;
    case M::IsGroupRole: return c.group;
    case M::AvatarSourceRole: return u"image://wisp/avatar/"_s + c.jid;
    case M::PreviewRole: return c.preview;
    case M::RichPreviewRole: return c.richPreview;
    case M::PreviewSenderRole:
        if (c.lastId.isEmpty() || c.system)
            return QString();
        if (c.lastFromMe)
            return u"You"_s;
        return c.group ? firstName(c.senderName) : QString();
    case M::TimeTextRole: return chatTime(c.lastTime);
    case M::UnreadRole: return c.unread;
    case M::MutedRole: return c.muted;
    case M::TypingTextRole: return c.typingText;
    case M::PinnedRole: return c.pinned > 0;
    case M::LastStatusRole: return c.lastStatus;
    }
    return {};
}

} // namespace

ChatListModel *ChatListModel::instance()
{
    static ChatListModel *s = new ChatListModel(QCoreApplication::instance());
    return s;
}

ChatListModel *ChatListModel::create(QQmlEngine *, QJSEngine *)
{
    QJSEngine::setObjectOwnership(instance(), QJSEngine::CppOwnership);
    return instance();
}

ChatListModel::ChatListModel(QObject *parent)
    : QAbstractListModel(parent)
    , m_typingTimer(new QTimer(this))
{
    m_typingTimer->setSingleShot(true);
    connect(m_typingTimer, &QTimer::timeout, this, &ChatListModel::expireTyping);

    RpcClient *rpc = RpcClient::instance();
    connect(rpc, &RpcClient::event, this, &ChatListModel::onEvent);
    connect(rpc, &RpcClient::connectedChanged, this, &ChatListModel::syncReady);
    connect(rpc, &RpcClient::authStateChanged, this, &ChatListModel::syncReady);
    syncReady();
}

ChatListModel::~ChatListModel()
{
    qDeleteAll(m_chats);
}

int ChatListModel::rowCount(const QModelIndex &parent) const
{
    return parent.isValid() ? 0 : int(m_rows.size());
}

QHash<int, QByteArray> ChatListModel::roleNames() const
{
    return {
        {JidRole, "jid"},
        {NameRole, "name"},
        {IsGroupRole, "isGroup"},
        {AvatarSourceRole, "avatarSource"},
        {PreviewRole, "preview"},
        {RichPreviewRole, "richPreview"},
        {PreviewSenderRole, "previewSender"},
        {TimeTextRole, "timeText"},
        {UnreadRole, "unread"},
        {MutedRole, "muted"},
        {TypingTextRole, "typingText"},
        {PinnedRole, "pinned"},
        {LastStatusRole, "lastStatus"},
    };
}

QVariant ChatListModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() >= m_rows.size())
        return {};
    return roleData(*m_rows[index.row()], role);
}

const ChatListModel::Chat *ChatListModel::chat(const QString &jid) const
{
    return m_chats.value(jid);
}

bool ChatListModel::visible(const Chat &c) const
{
    return !c.archived && (m_kind != Unread || c.unread > 0) && (m_kind != Groups || c.group)
        && (m_folded.isEmpty() || c.folded.contains(m_folded) || c.jid.startsWith(m_folded));
}

int ChatListModel::rowOf(const Chat *c) const
{
    return int(m_rows.indexOf(c));
}

// Sorted position for c, as if skipRow were not in the list.
int ChatListModel::insertPos(const Chat &c, int skipRow) const
{
    int lo = 0, hi = int(m_rows.size()) - (skipRow >= 0 ? 1 : 0);
    while (lo < hi) {
        const int mid = (lo + hi) / 2;
        const int i = skipRow >= 0 && mid >= skipRow ? mid + 1 : mid;
        if (lessThan(*m_rows[i], c))
            lo = mid + 1;
        else
            hi = mid;
    }
    return lo;
}

void ChatListModel::rowChanged(const Chat *c, const QList<int> &roles)
{
    if (const int row = rowOf(c); row >= 0)
        emit dataChanged(index(row), index(row), roles);
}

void ChatListModel::store(Chat &&c)
{
    if (c.jid.isEmpty())
        return;
    Chat *p = m_chats.value(c.jid);
    if (!p) {
        p = new Chat(std::move(c));
        m_chats.insert(p->jid, p);
        if (visible(*p)) {
            const int pos = insertPos(*p);
            beginInsertRows({}, pos, pos);
            m_rows.insert(pos, p);
            endInsertRows();
            emit countChanged();
        }
        return;
    }

    // A new message from whoever was typing ends their typing.
    const bool typerSpoke = !p->typingText.isEmpty() && c.lastId != p->lastId && !c.lastFromMe
        && (!c.group || c.lastSender == p->typingJid);
    if (!typerSpoke) {
        c.typingJid = p->typingJid;
        c.typingText = p->typingText;
        c.typingUntil = p->typingUntil;
    }

    QList<int> roles;
    for (int role = JidRole; role <= LastStatusRole; ++role)
        if (roleData(*p, role) != roleData(c, role))
            roles << role;
    const int oldRow = rowOf(p);
    *p = std::move(c);
    if (typerSpoke)
        emit typingChanged(p->jid);

    const bool nowVisible = visible(*p);
    if (oldRow >= 0 && nowVisible) {
        const int row = insertPos(*p, oldRow);
        if (row != oldRow) {
            beginMoveRows({}, oldRow, oldRow, {}, row > oldRow ? row + 1 : row);
            m_rows.move(oldRow, row);
            endMoveRows();
        }
        if (!roles.isEmpty())
            emit dataChanged(index(row), index(row), roles);
    } else if (oldRow >= 0) {
        beginRemoveRows({}, oldRow, oldRow);
        m_rows.removeAt(oldRow);
        endRemoveRows();
        emit countChanged();
    } else if (nowVisible) {
        const int row = insertPos(*p);
        beginInsertRows({}, row, row);
        m_rows.insert(row, p);
        endInsertRows();
        emit countChanged();
    }
}

void ChatListModel::remove(const QString &jid)
{
    Chat *p = m_chats.take(jid);
    if (!p)
        return;
    if (const int row = rowOf(p); row >= 0) {
        beginRemoveRows({}, row, row);
        m_rows.removeAt(row);
        endRemoveRows();
        emit countChanged();
    }
    delete p;
}

void ChatListModel::applyChat(const QJsonObject &o)
{
    store(parse(o));
}

void ChatListModel::setFilterText(const QString &text)
{
    if (text == m_filter)
        return;
    m_filter = text;
    m_folded = Format::fold(text.trimmed());
    emit filterTextChanged();
    refilter();
}

void ChatListModel::setFilter(Filter f)
{
    if (f == m_kind)
        return;
    m_kind = f;
    emit filterChanged();
    refilter();
}

// Moves the rows to the new filter with removes and inserts; rows present in
// both keep their relative order, since the sort is the same.
void ChatListModel::refilter()
{
    QList<Chat *> next;
    for (Chat *c : std::as_const(m_chats))
        if (visible(*c))
            next.append(c);
    std::sort(next.begin(), next.end(), [](const Chat *a, const Chat *b) { return lessThan(*a, *b); });

    const qsizetype before = m_rows.size();
    const QSet<Chat *> keep(next.cbegin(), next.cend());
    for (int i = int(m_rows.size()) - 1; i >= 0;) {
        if (keep.contains(m_rows[i])) {
            --i;
            continue;
        }
        int j = i;
        while (j > 0 && !keep.contains(m_rows[j - 1]))
            --j;
        beginRemoveRows({}, j, i);
        m_rows.remove(j, i - j + 1);
        endRemoveRows();
        i = j - 1;
    }
    const QSet<Chat *> have(m_rows.cbegin(), m_rows.cend());
    for (int i = 0; i < next.size();) {
        if (have.contains(next[i])) {
            ++i;
            continue;
        }
        int j = i;
        while (j + 1 < next.size() && !have.contains(next[j + 1]))
            ++j;
        beginInsertRows({}, i, j);
        for (int k = i; k <= j; ++k)
            m_rows.insert(k, next[k]);
        endInsertRows();
        i = j + 1;
    }
    if (before != m_rows.size())
        emit countChanged();
}

void ChatListModel::setTyping(Chat &c, const QString &jid, const QString &text, qint64 until)
{
    const bool changed = c.typingText != text;
    c.typingJid = jid;
    c.typingText = text;
    c.typingUntil = until;
    if (changed) {
        rowChanged(&c, {TypingTextRole});
        emit typingChanged(c.jid);
    }
}

void ChatListModel::applyTyping(const QJsonObject &params)
{
    Chat *c = m_chats.value(params["chat"_L1].toString());
    if (!c)
        return;
    const QString who = params["jid"_L1].toString();
    if (params["typing"_L1].toBool()) {
        const bool audio = params["kind"_L1].toString() == "audio"_L1;
        QString text = audio ? u"recording audio…"_s : u"typing…"_s;
        if (c->group) {
            QString name = firstName(params["name"_L1].toString());
            if (name.isEmpty())
                name = who.section(u'@', 0, 0);
            text = name + u" is "_s + text;
        }
        setTyping(*c, who, text, QDateTime::currentMSecsSinceEpoch() + kTypingMs);
    } else if (!c->group || c->typingJid == who) {
        setTyping(*c, {}, {}, 0);
    }
    expireTyping();
}

// Clears expired typing and arms the one timer for the next expiry.
void ChatListModel::expireTyping()
{
    const qint64 now = QDateTime::currentMSecsSinceEpoch();
    qint64 next = 0;
    for (Chat *c : std::as_const(m_chats)) {
        if (c->typingUntil == 0)
            continue;
        if (c->typingUntil <= now)
            setTyping(*c, {}, {}, 0);
        else if (next == 0 || c->typingUntil < next)
            next = c->typingUntil;
    }
    if (next)
        m_typingTimer->start(int(next - now));
    else
        m_typingTimer->stop();
}

void ChatListModel::onEvent(const QString &name, const QJsonObject &params)
{
    if (name == "chat.update"_L1) {
        const QJsonObject chat = params["chat"_L1].toObject();
        if (m_loading)
            m_touched.insert(chat["jid"_L1].toString());
        applyChat(chat);
    } else if (name == "typing"_L1) {
        applyTyping(params);
    } else if (name == "historySync.progress"_L1 && params["done"_L1].toBool() && m_ready) {
        reload();
    }
}

// (Re)loads whenever the daemon becomes connected and paired.
void ChatListModel::syncReady()
{
    const RpcClient *rpc = RpcClient::instance();
    const bool ready = rpc->isConnected() && rpc->authState() == "paired"_L1;
    if (ready && !m_ready)
        reload();
    m_ready = ready;
}

void ChatListModel::reload()
{
    m_loadBuf.clear();
    m_touched.clear();
    if (!m_loading) {
        m_loading = true;
        emit loadingChanged();
    }
    fetchPage({}, ++m_loadGen);
}

void ChatListModel::fetchPage(const QString &cursor, quint64 gen)
{
    RpcClient::instance()->call(
        u"chats.list"_s, {{u"limit"_s, 500}, {u"cursor"_s, cursor}},
        [this, gen](const QJsonValue &result, const QJsonObject &error) {
            if (gen != m_loadGen)
                return;
            if (!error.isEmpty()) {
                m_loadBuf.clear();
                m_touched.clear();
                m_loading = false;
                emit loadingChanged();
                return;
            }
            const QJsonArray chats = result["chats"_L1].toArray();
            for (const QJsonValue &v : chats)
                m_loadBuf.append(v.toObject());
            const QString next = result["nextCursor"_L1].toString();
            if (!next.isEmpty() && !chats.isEmpty())
                fetchPage(next, gen);
            else
                finishLoad();
        });
}

void ChatListModel::finishLoad()
{
    const QList<QJsonObject> buf = std::exchange(m_loadBuf, {});
    if (m_chats.isEmpty()) {
        beginResetModel();
        for (const QJsonObject &o : buf) {
            Chat *c = new Chat(parse(o));
            delete m_chats.value(c->jid); // pages can overlap
            m_chats.insert(c->jid, c);
        }
        for (Chat *c : std::as_const(m_chats))
            if (visible(*c))
                m_rows.append(c);
        std::sort(m_rows.begin(), m_rows.end(), [](const Chat *a, const Chat *b) { return lessThan(*a, *b); });
        endResetModel();
        emit countChanged();
    } else {
        // Merge. Chats missing from the list were archived or removed.
        // Chats updated during the load already hold newer rows.
        QSet<QString> seen;
        for (const QJsonObject &o : buf) {
            Chat c = parse(o);
            seen.insert(c.jid);
            if (!m_touched.contains(c.jid))
                store(std::move(c));
        }
        for (const QString &jid : m_chats.keys())
            if (!seen.contains(jid) && !m_touched.contains(jid))
                remove(jid);
    }
    m_touched.clear();
    m_loading = false;
    emit loadingChanged();
}

void ChatListModel::call(const QString &method, QJsonObject params, const QString &jid)
{
    params.insert(u"chat"_s, jid);
    RpcClient::instance()->call(method, params, [this, jid](const QJsonValue &, const QJsonObject &error) {
        if (error.isEmpty())
            return;
        RpcClient::instance()->reportError(error["message"_L1].toString());
        // Undo the optimistic change.
        RpcClient::instance()->call(u"chats.get"_s, {{u"chat"_s, jid}},
                                    [this](const QJsonValue &r, const QJsonObject &e) {
                                        if (e.isEmpty())
                                            applyChat(r.toObject());
                                    });
    });
}

void ChatListModel::markRead(const QString &jid)
{
    Chat *c = m_chats.value(jid);
    if (c && c->unread > 0) {
        Chat next = *c;
        next.unread = 0;
        store(std::move(next));
    }
    call(u"chats.markRead"_s, {}, jid);
}

void ChatListModel::setMuted(const QString &jid, bool muted)
{
    if (Chat *c = m_chats.value(jid); c && c->muted != muted) {
        c->muted = muted;
        rowChanged(c, {MutedRole});
    }
    call(u"chats.setMuted"_s, {{u"muted"_s, muted}, {u"until"_s, 0}}, jid);
}

// Optimistic, like setMuted: the row moves at once and moves back if the
// daemon refuses (WhatsApp pins at most 3 chats).
void ChatListModel::setPinned(const QString &jid, bool pinned)
{
    Chat *c = m_chats.value(jid);
    if (!c || (c->pinned > 0) == pinned)
        return;
    Chat next = *c;
    next.pinned = pinned ? QDateTime::currentSecsSinceEpoch() : 0;
    store(std::move(next));
    call(u"chats.setPinned"_s, {{u"pinned"_s, pinned}}, jid);
}
