#include "messagemodel.h"

#include <QDateTime>
#include <QTextBoundaryFinder>

#include <limits>

using namespace Qt::StringLiterals;

namespace {

bool isEmoji(char32_t c)
{
    return (c >= 0x1F000 && c <= 0x1FAFF) || (c >= 0x2600 && c <= 0x27BF)
        || (c >= 0x2300 && c <= 0x23FF) || (c >= 0x2B00 && c <= 0x2BFF)
        || c == 0x00A9 || c == 0x00AE || c == 0x203C || c == 0x2049 || c == 0x2122;
}

// A stable index into the sender-name palette (Theme.senderColors), from the JID's user part.
int senderColor(const QString &jid)
{
    const QStringView user = QStringView(jid).left(jid.indexOf(u'@'));
    quint32 h = 2166136261u;
    for (QChar c : user)
        h = (h ^ c.unicode()) * 16777619u;
    return int(h % 8);
}

// One to three emoji and nothing else, not even spaces, shown large.
int emojiCount(const QString &text)
{
    if (text.isEmpty() || text.size() > 50)
        return 0;
    QTextBoundaryFinder f(QTextBoundaryFinder::Grapheme, text);
    qsizetype start = 0;
    int n = 0;
    while (f.toNextBoundary() != -1) {
        const QStringView g = QStringView(text).mid(start, f.position() - start);
        start = f.position();
        if (!isEmoji(g.toUcs4().value(0)) || ++n > 3)
            return 0;
    }
    return n;
}

QString typeLabel(const QString &type)
{
    static const QHash<QString, QString> labels = {
        {u"image"_s, u"Photo"_s},
        {u"video"_s, u"Video"_s},
        {u"gif"_s, u"GIF"_s},
        {u"audio"_s, u"Audio"_s},
        {u"voice"_s, u"Voice message"_s},
        {u"document"_s, u"Document"_s},
        {u"sticker"_s, u"Sticker"_s},
        {u"location"_s, u"Location"_s},
        {u"contact"_s, u"Contact"_s},
        {u"poll"_s, u"Poll"_s},
    };
    return labels.value(type);
}

} // namespace

MessageModel::MessageModel(QObject *parent)
    : QAbstractListModel(parent)
{
}

int MessageModel::rowCount(const QModelIndex &parent) const
{
    return parent.isValid() ? 0 : int(m_rows.size());
}

QHash<int, QByteArray> MessageModel::roleNames() const
{
    return {
        {MsgIdRole, "msgId"},
        {SenderNameRole, "senderName"},
        {FromMeRole, "fromMe"},
        {TimeTextRole, "timeText"},
        {TypeRole, "type"},
        {StatusRole, "status"},
        {EditedRole, "edited"},
        {DeletedRole, "deleted"},
        {RichTextRole, "richText"},
        {EmojiOnlyRole, "emojiOnly"},
        {EmojiCountRole, "emojiCount"},
        {ReplyToRole, "replyTo"},
        {ReactionsRole, "reactions"},
        {HasMediaRole, "hasMedia"},
        {MediaWidthRole, "mediaWidth"},
        {MediaHeightRole, "mediaHeight"},
        {MediaDurationRole, "mediaDuration"},
        {MediaWaveformRole, "mediaWaveform"},
        {SenderRole, "sender"},
        {MediaFileNameRole, "mediaFileName"},
        {MediaSizeRole, "mediaSize"},
        {MediaLoadingRole, "mediaLoading"},
        {ThumbSourceRole, "thumbSource"},
        {MediaSourceRole, "mediaSource"},
        {DayLabelRole, "dayLabel"},
        {FirstOfDayRole, "firstOfDay"},
        {FirstInGroupRole, "firstInGroup"},
        {SenderColorRole, "senderColor"},
        {HasMentionsRole, "hasMentions"},
        {SenderJidRole, "senderJid"},
    };
}

