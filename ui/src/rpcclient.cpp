#include "rpcclient.h"

#include <QCoreApplication>
#include <QFileInfo>
#include <QJSEngine>
#include <QJsonDocument>
#include <QLoggingCategory>
#include <QProcess>
#include <QStandardPaths>

Q_LOGGING_CATEGORY(lcRpc, "wisp.rpc", QtInfoMsg)

using namespace Qt::StringLiterals;

namespace {
constexpr int kMaxBackoffMs = 10000;
constexpr int kGiveUpBackoffMs = 3200; // fail queued calls after about 3 s

QJsonObject makeError(int code, const QString &message)
{
    return {{u"code"_s, code}, {u"message"_s, message}};
}

QString str(const QJsonObject &o, const char *key)
{
    return o.value(QLatin1String(key)).toString();
}
} // namespace

RpcClient *RpcClient::instance()
{
    static RpcClient *s = new RpcClient(QCoreApplication::instance());
    return s;
}

RpcClient *RpcClient::create(QQmlEngine *, QJSEngine *js)
{
    RpcClient *c = instance();
    QJSEngine::setObjectOwnership(c, QJSEngine::CppOwnership);
    c->m_js = js;
    return c;
}

RpcClient::RpcClient(QObject *parent)
    : QObject(parent)
{
    m_retry.setSingleShot(true);
    connect(&m_retry, &QTimer::timeout, this, &RpcClient::connectNow);
    connect(&m_socket, &QLocalSocket::connected, this, [this] {
        call(u"daemon.hello"_s,
             {{u"client"_s, u"wisp"_s}, {u"version"_s, QStringLiteral(WISP_VERSION)}},
             [this](const QJsonValue &result, const QJsonObject &error) {
                 if (error.isEmpty())
                     onHello(result.toObject());
                 else
                     m_socket.abort();
             });
    });
    connect(&m_socket, &QLocalSocket::disconnected, this, &RpcClient::onDisconnected);
    connect(&m_socket, &QLocalSocket::errorOccurred, this, [this] {
        if (!m_connected)
            retry(); // a dropped connection goes through disconnected
    });
    connect(&m_socket, &QLocalSocket::readyRead, this, &RpcClient::readLines);
}

RpcClient::~RpcClient()
{
    m_socket.disconnect(this); // no callbacks while members are being destroyed
}

void RpcClient::start(const Options &opts)
{
    m_opts = opts;
    m_socketPath = opts.socket;
    if (m_socketPath.isEmpty())
        m_socketPath = qEnvironmentVariable("WISP_SOCKET");
    if (m_socketPath.isEmpty()) {
        QString dir = qEnvironmentVariable("XDG_RUNTIME_DIR");
        if (dir.isEmpty())
            dir = QStandardPaths::writableLocation(QStandardPaths::RuntimeLocation);
        m_socketPath = dir + (opts.mock ? "/wisp-mock.sock"_L1 : "/wisp.sock"_L1);
    }
    connectNow();
}

void RpcClient::connectNow()
{
    m_socket.abort();
    m_socket.connectToServer(m_socketPath);
}

void RpcClient::onDisconnected()
{
    m_buf.clear();
    const bool was = std::exchange(m_connected, false);
    const auto pending = std::exchange(m_pending, {});
    for (const RpcCallback &cb : pending)
        cb({}, makeError(-1, tr("Lost connection to wispd")));
    if (was) {
        m_spawned = false;
        m_backoffMs = 100;
        qCInfo(lcRpc) << "disconnected from wispd";
        setSyncPercent(-1);
        emit connectedChanged();
    }
    retry();
}

// Single-shot backoff: spawn wispd on the first failure, then retry at
// 100 ms, 200 ms, … up to 10 s. Nothing runs while connected.
void RpcClient::retry()
{
    if (m_connected || m_retry.isActive())
        return;
    if (!m_spawned) {
        m_spawned = true;
        if (!spawnDaemon())
            giveUp(tr("Could not start wispd"));
    } else if (m_backoffMs >= kGiveUpBackoffMs) {
        giveUp(tr("Cannot reach wispd at %1").arg(m_socketPath));
    }
    m_retry.start(m_backoffMs);
    m_backoffMs = qMin(m_backoffMs * 2, kMaxBackoffMs);
}

