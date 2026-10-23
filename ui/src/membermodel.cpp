#include "membermodel.h"

#include "format.h"
#include "rpcclient.h"

#include <QCoreApplication>
#include <QJSEngine>
#include <QJsonArray>
#include <QJsonObject>

using namespace Qt::StringLiterals;

namespace {

// A word of the name, or the number after its "+", starts with query.
bool matches(const QString &folded, const QString &phone, const QString &query)
{
    if (query.isEmpty() || (!phone.isEmpty() && QStringView(phone).sliced(1).startsWith(query)))
        return true;
    for (qsizetype at = folded.indexOf(query); at >= 0; at = folded.indexOf(query, at + 1))
        if (at == 0 || !folded[at - 1].isLetterOrNumber())
            return true;
    return false;
}

} // namespace

MemberModel *MemberModel::instance()
{
    static MemberModel *s = new MemberModel(QCoreApplication::instance());
    return s;
}

MemberModel *MemberModel::create(QQmlEngine *, QJSEngine *)
{
    QJSEngine::setObjectOwnership(instance(), QJSEngine::CppOwnership);
    return instance();
}

MemberModel::MemberModel(QObject *parent)
    : QAbstractListModel(parent)
{
    RpcClient *rpc = RpcClient::instance();
    connect(rpc, &RpcClient::authStateChanged, this, [this, rpc] {
        if (rpc->authState() != "paired"_L1) {
            m_members.clear();
            m_fetching.clear();
            m_chat.clear();
            filter();
        }
    });
}

int MemberModel::rowCount(const QModelIndex &parent) const
{
    return parent.isValid() ? 0 : int(m_rows.size());
}

QVariant MemberModel::data(const QModelIndex &index, int role) const
{
    if (!checkIndex(index, CheckIndexOption::IndexIsValid))
        return {};
    const Member &m = m_rows.at(index.row());
    switch (role) {
    case JidRole: return m.jid;
    case NameRole: return m.name;
    case PhoneRole: return m.phone;
    }
    return {};
}

QHash<int, QByteArray> MemberModel::roleNames() const
{
    return {{JidRole, "jid"}, {NameRole, "name"}, {PhoneRole, "phone"}};
}

void MemberModel::query(const QString &chat, const QString &text)
{
    m_chat = chat;
    m_query = Format::fold(text);
    if (!m_members.contains(chat))
        fetch(chat);
    filter();
}

void MemberModel::fetch(const QString &chat)
{
    if (chat.isEmpty() || m_fetching.contains(chat))
        return;
    m_fetching.insert(chat);
    RpcClient::instance()->call(u"groups.members"_s, {{u"chat"_s, chat}},
                                [this, chat](const QJsonValue &result, const QJsonObject &error) {
        if (!m_fetching.remove(chat) || !error.isEmpty())
            return; // unlinked meanwhile, or failed: the next query asks again
        QList<Member> members;
        for (const QJsonValue &v : result["members"_L1].toArray()) {
            Member m;
            m.jid = v["jid"_L1].toString();
            m.name = v["name"_L1].toString();
            m.folded = Format::fold(m.name);
            if (m.jid.endsWith("@s.whatsapp.net"_L1))
                m.phone = u'+' + Format::user(m.jid);
            if (m.phone == m.name)
                m.phone.clear(); // known only by number
            members.append(m);
        }
        m_members.insert(chat, members);
        if (chat == m_chat)
            filter();
    });
}

void MemberModel::filter()
{
    QList<Member> rows;
    for (const Member &m : m_members.value(m_chat))
        if (matches(m.folded, m.phone, m_query))
            rows.append(m);
    if (rows == m_rows)
        return;
    // Removed, then inserted: after a reset, the list's pending row
    // requests could point past the new end.
    const qsizetype before = m_rows.size();
    if (!m_rows.isEmpty()) {
        beginRemoveRows({}, 0, int(m_rows.size()) - 1);
        m_rows.clear();
        endRemoveRows();
    }
    if (!rows.isEmpty()) {
        beginInsertRows({}, 0, int(rows.size()) - 1);
        m_rows = std::move(rows);
        endInsertRows();
    }
    if (m_rows.size() != before)
        emit countChanged();
}
