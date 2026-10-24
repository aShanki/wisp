// Setup shared by the tests that drive the real QML app offscreen against a
// private wispd --mock.
#pragma once

#include "fontsetup.h"

#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QFont>
#include <QGuiApplication>
#include <QMetaObject>
#include <QProcess>
#include <QQuickItem>
#include <QQuickStyle>
#include <QString>
#include <QStringList>

#include <csignal>

#ifdef Q_OS_LINUX
#include <sys/prctl.h>
#endif

namespace harness {

using namespace Qt::StringLiterals;

// Where private dirs go: $TMPDIR, else build/tmp in the source tree.
inline QString scratchRoot()
{
    QString root = qEnvironmentVariable("TMPDIR");
    if (root.isEmpty())
        root = QStringLiteral(WISP_SOURCE_DIR "/../build/tmp");
    root = QDir::cleanPath(QDir(root).absolutePath());
    QDir().mkpath(root);
    return root;
}

// The directory with wispd: $WISP_BIN, else the repo's bin/.
inline QString binDir()
{
    return QDir::cleanPath(QDir(qEnvironmentVariable("WISP_BIN", QStringLiteral(WISP_SOURCE_DIR "/../bin"))).absolutePath());
}

inline QString socketPath(const QString &dir) { return dir + "/wispd.sock"_L1; }

// Points HOME, the XDG dirs, TMPDIR and D-Bus into dir, and Qt at the
// offscreen software renderer, so nothing reaches the desktop or the user's
// own Wisp. Call before the QGuiApplication exists.
inline void isolate(const QString &dir)
{
    const QByteArray d = QFile::encodeName(dir);
    for (const char *var : {"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"})
        qputenv(var, d + '/' + var);
    const QString runtime = dir + "/XDG_RUNTIME_DIR"_L1;
    QDir().mkpath(runtime);
    QFile::setPermissions(runtime, QFile::ReadOwner | QFile::WriteOwner | QFile::ExeOwner);
    qputenv("XDG_RUNTIME_DIR", QFile::encodeName(runtime));
    qputenv("HOME", d);
    qputenv("TMPDIR", d);
    qputenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=" + d + "/no-bus");
    qputenv("WISP_UPDATES", "0");
    qputenv("QT_QPA_PLATFORM", "offscreen");
    qputenv("QT_QUICK_BACKEND", "software");
    qputenv("QT_ACCESSIBILITY", "0");
    const QString assets = QStringLiteral(WISP_SOURCE_DIR "/../build/whatsapp/");
    const QString data = dir + "/XDG_DATA_HOME/wisp/"_L1;
    QDir().mkpath(data);
    for (const auto *name : {"wallpaper-light.png", "wallpaper-dark.png"})
        if (QFileInfo::exists(assets + QLatin1StringView(name)))
            QFile::link(assets + QLatin1StringView(name), data + QLatin1StringView(name));
}

// Match the app's controls and font.
inline void useAppStyle()
{
    QGuiApplication::setFont(applicationFont(QStringLiteral(WISP_SOURCE_DIR "/../build/whatsapp")));
    QQuickStyle::setStyle(u"Basic"_s);
}

// Starts wispd --mock on dir's socket; it gets SIGTERM if the test dies.
inline void startMock(QProcess &daemon, const QString &dir, const QStringList &extra = {})
{
#ifdef Q_OS_LINUX
    daemon.setChildProcessModifier([] { ::prctl(PR_SET_PDEATHSIG, SIGTERM); });
#endif
    daemon.start(binDir() + "/wispd"_L1,
                 QStringList{u"--mock"_s, u"--socket"_s, socketPath(dir), u"--data"_s, dir + "/data"_L1} + extra);
}

inline QQuickItem *findView(QQuickItem *root, const QObject *model)
{
    for (QQuickItem *child : root->childItems()) {
        if (child->inherits("QQuickListView") && child->property("model").value<QObject *>() == model)
            return child;
        if (QQuickItem *found = findView(child, model))
            return found;
    }
    return nullptr;
}

inline QQuickItem *itemAt(QQuickItem *view, int row)
{
    QQuickItem *item = nullptr;
    QMetaObject::invokeMethod(view, "itemAtIndex", Q_RETURN_ARG(QQuickItem *, item), Q_ARG(int, row));
    return item;
}

inline QQuickItem *named(QQuickItem *root, const QString &name)
{
    for (QQuickItem *child : root->childItems()) {
        if (child->objectName() == name)
            return child;
        if (QQuickItem *found = named(child, name))
            return found;
    }
    return nullptr;
}

} // namespace harness