void RpcClient::giveUp(const QString &message)
{
    if (m_failed)
        return;
    m_failed = true;
    qCWarning(lcRpc).noquote() << message;
    emit errorOccurred(message);
    const auto queue = std::exchange(m_queue, {});
    for (const Queued &q : queue)
        q.cb({}, makeError(-1, message));
}

QString RpcClient::daemonPath() const
{
    if (!m_opts.daemon.isEmpty())
        return m_opts.daemon;
    QStringList dirs{QCoreApplication::applicationDirPath()};
    if (m_opts.argv0.contains(u'/'))
        dirs.prepend(QFileInfo(m_opts.argv0).absolutePath());
    for (const QString &dir : dirs) {
        const QFileInfo fi(dir + "/wispd"_L1);
        if (fi.isExecutable())
            return fi.filePath();
    }
    return QStandardPaths::findExecutable(u"wispd"_s);
}

bool RpcClient::spawnDaemon()
{
    const QString path = daemonPath();
    if (path.isEmpty())
        return false;
    QStringList args;
    if (m_opts.mock)
        args << u"--mock"_s;
    args << u"--socket"_s << m_socketPath;

    QProcess p;
    p.setProgram(path);
    p.setArguments(args);
    p.setStandardInputFile(QProcess::nullDevice());
    p.setStandardOutputFile(QProcess::nullDevice());
    p.setStandardErrorFile(QProcess::nullDevice());
    p.setUnixProcessParameters(QProcess::UnixProcessFlag::CreateNewSession
                               | QProcess::UnixProcessFlag::ResetSignalHandlers
                               | QProcess::UnixProcessFlag::CloseFileDescriptors);
    if (!p.startDetached())
        return false;
    qCInfo(lcRpc).noquote() << "started" << path << args.join(u' ');
    return true;
}

void RpcClient::onHello(const QJsonObject &r)
{
    m_connected = true;
    m_failed = false;
    m_backoffMs = 100;
    setAuthState(str(r, "auth"));
    m_calls = r.value("calls"_L1).toBool();
    m_connectionState = str(r, "connection");
    emit connectionStateChanged();
    m_sentChat.clear();
    m_sentFocused = false;
    for (Queued &q : std::exchange(m_queue, {}))
        send(q.method, q.params, std::move(q.cb));
    emit connectedChanged();
    syncActive();
}

void RpcClient::call(const QString &method, const QJsonObject &params, RpcCallback cb)
{
    if (!cb) {
        cb = [this, method](const QJsonValue &, const QJsonObject &error) {
            if (error.isEmpty())
                return;
            qCWarning(lcRpc).noquote() << method << "failed:" << QJsonDocument(error).toJson(QJsonDocument::Compact);
            if (error.value("code"_L1).toInt() != -1)
                emit errorOccurred(str(error, "message"));
        };
    }
    const bool hello = method == "daemon.hello"_L1;
    if (m_connected || hello) {
        send(method, params, std::move(cb));
    } else if (m_failed) {
        // Fail asynchronously so callers are never re-entered.
        QMetaObject::invokeMethod(this, [cb = std::move(cb)] {
            cb({}, makeError(-1, tr("Not connected to wispd")));
        }, Qt::QueuedConnection);
    } else {
        m_queue.append({method, params, std::move(cb)});
    }
}

void RpcClient::call(const QString &method, const QJSValue &params, const QJSValue &callback)
{
    const QJsonObject p = QJsonObject::fromVariantMap(params.toVariant().toMap());
    if (!callback.isCallable()) {
        call(method, p);
        return;
    }
    call(method, p, [this, callback](const QJsonValue &result, const QJsonObject &error) {
        if (!m_js)
            return;
        const QJSValue err = error.isEmpty() ? QJSValue(QJSValue::NullValue) : m_js->toScriptValue(error);
        const QJSValue ret = callback.call({m_js->toScriptValue(result), err});
        if (ret.isError())
            qCWarning(lcRpc).noquote() << "callback error:" << ret.toString();
    });
}

void RpcClient::send(const QString &method, const QJsonObject &params, RpcCallback cb)
{
    const quint64 id = m_nextId++;
    m_pending.insert(id, std::move(cb));
    QJsonObject req{{u"jsonrpc"_s, u"2.0"_s},
                    {u"id"_s, qint64(id)},
                    {u"method"_s, method}};
    if (!params.isEmpty())
        req.insert(u"params"_s, params);
    m_socket.write(QJsonDocument(req).toJson(QJsonDocument::Compact).append('\n'));
}