QVariant MessageModel::data(const QModelIndex &index, int role) const
{
    const int row = index.row();
    if (!index.isValid() || row >= m_rows.size())
        return {};
    const Msg &m = m_rows[row];
    switch (role) {
    case MsgIdRole: return m.id;
    case SenderNameRole: return m.fromMe ? u"You"_s : m.senderName;
    case FromMeRole: return m.fromMe;
    case TimeTextRole: return Format::time(m.timestamp);
    case TypeRole: return m.type;
    case StatusRole: return m.status;
    case EditedRole: return m.edited;
    case DeletedRole: return m.deleted;
    case RichTextRole:
        if (!m.richDone) {
            m.rich = Format::richText(m.text, m.mentions);
            m.richDone = true;
        }
        return m.rich;
    case EmojiOnlyRole: return m.emojiCount > 0;
    case EmojiCountRole: return m.emojiCount;
    case ReplyToRole:
        if (!m.isReply)
            return QVariant::fromValue(nullptr);
        return QVariantMap{{u"senderName"_s, m.replySender},
                           {u"preview"_s, m.replyPreview},
                           {u"color"_s, m.replyColor}};
    case ReactionsRole: {
        QVariantList l;
        for (const Reaction &r : m.reactions)
            l.append(QVariantMap{{u"emoji"_s, r.emoji},
                                 {u"count"_s, r.count},
                                 {u"mine"_s, r.mine}});
        return l;
    }
    case HasMediaRole: return m.hasMedia;
    case MediaWidthRole: return m.width;
    case MediaHeightRole: return m.height;
    case MediaDurationRole: return m.duration;
    case MediaWaveformRole: return m.waveform;
    case SenderRole: return m.sender;
    case MediaFileNameRole: return m.fileName;
    case MediaSizeRole: return m.size;
    case MediaLoadingRole: return m.mediaLoading;
    case ThumbSourceRole:
        return m.hasThumb ? u"image://wisp/thumb/%1/%2"_s.arg(m_chat, m.id) : QString();
    case MediaSourceRole:
        return m.isImage ? u"image://wisp/media/%1/%2"_s.arg(m_chat, m.id) : QString();
    case DayLabelRole: return firstOfDay(row) ? Format::day(m.timestamp) : QString();
    case FirstOfDayRole: return firstOfDay(row);
    case FirstInGroupRole: return firstInGroup(row);
    case SenderColorRole: return m.senderColor;
    case HasMentionsRole: return !m.mentions.isEmpty();
    case SenderJidRole: return m.sender;
    }
    return {};
}

bool MessageModel::firstOfDay(int row) const
{
    return row == m_rows.size() - 1 || m_rows[row + 1].day != m_rows[row].day;
}

bool MessageModel::firstInGroup(int row) const
{
    if (firstOfDay(row))
        return true;
    const Msg &m = m_rows[row];
    const Msg &older = m_rows[row + 1];
    return m.fromMe != older.fromMe || m.sender != older.sender || m.timestamp - older.timestamp > 300
        || m.type == "system"_L1 || older.type == "system"_L1;
}

