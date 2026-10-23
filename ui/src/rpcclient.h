// RpcClient: the connection to wispd (QML singleton `Daemon`).
// Line-delimited JSON-RPC 2.0 over a QLocalSocket, on the GUI thread.
#pragma once

#include <QHash>
#include <QJSValue>
#include <QJsonObject>
#include <QJsonValue>
#include <QList>
#include <QLocalSocket>
#include <QObject>
#include <QTimer>
#include <QtQml/qqmlregistration.h>

#include <functional>

class QJSEngine;
class QQmlEngine;

// error is empty on success, else {code, message}.
using RpcCallback = std::function<void(const QJsonValue &result, const QJsonObject &error)>;

class RpcClient : public QObject
{
    Q_OBJECT
    QML_NAMED_ELEMENT(Daemon)
    QML_SINGLETON

    Q_PROPERTY(bool connected READ isConnected NOTIFY connectedChanged)
    Q_PROPERTY(bool callsAvailable READ callsAvailable NOTIFY connectedChanged)
    Q_PROPERTY(QString authState READ authState NOTIFY authStateChanged)
    Q_PROPERTY(QString connectionState READ connectionState NOTIFY connectionStateChanged)
    Q_PROPERTY(QString qrCode READ qrCode NOTIFY qrCodeChanged)
    Q_PROPERTY(QString pairCode READ pairCode NOTIFY pairCodeChanged)
    Q_PROPERTY(int syncPercent READ syncPercent NOTIFY syncPercentChanged)
    Q_PROPERTY(bool windowActive READ windowActive WRITE setWindowActive NOTIFY windowActiveChanged)

public:
    struct Options {
        bool mock = false;
        QString socket; // --socket
        QString daemon; // --daemon
        QString argv0;  // to find wispd next to a symlinked wisp
    };

    static RpcClient *instance();
    static RpcClient *create(QQmlEngine *, QJSEngine *js);

    void start(const Options &opts);

    // Calls made before the daemon answers are queued; they fail with code -1
    // if it cannot be reached. Without a callback, errors go to errorOccurred.
    void call(const QString &method, const QJsonObject &params = {}, RpcCallback cb = {});
    // QML: Daemon.call("chats.get", {chat: jid}, function(result, error) {...}); error is null on success.
    Q_INVOKABLE void call(const QString &method, const QJSValue &params, const QJSValue &callback = QJSValue());

    Q_INVOKABLE void startQR();
    Q_INVOKABLE void pairPhone(const QString &phone);
    Q_INVOKABLE void logout();

    // The open chat ("" for none); with windowActive it drives ui.setActive.
    void setActiveChat(const QString &jid);
    void reportError(const QString &message) { emit errorOccurred(message); }

    bool isConnected() const { return m_connected; }
    bool callsAvailable() const { return m_connected && m_calls; }
    QString authState() const { return m_authState; }
    QString connectionState() const { return m_connectionState; }
    QString qrCode() const { return m_qrCode; }
    QString pairCode() const { return m_pairCode; }
    int syncPercent() const { return m_syncPercent; }
    bool windowActive() const { return m_windowActive; }
    void setWindowActive(bool active);

signals:
    void connectedChanged();
    void authStateChanged();
    void connectionStateChanged();
    void qrCodeChanged();
    void pairCodeChanged();
    void syncPercentChanged();
    void windowActiveChanged();

    void event(const QString &name, const QJsonObject &params);
    void openChatRequested(const QString &jid);
    void errorOccurred(const QString &message);

private:
    explicit RpcClient(QObject *parent);
    ~RpcClient() override;

    void connectNow();
    void onDisconnected();
    void retry();
    void giveUp(const QString &message);
    bool spawnDaemon();
    QString daemonPath() const;
    void onHello(const QJsonObject &r);
    void send(const QString &method, const QJsonObject &params, RpcCallback cb);
    void readLines();
    void handleEvent(const QString &name, const QJsonObject &p);
    void syncActive();
    void setAuthState(const QString &state);
    void setQr(const QString &code);
    void setPairCode(const QString &code);
    void setSyncPercent(int percent);

    struct Queued {
        QString method;
        QJsonObject params;
        RpcCallback cb;
    };

    Options m_opts;
    QString m_socketPath;
    QLocalSocket m_socket;
    QTimer m_retry;
    int m_backoffMs = 100;
    bool m_spawned = false; // spawned wispd since the last connection
    bool m_failed = false;  // gave up; calls fail until connected
    QByteArray m_buf;
    quint64 m_nextId = 1;
    QHash<quint64, RpcCallback> m_pending;
    QList<Queued> m_queue;
    QJSEngine *m_js = nullptr;

    bool m_connected = false;
    bool m_calls = false; // daemon.hello: calls can be made
    QString m_authState = QStringLiteral("unknown");
    QString m_connectionState = QStringLiteral("disconnected");
    QString m_qrCode;
    QString m_pairCode;
    int m_syncPercent = -1;
    bool m_windowActive = false;
    QString m_activeChat;
    QString m_sentChat; // last ui.setActive sent
    bool m_sentFocused = false;
};
