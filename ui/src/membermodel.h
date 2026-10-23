// Group members for the composer's mention picker (QML singleton `Members`).
// The rows are the matches of the last query(), in the daemon's order. Each
// group's members are fetched once (groups.members) and kept until unlinked.
#pragma once

#include <QAbstractListModel>
#include <QHash>
#include <QList>
#include <QSet>
#include <QString>
#include <QtQml/qqmlregistration.h>

class QJSEngine;
class QQmlEngine;

class MemberModel : public QAbstractListModel
{
    Q_OBJECT
    QML_NAMED_ELEMENT(Members)
    QML_SINGLETON

    Q_PROPERTY(int count READ count NOTIFY countChanged)

public:
    enum Roles {
        JidRole = Qt::UserRole + 1,
        NameRole,
        PhoneRole, // "+61400000001", or "" when that is the name or the JID has no number
    };

    static MemberModel *instance();
    static MemberModel *create(QQmlEngine *, QJSEngine *);

    int rowCount(const QModelIndex &parent = {}) const override;
    QVariant data(const QModelIndex &index, int role) const override;
    QHash<int, QByteArray> roleNames() const override;
    int count() const { return int(m_rows.size()); }

    // The members of chat with a word of their name, or their number,
    // starting with text; all of them for "".
    Q_INVOKABLE void query(const QString &chat, const QString &text);

signals:
    void countChanged();

private:
    explicit MemberModel(QObject *parent = nullptr);

    struct Member {
        QString jid, name, phone, folded;
        bool operator==(const Member &) const = default;
    };

    void fetch(const QString &chat);
    void filter();

    QHash<QString, QList<Member>> m_members; // by chat
    QSet<QString> m_fetching;
    QString m_chat, m_query; // m_query folded
    QList<Member> m_rows;
};
