#include "emojimodel.h"

#include <QResource>

#include <array>

using namespace Qt::StringLiterals;

namespace {

enum Tier { Exact, AliasPrefix, WordPrefix, Substring, NoMatch };

Tier tierOf(QByteArrayView key, QByteArrayView q, bool alias)
{
    if (alias && key == q)
        return Exact;
    if (key.startsWith(q))
        return alias ? AliasPrefix : WordPrefix;
    Tier tier = NoMatch;
    for (qsizetype at = key.indexOf(q, 1); at > 0; at = key.indexOf(q, at + 1)) {
        if (key[at - 1] == '_' || key[at - 1] == '-')
            return WordPrefix;
        tier = Substring;
    }
    return tier;
}

} // namespace

int EmojiModel::rowCount(const QModelIndex &parent) const
{
    return parent.isValid() ? 0 : int(m_rows.size());
}

QVariant EmojiModel::data(const QModelIndex &index, int role) const
{
    if (!checkIndex(index, CheckIndexOption::IndexIsValid))
        return {};
    const Row &r = m_rows.at(index.row());
    switch (role) {
    case EmojiRole: return r.emoji;
    case NameRole: return r.name;
    }
    return {};
}

QHash<int, QByteArray> EmojiModel::roleNames() const
{
    return {{EmojiRole, "emoji"}, {NameRole, "name"}};
}

// One line per emoji: the emoji, TAB, its aliases (the primary one first), TAB,
// its tags; aliases and tags are separated by spaces.
void EmojiModel::load()
{
    if (m_loaded)
        return;
    m_loaded = true;
    m_data = QResource(u":/assets/emoji/emoji.tsv"_s).uncompressedData();
    const QByteArrayView d(m_data);
    m_entries.reserve(d.count('\n'));
    for (qsizetype i = 0; i < d.size();) {
        qsizetype end = d.indexOf('\n', i);
        if (end < 0)
            end = d.size();
        const qsizetype aliases = d.indexOf('\t', i) + 1;
        const qsizetype tags = aliases > 0 ? d.indexOf('\t', aliases) + 1 : 0;
        if (aliases > i + 1 && tags > aliases + 1 && tags <= end)
            m_entries.append({int(i), int(aliases), int(tags), int(end)});
        i = end + 1;
    }
    if (m_entries.isEmpty())
        qWarning("no emoji table in the resources");
}

void EmojiModel::query(const QString &text)
{
    load();
    const QByteArray q = text.toLatin1().toLower();
    if (q.isEmpty()) {
        setRows({});
        return;
    }
    std::array<QList<Row>, NoMatch> tiers;
    const char *d = m_data.constData();
    for (const Entry &e : std::as_const(m_entries)) {
        Tier best = NoMatch;
        QByteArrayView name, primary;
        for (int i = e.aliases; i < e.end && best != Exact;) {
            int j = i;
            while (j < e.end && d[j] != ' ' && d[j] != '\t')
                ++j;
            const QByteArrayView key(d + i, j - i);
            const bool alias = i < e.tags;
            if (primary.isNull())
                primary = key;
            if (const Tier t = tierOf(key, q, alias); t < best) {
                best = t;
                name = alias ? key : primary;
            }
            i = j + 1;
        }
        if (best != NoMatch && tiers[best].size() < kMaxRows)
            tiers[best].append({QString::fromUtf8(d + e.emoji, e.aliases - 1 - e.emoji), QString::fromLatin1(name)});
    }
    QList<Row> rows;
    for (const QList<Row> &tier : tiers)
        rows.append(tier.first(std::min(tier.size(), kMaxRows - rows.size())));
    setRows(rows);
}

QString EmojiModel::lookup(const QString &alias)
{
    load();
    const QByteArray q = alias.toLatin1().toLower();
    if (q.isEmpty())
        return {};
    const char *d = m_data.constData();
    for (const Entry &e : std::as_const(m_entries)) {
        const QByteArrayView aliases(d + e.aliases, e.tags - 1 - e.aliases);
        for (qsizetype at = aliases.indexOf(q); at >= 0; at = aliases.indexOf(q, at + 1)) {
            const qsizetype after = at + q.size();
            if ((at == 0 || aliases[at - 1] == ' ') && (after == aliases.size() || aliases[after] == ' '))
                return QString::fromUtf8(d + e.emoji, e.aliases - 1 - e.emoji);
        }
    }
    return {};
}

// Rows are replaced in place, so the view keeps its delegates.
void EmojiModel::setRows(const QList<Row> &rows)
{
    const qsizetype old = m_rows.size(), n = rows.size();
    if (n < old) {
        beginRemoveRows({}, int(n), int(old - 1));
        m_rows.resize(n);
        endRemoveRows();
    }
    qsizetype first = -1, last = -1;
    for (qsizetype i = 0; i < std::min(old, n); ++i) {
        if (m_rows[i] == rows[i])
            continue;
        m_rows[i] = rows[i];
        if (first < 0)
            first = i;
        last = i;
    }
    if (first >= 0)
        emit dataChanged(index(int(first)), index(int(last)), {EmojiRole, NameRole});
    if (n > old) {
        beginInsertRows({}, int(old), int(n - 1));
        m_rows.append(rows.sliced(old));
        endInsertRows();
    }
    if (n != old)
        emit countChanged();
}
