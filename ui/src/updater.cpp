#include "updater.h"
#include "rpcclient.h"

#include <QCoreApplication>
#include <QCryptographicHash>
#include <QDateTime>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QNetworkReply>
#include <QProcess>
#include <QRegularExpression>
#include <QSettings>
#include <QTemporaryDir>

#include <memory>

using namespace Qt::StringLiterals;

namespace {
QList<quint64> semver(QString value)
{
    static const QRegularExpression re(u"^v?(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)$"_s);
    const auto match = re.match(value);
    if (!match.hasMatch()) return {};
    QList<quint64> parts;
    for (int i = 1; i <= 3; ++i) {
        bool ok;
        auto part = match.captured(i).toULongLong(&ok);
        if (!ok) return {};
        parts << part;
    }
    return parts;
}
QString platform()
{
#if defined(Q_OS_MACOS) && defined(Q_PROCESSOR_ARM_64)
    return u"macos-arm64.zip"_s;
#elif defined(Q_OS_LINUX) && defined(Q_PROCESSOR_X86_64)
    return u"linux-x86_64.tar.gz"_s;
#else
    return {};
#endif
}
bool run(const QString &program, const QStringList &args)
{
    QProcess p;
    p.start(program, args);
    return p.waitForFinished(120000) && p.exitStatus() == QProcess::NormalExit && p.exitCode() == 0;
}
QByteArray hashFile(const QString &path)
{
    QFile file(path);
    if (!file.open(QIODevice::ReadOnly)) return {};
    QCryptographicHash hash(QCryptographicHash::Sha256);
    if (!hash.addData(&file)) return {};
    return hash.result().toHex();
}
}

bool newerVersion(const QString &candidate, const QString &current)
{
    const auto a = semver(candidate), b = semver(current);
    return a.size() == 3 && b.size() == 3 && a > b;
}

UpdateRelease updateRelease(const QByteArray &json, const QString &current, const QString &platform)
{
    const auto obj = QJsonDocument::fromJson(json).object();
    const QString tag = obj.value("tag_name"_L1).toString();
    if (platform.isEmpty() || obj.value("draft"_L1).toBool() || obj.value("prerelease"_L1).toBool()
        || !newerVersion(tag, current)) return {};
    UpdateRelease result;
    result.version = tag.startsWith(u'v') ? tag.mid(1) : tag;
    const QString name = u"wisp-%1-%2"_s.arg(result.version, platform);
    for (const auto &entry : obj.value("assets"_L1).toArray()) {
        const auto asset = entry.toObject();
        const QUrl url(asset.value("browser_download_url"_L1).toString());
        if (url.scheme() != "https"_L1 || url.host() != "github.com"_L1
            || !url.path().startsWith(u"/aShanki/wisp/releases/download/"_s + tag + u'/')) continue;
        if (asset.value("name"_L1).toString() == name) result.archive = url;
        if (asset.value("name"_L1).toString() == "SHA256SUMS"_L1) result.checksums = url;
    }
    return result.archive.isEmpty() || result.checksums.isEmpty() ? UpdateRelease{} : result;
}

