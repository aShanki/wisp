#pragma once
#include <QAbstractListModel>
#include <QJsonArray>
#include <QTimer>
#include <QtQml/qqmlregistration.h>

// Independent instances for global and in-chat search. Only one RPC per
// instance can be in flight; edits coalesce while it runs.
class SearchModel : public QAbstractListModel {
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QString query READ query WRITE setQuery NOTIFY changed)
    Q_PROPERTY(QString chat READ chat WRITE setChat NOTIFY changed)
    Q_PROPERTY(bool busy READ busy NOTIFY changed)
    Q_PROPERTY(bool hasMore READ hasMore NOTIFY changed)
    Q_PROPERTY(QString error READ error NOTIFY changed)
    Q_PROPERTY(int count READ rowCount NOTIFY changed)
public:
    explicit SearchModel(QObject *parent = nullptr);
    QString query() const { return m_query; }
    QString chat() const { return m_chat; }
    bool busy() const { return m_timer.isActive() || m_inFlight; }
    bool hasMore() const { return m_more; }
    QString error() const { return m_error; }
    void setQuery(const QString &s);
    void setChat(const QString &s);
    int rowCount(const QModelIndex &p = {}) const override { return p.isValid() ? 0 : int(m_rows.size()); }
    QVariant data(const QModelIndex &i, int role) const override;
    QHash<int, QByteArray> roleNames() const override;
    Q_INVOKABLE void loadMore();
    Q_INVOKABLE void open(int row);
signals:
    void changed();
private:
    void restart();
    void fetch();
    QString m_query, m_chat, m_error;
    QJsonArray m_rows;
    QTimer m_timer;
    quint64 m_generation = 0;
    bool m_inFlight = false, m_more = false;
};