MessageModel::Msg MessageModel::parse(const QJsonObject &o)
{
    Msg m;
    m.id = o["id"_L1].toString();
    m.sender = o["sender"_L1].toString();
    m.senderName = o["senderName"_L1].toString();
    m.type = o["type"_L1].toString();
    m.text = o["text"_L1].toString();
    m.status = o["status"_L1].toString();
    m.timestamp = o["timestamp"_L1].toInteger();
    m.day = QDateTime::fromSecsSinceEpoch(m.timestamp).date().toJulianDay();
    m.fromMe = o["fromMe"_L1].toBool();
    m.edited = o["edited"_L1].toBool();
    m.deleted = o["deleted"_L1].toBool();
    m.emojiCount = m.type == "text"_L1 ? emojiCount(m.text) : 0;
    m.senderColor = m.fromMe ? -1 : senderColor(m.sender);

    const QJsonObject r = o["replyTo"_L1].toObject();
    if (!r.isEmpty()) {
        m.isReply = true;
        m.replySender = r["fromMe"_L1].toBool()
            ? u"You"_s : r["senderName"_L1].toString();
        m.replyPreview = r["preview"_L1].toString();
        m.replyColor = r["fromMe"_L1].toBool() ? -1 : senderColor(r["sender"_L1].toString());
    }
    for (const QJsonValue &v : o["mentions"_L1].toArray())
        m.mentions.append({v["jid"_L1].toString(), v["name"_L1].toString()});
    for (const QJsonValue &v : o["reactions"_L1].toArray()) {
        const QJsonObject x = v.toObject();
        m.reactions.append({x["emoji"_L1].toString(), x["count"_L1].toInt(),
                            x["mine"_L1].toBool()});
    }
    const QJsonObject md = o["media"_L1].toObject();
    if (!md.isEmpty()) {
        m.hasMedia = true;
        m.isImage = md["mime"_L1].toString().startsWith("image/"_L1);
        m.hasThumb = md["hasThumb"_L1].toBool();
        m.size = md["size"_L1].toInteger();
        m.width = md["width"_L1].toInt();
        m.height = md["height"_L1].toInt();
        m.duration = md["duration"_L1].toInt();
        m.fileName = md["fileName"_L1].toString();
        m.path = md["path"_L1].toString();
        m.waveform = QByteArray::fromBase64(md["waveform"_L1].toString().toLatin1());
    }
    return m;
}

// Rows go by removal, not a model reset, which could leave the list's
// pending row requests pointing past the new end.
void MessageModel::reset(const QString &chat)
{
    if (!m_rows.isEmpty()) {
        beginRemoveRows({}, 0, int(m_rows.size()) - 1);
        m_rows.clear();
        m_rows.squeeze();
        endRemoveRows();
    }
    m_chat = chat;
}

// Derived roles depend on the older neighbour, so a change at `row` only
// affects the row above it.
void MessageModel::derivedChanged(int row)
{
    if (row > 0 && row <= m_rows.size()) {
        const QModelIndex i = index(row - 1);
        emit dataChanged(i, i, {DayLabelRole, FirstOfDayRole, FirstInGroupRole});
    }
}

void MessageModel::appendOlder(const QJsonArray &page)
{
    QList<Msg> add;
    add.reserve(page.size());
    const qint64 oldest = m_rows.isEmpty() ? std::numeric_limits<qint64>::max() : m_rows.last().timestamp;
    for (const QJsonValue &v : page) {
        const QJsonObject o = v.toObject();
        const qint64 ts = o["timestamp"_L1].toInteger();
        if (ts >= oldest && indexOf(o["id"_L1].toString()) >= 0)
            continue;
        if (ts > oldest)
            upsert(o); // raced with a live message; place it by time
        else
            add.append(parse(o));
    }
    if (add.isEmpty())
        return;
    const int first = int(m_rows.size());
    beginInsertRows({}, first, first + int(add.size()) - 1);
    m_rows.append(std::move(add));
    endInsertRows();
    derivedChanged(first);
}

bool MessageModel::upsert(const QJsonObject &o)
{
    Msg m = parse(o);
    if (m.id.isEmpty())
        return false;
    if (const int row = indexOf(m.id); row >= 0) {
        replace(row, std::move(m));
        return false;
    }
    int row = 0;
    while (row < m_rows.size() && m_rows[row].timestamp > m.timestamp)
        ++row;
    beginInsertRows({}, row, row);
    m_rows.insert(row, std::move(m));
    endInsertRows();
    derivedChanged(row);
    return true;
}

void MessageModel::update(const QJsonObject &o)
{
    if (const int row = indexOf(o["id"_L1].toString()); row >= 0)
        replace(row, parse(o));
}