Updater::Updater(QObject *parent) : QObject(parent)
{
    m_timer.setInterval(6 * 60 * 60 * 1000);
    connect(&m_timer, &QTimer::timeout, this, &Updater::check);
    // Tests load the QML module without main(), so require an explicit opt-in there.
    if (qEnvironmentVariable("WISP_UPDATES") == "1"_L1 && !platform().isEmpty()) {
        m_timer.start();
        QTimer::singleShot(10000, this, &Updater::check);
    }
}
bool Updater::enabled() const { return QSettings().value(u"updates/enabled"_s, true).toBool(); }
void Updater::setEnabled(bool value)
{
    QSettings().setValue(u"updates/enabled"_s, value);
    if (!value) m_release = {};
    emit changed();
}
void Updater::later() { m_release = {}; m_error.clear(); emit changed(); }
void Updater::fail(const QString &message) { m_busy = false; m_error = message; emit changed(); }
void Updater::get(const QUrl &url, qsizetype limit, std::function<void(QByteArray)> done, const QString &destination)
{
    QNetworkRequest request(url);
    request.setRawHeader("User-Agent", "Wisp/" WISP_VERSION);
    request.setRawHeader("Accept", "application/vnd.github+json");
    request.setAttribute(QNetworkRequest::RedirectPolicyAttribute, QNetworkRequest::NoLessSafeRedirectPolicy);
    request.setTransferTimeout(60000);
    auto *reply = m_network.get(request);
    auto bytes = std::make_shared<QByteArray>();
    auto file = std::make_shared<QFile>(destination);
    if (!destination.isEmpty() && !file->open(QIODevice::WriteOnly)) {
        reply->abort();
        reply->deleteLater();
        done({});
        return;
    }
    auto received = std::make_shared<qint64>(0);
    auto read = [reply, bytes, file, received, limit, destination] {
        const auto chunk = reply->readAll();
        *received += chunk.size();
        if (*received > limit) { reply->abort(); return; }
        if (destination.isEmpty()) bytes->append(chunk);
        else if (file->write(chunk) != chunk.size()) reply->abort();
    };
    reply->setReadBufferSize(256 * 1024);
    connect(reply, &QNetworkReply::readyRead, this, read);
    connect(reply, &QNetworkReply::finished, this, [reply, bytes, file, read, done, destination] {
        read();
        const bool ok = reply->error() == QNetworkReply::NoError;
        if (file->isOpen()) file->close();
        if (reply->url().host() == "api.github.com"_L1) {
            const auto now = QDateTime::currentSecsSinceEpoch();
            const auto reset = reply->rawHeader("X-RateLimit-Reset").toLongLong();
            const auto retry = reply->rawHeader("Retry-After").toLongLong();
            QSettings settings;
            const auto next = settings.value(u"updates/nextCheck"_s).toLongLong();
            settings.setValue(u"updates/nextCheck"_s, qMax(next, qMax(reset, now + retry)));
        }
        reply->deleteLater();
        done(ok ? (destination.isEmpty() ? *bytes : QByteArray("ok")) : QByteArray{});
    });
}
void Updater::check()
{
    QSettings settings;
    const auto now = QDateTime::currentSecsSinceEpoch();
    if (!enabled() || m_busy || now < settings.value(u"updates/nextCheck"_s, 0).toLongLong()) return;
    settings.setValue(u"updates/nextCheck"_s, now + 6 * 60 * 60);
    get(QUrl(u"https://api.github.com/repos/aShanki/wisp/releases/latest"_s), 1024 * 1024,
        [this](const QByteArray &json) {
            if (!enabled()) return;
            m_release = updateRelease(json, version(), platform());
            emit changed();
        });
}
void Updater::install()
{
    if (m_busy || m_release.archive.isEmpty()) return;
    m_busy = true;
    m_error.clear();
    emit changed();
    const auto release = m_release;
    get(release.checksums, 65536, [this, release](const QByteArray &sums) {
        QByteArray expected;
        for (const auto &line : sums.split('\n')) {
            const auto fields = line.simplified().split(' ');
            if (fields.size() == 2 && fields[1] == release.archive.fileName().toUtf8()) {
                if (!expected.isEmpty()) { fail(tr("Duplicate update checksum")); return; }
                expected = fields[0];
            }
        }
        if (!QRegularExpression(u"^[0-9a-f]{64}$"_s).match(QString::fromLatin1(expected)).hasMatch()) {
            fail(tr("Update checksum is missing")); return;
        }
        const QString exe = QCoreApplication::applicationFilePath();
#ifdef Q_OS_MACOS
        const QString target = QDir::cleanPath(QFileInfo(exe).absolutePath() + u"/../.."_s);
        if (!target.endsWith(u".app"_s)) { fail(tr("Updates require an installed app bundle")); return; }
#else
        const QString target = QFileInfo(exe).absolutePath();
#endif
        auto stage = std::make_shared<QTemporaryDir>(QFileInfo(target).absolutePath() + u"/.wisp-update-XXXXXX"_s);
        if (!stage->isValid()) { fail(tr("The installation directory is not writable")); return; }
        const QString root = stage->path();
        const QString archive = root + u"/archive"_s;
        get(release.archive, 512 * 1024 * 1024, [this, expected, exe, target, stage, root, archive](const QByteArray &result) {
            if (result.isEmpty() || hashFile(archive) != expected) {
                fail(tr("Update download or checksum verification failed")); return;
            }
#ifdef Q_OS_MACOS
            bool extracted = run(u"/usr/bin/ditto"_s, {u"-x"_s, u"-k"_s, archive, root + u"/new"_s});
            const QString binaries = root + u"/new/Wisp.app/Contents/MacOS/"_s;
            extracted = extracted && run(u"/usr/bin/codesign"_s, {u"--verify"_s, u"--deep"_s, root + u"/new/Wisp.app"_s});
#else
            QDir().mkdir(root + u"/new"_s);
            // Release tarballs contain these three regular files only.
            QProcess list;
            list.start(u"tar"_s, {u"-tzf"_s, archive});
            bool extracted = list.waitForFinished() && list.exitCode() == 0;
            auto names = QString::fromUtf8(list.readAllStandardOutput()).split(u'\n', Qt::SkipEmptyParts);
            names.sort();
            extracted = extracted && names == QStringList{u"wisp"_s, u"wispctl"_s, u"wispd"_s};
            extracted = extracted && run(u"tar"_s, {u"-xzf"_s, archive, u"-C"_s, root + u"/new"_s});
            const QString binaries = root + u"/new/"_s;
#endif
            for (const auto &name : {u"wisp"_s, u"wispd"_s, u"wispctl"_s}) {
                QFileInfo file(binaries + name);
                extracted = extracted && file.isFile() && !file.isSymLink() && file.isExecutable();
            }
            if (!extracted) { fail(tr("Update archive is invalid")); return; }
            const bool daemonChanged = hashFile(binaries + u"wispd"_s) != hashFile(QFileInfo(exe).absolutePath() + u"/wispd"_s);
            QFile script(root + u"/install.sh"_s);
            if (!script.open(QIODevice::WriteOnly)) { fail(tr("Could not stage installer")); return; }
            QFile installer(u":/install-update.sh"_s);
            if (!installer.open(QIODevice::ReadOnly)) {
                fail(tr("Could not read installer")); return;
            }
            const auto installerData = installer.readAll();
            if (script.write(installerData) != installerData.size() || !script.flush()) {
                fail(tr("Could not write installer")); return;
            }
            script.close();
            const auto daemonPid = RpcClient::instance()->daemonPid();
            auto launch = [this, stage, target, exe, daemonChanged, root, daemonPid] {
                if (!QProcess::startDetached(u"/bin/sh"_s, {root + u"/install.sh"_s, root, target, exe,
                    QString::number(QCoreApplication::applicationPid()), daemonChanged ? u"1"_s : u"0"_s, QString::number(daemonPid)})) {
                    fail(tr("Could not start installer")); return;
                }
                stage->setAutoRemove(false);
                QCoreApplication::quit();
            };
            if (daemonChanged && daemonPid <= 0) {
                fail(tr("Restart Wisp before installing this update")); return;
            }
            if (daemonChanged) {
                RpcClient::instance()->call(u"daemon.shutdown"_s, {}, [this, launch](const QJsonValue &, const QJsonObject &error) {
                    if (!error.isEmpty()) { fail(tr("Could not stop daemon for update")); return; }
                    launch();
                });
            } else launch();
        }, archive);
    });
}
