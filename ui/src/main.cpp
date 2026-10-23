#include "imageprovider.h"
#include "perflog.h"
#include "rpcclient.h"

#include <QCommandLineParser>
#include <QGuiApplication>
#include <QFont>
#include <QIcon>
#include <QQmlApplicationEngine>
#include <QQuickStyle>
#include <QQuickWindow>
#include <QTimer>
#include <QtQml/QQmlExtensionPlugin>

Q_IMPORT_QML_PLUGIN(WispPlugin)

using namespace Qt::StringLiterals;

#ifdef Q_OS_MACOS
void setupMacDesktop(QQuickWindow *, RpcClient *);
#endif

int main(int argc, char *argv[])
{
    PerfLog::elapsed();
    // Without QApplication, KDE's platform theme only offers the system file
    // dialog through the desktop portal; otherwise Qt falls back to its own.
    if (!qEnvironmentVariableIsSet("PLASMA_INTEGRATION_USE_PORTAL"))
        qputenv("PLASMA_INTEGRATION_USE_PORTAL", "1");
    // Software rendering without EGL: Mesa's GL stack alone costs about 60 MB,
    // more than the rest of the UI. WISP_GPU=1 renders on the GPU instead.
    if (!qEnvironmentVariableIsSet("WISP_GPU")) {
        qputenv("QT_WAYLAND_DISABLE_HW_INTEGRATION", "1");
        QQuickWindow::setGraphicsApi(QSGRendererInterface::Software);
    }
    QGuiApplication app(argc, argv);
    app.setFont(QFont(u"Arial"_s));
    app.setApplicationName(u"wisp"_s);
    app.setOrganizationName(u"wisp"_s);
    app.setApplicationVersion(QStringLiteral(WISP_VERSION));
    app.setDesktopFileName(u"wisp"_s);
    app.setWindowIcon(QIcon::fromTheme(u"wisp"_s));

    QCommandLineParser cli;
    cli.addHelpOption();
    cli.addVersionOption();
    const QCommandLineOption mock(u"mock"_s, u"Use the mock daemon."_s);
    const QCommandLineOption socket(u"socket"_s, u"Daemon socket."_s, u"path"_s);
    const QCommandLineOption daemon(u"daemon"_s, u"wispd to start if none is running."_s, u"path"_s);
    const QCommandLineOption chat(u"chat"_s, u"Open this chat."_s, u"jid"_s);
    const QCommandLineOption shot(u"screenshot"_s, u"Save a screenshot once loaded, then quit."_s, u"file"_s);
    const QCommandLineOption shotDelay(u"screenshot-delay"_s, u"Wait before the screenshot."_s, u"ms"_s, u"300"_s);
    const QCommandLineOption size(u"size"_s, u"Window size."_s, u"WxH"_s);
    cli.addOptions({mock, socket, daemon, chat, shot, shotDelay, size});
    cli.process(app);

    if (qEnvironmentVariableIsEmpty("QT_QUICK_CONTROLS_STYLE"))
        QQuickStyle::setStyle(u"Basic"_s);

    RpcClient *rpc = RpcClient::instance();
    rpc->start({cli.isSet(mock), cli.value(socket), cli.value(daemon), QString::fromLocal8Bit(argv[0])});

    QQmlApplicationEngine engine;
    engine.addImageProvider(u"wisp"_s, new ImageProvider);
    engine.loadFromModule("Wisp", "Main");
    auto *window = engine.rootObjects().isEmpty() ? nullptr : qobject_cast<QQuickWindow *>(engine.rootObjects().constFirst());
    if (!window)
        return 1;
#ifdef Q_OS_MACOS
    if (!cli.isSet(shot))
        setupMacDesktop(window, rpc);
#endif
    if (const QStringList wh = cli.value(size).split(u'x'); wh.size() == 2)
        window->resize(wh[0].toInt(), wh[1].toInt());

    // Loaded: the chat list has rows, or the daemon is waiting to be linked.
    // Then report WISP_READY, open --chat, and take the --screenshot.
    QObject *chatList = engine.singletonInstance<QObject *>("Wisp", "ChatList");
    QObject *conversation = engine.singletonInstance<QObject *>("Wisp", "Conversation");
    const QString openChat = cli.value(chat);
    const QString shotFile = cli.value(shot);

    QTimer shotTimer;
    shotTimer.setSingleShot(true);
    shotTimer.setInterval(cli.value(shotDelay).toInt());
    QObject::connect(&shotTimer, &QTimer::timeout, &app, [&] {
        const bool ok = window->grabWindow().save(shotFile);
        if (!ok)
            qWarning("could not save %s", qPrintable(shotFile));
        QCoreApplication::exit(ok ? 0 : 1);
    });
    if (!shotFile.isEmpty()) {
        QObject::connect(conversation, SIGNAL(opened()), &shotTimer, SLOT(start()));
        QTimer::singleShot(15000, &shotTimer, [&] { // never loaded: show what there is
            if (shotTimer.isActive())
                return;
            qWarning("not loaded after 15 s");
            shotTimer.start(0);
        });
    }

    bool loaded = false;
    QTimer check; // coalesces the change signals below into one check per event-loop turn
    check.setSingleShot(true);
    QObject::connect(&check, &QTimer::timeout, &app, [&] {
        const bool paired = rpc->authState() == "paired"_L1;
        if (loaded || !rpc->isConnected() || rpc->authState() == "unknown"_L1
            || (paired && (chatList->property("loading").toBool() || chatList->property("count").toInt() == 0)))
            return;
        loaded = true;
        if (paired)
            PerfLog::print("WISP_READY", PerfLog::elapsed());
        if (paired && !openChat.isEmpty())
            QMetaObject::invokeMethod(conversation, "open", Q_ARG(QString, openChat));
        else if (!shotFile.isEmpty())
            shotTimer.start();
    });
    QObject::connect(rpc, &RpcClient::connectedChanged, &check, qOverload<>(&QTimer::start));
    QObject::connect(rpc, &RpcClient::authStateChanged, &check, qOverload<>(&QTimer::start));
    QObject::connect(chatList, SIGNAL(loadingChanged()), &check, SLOT(start()));
    QObject::connect(chatList, SIGNAL(countChanged()), &check, SLOT(start()));

    return app.exec();
}
