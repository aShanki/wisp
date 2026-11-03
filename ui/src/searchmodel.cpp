#include "searchmodel.h"
#include "conversation.h"
#include "rpcclient.h"
#include <QDateTime>
#include <QPointer>
#include <QRegularExpression>
using namespace Qt::StringLiterals;
namespace {
const QList<QByteArray> roles = {"chat", "chatName", "id", "senderName", "snippet", "timestamp", "richSnippet"};
constexpr int pageSize = 50;
QString highlight(const QString &text, const QString &query) {
    QStringList words;
    for (const auto &word : query.split(u' ', Qt::SkipEmptyParts))
        words.append(QRegularExpression::escape(word));
    const QRegularExpression re(words.join(u'|'), QRegularExpression::CaseInsensitiveOption);
    auto matches = re.globalMatch(text);
    QString out;
    qsizetype at = 0;
    while (matches.hasNext()) {
        const auto m = matches.next();
        out += text.sliced(at, m.capturedStart() - at).toHtmlEscaped();
        out += u"<b>"_s + m.captured().toHtmlEscaped() + u"</b>"_s;
        at = m.capturedEnd();
    }
    return out + text.sliced(at).toHtmlEscaped();
}
}
SearchModel::SearchModel(QObject *parent) : QAbstractListModel(parent) {
    m_timer.setSingleShot(true);
    m_timer.setInterval(120);
    connect(&m_timer, &QTimer::timeout, this, &SearchModel::fetch);
    auto *rpc = RpcClient::instance();
    connect(rpc, &RpcClient::connectedChanged, this, &SearchModel::restart);
    connect(rpc, &RpcClient::authStateChanged, this, &SearchModel::restart);
    connect(rpc, &RpcClient::event, this, [this](const QString &name, const QJsonObject &) {
        if (name == "message.remove"_L1 || name == "message.update"_L1
                || name == "message.new"_L1 || name == "historySync.progress"_L1) {
            if (!m_query.isEmpty()) restart();
        }
    });
}
void SearchModel::setQuery(const QString &s) {
    const QString text = s.simplified();
    if (m_query == text) return;
    m_query = text;
    restart();
}
void SearchModel::setChat(const QString &s) {
    if (m_chat == s) return;
    m_chat = s;
    restart();
}
void SearchModel::restart() {
    ++m_generation;
    m_timer.stop();
    beginResetModel(); m_rows = {}; endResetModel();
    m_error.clear(); m_more = false;
    if (!m_query.isEmpty() && RpcClient::instance()->isConnected()
            && RpcClient::instance()->authState() == "paired"_L1) m_timer.start();
    emit changed();
}
QHash<int, QByteArray> SearchModel::roleNames() const {
    QHash<int, QByteArray> out;
    for (int i = 0; i < roles.size(); ++i) out[Qt::UserRole + 1 + i] = roles[i];
    return out;
}
QVariant SearchModel::data(const QModelIndex &i, int role) const {
    const int r = role - Qt::UserRole - 1;
    if (!i.isValid() || i.row() < 0 || i.row() >= m_rows.size() || r < 0 || r >= roles.size()) return {};
    return m_rows[i.row()].toObject().value(QString::fromUtf8(roles[r])).toVariant();
}
void SearchModel::loadMore() {
    if (m_more && !busy()) fetch();
}
void SearchModel::fetch() {
    if (m_inFlight || m_query.isEmpty()) return;
    m_inFlight = true;
    emit changed();
    const quint64 gen = m_generation;
    QJsonObject params{{u"query"_s, m_query}, {u"chat"_s, m_chat}, {u"limit"_s, pageSize}};
    if (!m_rows.isEmpty()) params[u"before"_s] = m_rows.last()["cursor"_L1];
    QPointer<SearchModel> guard(this);
    RpcClient::instance()->call(u"search"_s, params, [this, guard, gen](const QJsonValue &v, const QJsonObject &e) {
        if (!guard) return;
        m_inFlight = false;
        if (gen != m_generation) {
            if (!m_query.isEmpty() && RpcClient::instance()->isConnected()
                    && RpcClient::instance()->authState() == "paired"_L1 && !m_timer.isActive()) m_timer.start();
            emit changed();
            return;
        }
        m_error = e["message"_L1].toString();
        const QJsonArray page = v["results"_L1].toArray();
        m_more = e.isEmpty() && page.size() == pageSize;
        if (!page.isEmpty()) {
            beginInsertRows({}, int(m_rows.size()), int(m_rows.size() + page.size() - 1));
            for (const auto &r : page) {
                auto hit = r.toObject();
                hit[u"richSnippet"_s] = highlight(hit["snippet"_L1].toString(), m_query);
                m_rows.append(hit);
            }
            endInsertRows();
        }
        emit changed();
    });
}
void SearchModel::open(int row) {
    if (row < 0 || row >= m_rows.size()) return;
    const auto r = m_rows[row].toObject();
    ConversationController::instance()->openMessage(r["chat"_L1].toString(), r["id"_L1].toString());
}