void MessageModel::replace(int row, Msg &&n)
{
    Msg &m = m_rows[row];
    n.mediaLoading = m.mediaLoading;
    if (n.path.isEmpty())
        n.path = m.path; // fetched by us; the event may predate it
    const bool sameText = n.text == m.text && n.mentions == m.mentions;
    if (sameText) {
        n.rich = m.rich;
        n.richDone = m.richDone;
    }

    QList<int> roles;
    if (n.senderName != m.senderName) roles << SenderNameRole;
    if (n.senderColor != m.senderColor) roles << SenderColorRole;
    if (n.timestamp != m.timestamp) roles << TimeTextRole << DayLabelRole << FirstOfDayRole << FirstInGroupRole;
    if (n.type != m.type) roles << TypeRole << FirstInGroupRole;
    if (n.status != m.status) roles << StatusRole;
    if (n.edited != m.edited) roles << EditedRole;
    if (n.deleted != m.deleted) roles << DeletedRole;
    if (!sameText) roles << RichTextRole;
    if (n.mentions.isEmpty() != m.mentions.isEmpty()) roles << HasMentionsRole;
    if (n.emojiCount != m.emojiCount) roles << EmojiOnlyRole << EmojiCountRole;
    if (n.isReply != m.isReply || n.replySender != m.replySender || n.replyPreview != m.replyPreview
            || n.replyColor != m.replyColor)
        roles << ReplyToRole;
    if (n.reactions != m.reactions) roles << ReactionsRole;
    if (n.hasMedia != m.hasMedia) roles << HasMediaRole;
    if (n.width != m.width) roles << MediaWidthRole;
    if (n.height != m.height) roles << MediaHeightRole;
    if (n.duration != m.duration) roles << MediaDurationRole;
    if (n.waveform != m.waveform) roles << MediaWaveformRole;
    if (n.fileName != m.fileName) roles << MediaFileNameRole;
    if (n.size != m.size) roles << MediaSizeRole;
    if (n.hasThumb != m.hasThumb) roles << ThumbSourceRole;
    if (n.isImage != m.isImage) roles << MediaSourceRole;
    const bool regroup = n.timestamp != m.timestamp || n.type != m.type;
    m = std::move(n);
    if (roles.isEmpty())
        return;
    emit dataChanged(index(row), index(row), roles);
    if (regroup)
        derivedChanged(row);
}

int MessageModel::indexOf(const QString &id) const
{
    for (int i = 0, n = int(m_rows.size()); i < n; ++i)
        if (m_rows[i].id == id)
            return i;
    return -1;
}

const MessageModel::Msg *MessageModel::find(const QString &id) const
{
    const int i = indexOf(id);
    return i < 0 ? nullptr : &m_rows[i];
}

void MessageModel::setMediaLoading(const QString &id, bool loading)
{
    const int row = indexOf(id);
    if (row < 0 || m_rows[row].mediaLoading == loading)
        return;
    m_rows[row].mediaLoading = loading;
    emit dataChanged(index(row), index(row), {MediaLoadingRole});
}

void MessageModel::setMediaPath(const QString &id, const QString &path)
{
    const int row = indexOf(id);
    if (row < 0)
        return;
    Msg &m = m_rows[row];
    m.path = path;
    if (m.mediaLoading) {
        m.mediaLoading = false;
        emit dataChanged(index(row), index(row), {MediaLoadingRole});
    }
}

QVariantMap MessageModel::quote(const QString &id) const
{
    const Msg *m = find(id);
    if (!m)
        return {};
    QString preview = m->deleted ? u"This message was deleted"_s
                                 : Format::mentionNames(m->text.section(QLatin1Char('\n'), 0, 0), m->mentions).trimmed();
    if (preview.isEmpty())
        preview = m->fileName.isEmpty() ? typeLabel(m->type) : m->fileName;
    return {{u"id"_s, m->id},
            {u"senderName"_s, m->fromMe ? u"You"_s : m->senderName},
            {u"preview"_s, preview},
            {u"color"_s, m->senderColor}};
}