void RpcClient::readLines()
{
    m_buf.append(m_socket.readAll());
    qsizetype start = 0;
    for (qsizetype nl; (nl = m_buf.indexOf('\n', start)) >= 0; start = nl + 1) {
        const QJsonObject msg = QJsonDocument::fromJson(m_buf.sliced(start, nl - start)).object();
        const QJsonValue id = msg.value("id"_L1);
        if (id.isDouble()) {
            if (RpcCallback cb = m_pending.take(quint64(id.toInteger()))) {
                const QJsonValue err = msg.value("error"_L1);
                cb(msg.value("result"_L1), err.isObject() ? err.toObject() : QJsonObject());
            }
        } else if (msg.contains("method"_L1)) {
            handleEvent(str(msg, "method"), msg.value("params"_L1).toObject());
        }
        if (m_buf.isEmpty())
            return; // disconnected inside a callback
    }
    m_buf.remove(0, start);
}

void RpcClient::handleEvent(const QString &name, const QJsonObject &p)
{
    if (name == "auth.qr"_L1) {
        setQr(str(p, "code"));
    } else if (name == "auth.state"_L1) {
        const QString state = str(p, "state");
        if (state != "pairing"_L1)
            setQr({});
        if (state == "paired"_L1)
            setPairCode({});
        setAuthState(state);
    } else if (name == "connection.state"_L1) {
        m_connectionState = str(p, "state");
        emit connectionStateChanged();
    } else if (name == "historySync.progress"_L1) {
        if (p.value("done"_L1).toBool())
            setSyncPercent(-1);
        else if (const int percent = p.value("percent"_L1).toInt(-1); percent >= 0)
            setSyncPercent(qMin(percent, 100));
    } else if (name == "ui.openChat"_L1) {
        // On Wayland, QWindow::requestActivate() activates with this token.
        if (const QString token = str(p, "token"); !token.isEmpty())
            qputenv("XDG_ACTIVATION_TOKEN", token.toUtf8());
        emit openChatRequested(str(p, "chat"));
    } else if (name == "ui.quit"_L1) {
        m_socket.disconnect(this); // wispd is going away on purpose: don't respawn it
        QCoreApplication::quit();
    }
    emit event(name, p);
}

void RpcClient::startQR()
{
    setPairCode({});
    call(u"auth.startQR"_s);
}

void RpcClient::pairPhone(const QString &phone)
{
    setPairCode({});
    call(u"auth.pairPhone"_s, {{u"phone"_s, phone}},
         [this](const QJsonValue &result, const QJsonObject &error) {
             if (!error.isEmpty())
                 emit errorOccurred(str(error, "message"));
             else
                 setPairCode(str(result.toObject(), "code"));
         });
}

void RpcClient::logout()
{
    call(u"auth.logout"_s);
}

void RpcClient::setActiveChat(const QString &jid)
{
    m_activeChat = jid;
    syncActive();
}

void RpcClient::setWindowActive(bool active)
{
    if (m_windowActive == active)
        return;
    m_windowActive = active;
    emit windowActiveChanged();
    syncActive();
}

void RpcClient::syncActive()
{
    if (!m_connected || (m_sentChat == m_activeChat && m_sentFocused == m_windowActive))
        return;
    m_sentChat = m_activeChat;
    m_sentFocused = m_windowActive;
    call(u"ui.setActive"_s, {{u"chat"_s, m_activeChat}, {u"focused"_s, m_windowActive}});
}

void RpcClient::setAuthState(const QString &state)
{
    if (state.isEmpty() || state == m_authState)
        return;
    m_authState = state;
    emit authStateChanged();
}

void RpcClient::setQr(const QString &code)
{
    if (code == m_qrCode)
        return;
    m_qrCode = code;
    emit qrCodeChanged();
}

void RpcClient::setPairCode(const QString &code)
{
    if (code == m_pairCode)
        return;
    m_pairCode = code;
    emit pairCodeChanged();
}

void RpcClient::setSyncPercent(int percent)
{
    if (percent == m_syncPercent)
        return;
    m_syncPercent = percent;
    emit syncPercentChanged();
}
