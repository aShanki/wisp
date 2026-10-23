// The open chat (QML singleton `Conversation`).
#pragma once

#include <QElapsedTimer>
#include <QJsonObject>
#include <QList>
#include <QObject>
#include <QString>
#include <QVariant>
#include <QtQml/qqmlregistration.h>

#include "messagemodel.h"
#include "attachmentmodel.h"

class QJSEngine;
class QQmlEngine;
class QTimer;

class ConversationController : public QObject
{
    Q_OBJECT
    QML_NAMED_ELEMENT(Conversation)
    QML_SINGLETON

    Q_PROPERTY(QString jid READ jid NOTIFY jidChanged)
    Q_PROPERTY(QString name READ name NOTIFY headerChanged)
    Q_PROPERTY(bool isGroup READ isGroup NOTIFY headerChanged)
    Q_PROPERTY(bool muted READ muted NOTIFY headerChanged)
    Q_PROPERTY(bool readOnly READ readOnly NOTIFY headerChanged)
    Q_PROPERTY(QString subtitle READ subtitle NOTIFY subtitleChanged)
    Q_PROPERTY(MessageModel *messages READ messages CONSTANT)
    Q_PROPERTY(AttachmentModel *attachments READ attachments CONSTANT)
    Q_PROPERTY(bool loading READ loading NOTIFY loadingChanged)
    Q_PROPERTY(bool hasMore READ hasMore NOTIFY hasMoreChanged)
    Q_PROPERTY(QVariant replyTo READ replyTo NOTIFY replyToChanged)
    Q_PROPERTY(QString unreadFrom READ unreadFrom NOTIFY unreadFromChanged)
    Q_PROPERTY(int unreadCount READ unreadCount NOTIFY unreadFromChanged)

public:
    static ConversationController *instance();
    static ConversationController *create(QQmlEngine *, QJSEngine *);

    QString jid() const { return m_jid; }
    QString name() const { return m_name; }
    bool isGroup() const { return m_group; }
    bool muted() const { return m_muted; }
    bool readOnly() const { return m_readOnly; }
    QString subtitle() const;
    MessageModel *messages() const { return m_model; }
    AttachmentModel *attachments() const { return m_attachments; }
    bool loading() const { return m_loading; }
    bool hasMore() const { return m_hasMore; }
    QVariant replyTo() const;
    QString unreadFrom() const { return m_unreadFrom; }
    int unreadCount() const { return m_unreadCount; }

    Q_INVOKABLE void open(const QString &jid);
    Q_INVOKABLE void close();
    Q_INVOKABLE void loadOlder();
    // mentions: the names chosen in the mention picker, name → JID; their
    // "@Name" left in text become mentions.
    Q_INVOKABLE void send(const QString &text, const QVariantMap &mentions = {});
    Q_INVOKABLE void stageFiles(const QStringList &files, const QString &caption = {});
    Q_INVOKABLE bool stageClipboardImage(const QString &caption = {});
    Q_INVOKABLE void sendAttachments();
    // id from the daemon's stickers.list.
    Q_INVOKABLE void sendSticker(const QString &id);
    Q_INVOKABLE void startReply(const QString &id);
    Q_INVOKABLE void cancelReply();
    Q_INVOKABLE void userTyping();
    Q_INVOKABLE void openMedia(const QString &id);

signals:
    void jidChanged();
    void headerChanged();
    void subtitleChanged();
    void loadingChanged();
    void hasMoreChanged();
    void replyToChanged();
    void unreadFromChanged();
    void opened();
    void newMessage(bool fromMe);

private:
    explicit ConversationController(QObject *parent = nullptr);

    void onEvent(const QString &name, const QJsonObject &params);
    void switchTo(const QString &jid);
    void loadNewest();
    void applyChat(const QJsonObject &chat);
    void markRead();
    void markUnreadFrom();
    void setLoading(bool loading);
    void setHasMore(bool hasMore);
    void setPresence(const QString &text);
    void sendMessage(const QString &method, QJsonObject params, const QString &tmp = {});
    void stopTyping();
    void fail(const QJsonObject &error, const QString &what);

    MessageModel *m_model;
    AttachmentModel *m_attachments;
    QString m_jid, m_name, m_presence;
    bool m_group = false, m_muted = false, m_readOnly = false;
    int m_unread = 0;
    int m_unreadCount = 0; // unread when the chat was opened
    QString m_unreadFrom;  // the oldest of those, once the first page is in
    bool m_loading = false, m_hasMore = false, m_loaded = false;
    quint64 m_gen = 0; // bumped on every switch; stale callbacks compare it
    QElapsedTimer m_openTimer;
    QList<QJsonObject> m_early; // message.new that raced the first page

    QVariantMap m_reply; // empty when not replying

    QString m_typingChat; // where we last sent typing=true
    QTimer *m_typingIdle;
};
