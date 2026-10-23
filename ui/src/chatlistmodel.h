// The chat list (QML singleton `ChatList`). Every known chat is stored; the
// rows are the non-archived ones matching filterText and filter, in protocol order.
// Rows are kept with insert/remove/move signals rather than a
// QSortFilterProxyModel, whose source moves become layout changes that make
// views rebuild every delegate.
#pragma once

#include <QAbstractListModel>
#include <QHash>
#include <QJsonObject>
#include <QList>
#include <QSet>
#include <QString>
#include <QtQml/qqmlregistration.h>

class QJSEngine;
class QQmlEngine;
class QTimer;

class ChatListModel : public QAbstractListModel
{
    Q_OBJECT
    QML_NAMED_ELEMENT(ChatList)
    QML_SINGLETON

    Q_PROPERTY(QString filterText READ filterText WRITE setFilterText NOTIFY filterTextChanged)
    Q_PROPERTY(Filter filter READ filter WRITE setFilter NOTIFY filterChanged)
    Q_PROPERTY(int count READ count NOTIFY countChanged)
    Q_PROPERTY(bool loading READ loading NOTIFY loadingChanged)

public:
    enum Filter { All, Unread, Groups };
    Q_ENUM(Filter)

    enum Roles {
        JidRole = Qt::UserRole + 1,
        NameRole,
        IsGroupRole,
        AvatarSourceRole,
        PreviewRole,
        RichPreviewRole,
        PreviewSenderRole,
        TimeTextRole,
        UnreadRole,
        MutedRole,
        TypingTextRole,
        PinnedRole,
        LastStatusRole,
    };

    struct Chat {
        QString jid, name, folded; // folded: name for filtering
        QString preview, richPreview, senderName, lastSender, lastId, lastStatus;
        qint64 lastTime = 0, lastActivity = 0;
        int unread = 0;
        bool group = false, lastFromMe = false, system = false;
        bool muted = false, archived = false, readOnly = false;
        qint64 pinned = 0; // when it was pinned, 0 = not pinned
        QString typingJid, typingText;
        qint64 typingUntil = 0; // ms since epoch
    };

    static ChatListModel *instance();
    static ChatListModel *create(QQmlEngine *, QJSEngine *);

    int rowCount(const QModelIndex &parent = {}) const override;
    QVariant data(const QModelIndex &index, int role) const override;
    QHash<int, QByteArray> roleNames() const override;

    QString filterText() const { return m_filter; }
    void setFilterText(const QString &text);
    Filter filter() const { return m_kind; }
    void setFilter(Filter f);
    int count() const { return int(m_rows.size()); }
    bool loading() const { return m_loading; }

    Q_INVOKABLE void markRead(const QString &jid);
    Q_INVOKABLE void setMuted(const QString &jid, bool muted);
    Q_INVOKABLE void setPinned(const QString &jid, bool pinned);
    Q_INVOKABLE int indexOf(const QString &jid) const { return rowOf(chat(jid)); }
    // Whether the chat is stored, archived or not.
    Q_INVOKABLE bool has(const QString &jid) const { return m_chats.contains(jid); }

    // Any stored chat, archived or not, or nullptr.
    const Chat *chat(const QString &jid) const;
    // Store a chat object from the daemon (chat.update, chats.get).
    void applyChat(const QJsonObject &o);

signals:
    void filterTextChanged();
    void filterChanged();
    void countChanged();
    void loadingChanged();
    void typingChanged(const QString &jid);

private:
    explicit ChatListModel(QObject *parent = nullptr);
    ~ChatListModel() override;

    void onEvent(const QString &name, const QJsonObject &params);
    void syncReady();
    void reload();
    void fetchPage(const QString &cursor, quint64 gen);
    void finishLoad();

    bool visible(const Chat &c) const;
    int rowOf(const Chat *c) const;
    int insertPos(const Chat &c, int skipRow = -1) const;
    void store(Chat &&c);
    void remove(const QString &jid);
    void refilter();
    void setTyping(Chat &c, const QString &jid, const QString &text, qint64 until);
    void applyTyping(const QJsonObject &params);
    void expireTyping();
    void rowChanged(const Chat *c, const QList<int> &roles);
    void call(const QString &method, QJsonObject params, const QString &jid);

    QHash<QString, Chat *> m_chats; // owned
    QList<Chat *> m_rows;

    QString m_filter, m_folded;
    Filter m_kind = All;
    bool m_loading = false;
    bool m_ready = false; // connected and paired

    quint64 m_loadGen = 0;
    QList<QJsonObject> m_loadBuf;
    QSet<QString> m_touched; // chat.update during a load: newer than the snapshot

    QTimer *m_typingTimer = nullptr;
};
