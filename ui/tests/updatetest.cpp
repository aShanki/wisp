#include "updater.h"
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QtTest>

class UpdateTest : public QObject
{
    Q_OBJECT
private slots:
    void versions()
    {
        QVERIFY(newerVersion("v1.10.0", "1.9.9"));
        QVERIFY(newerVersion("2.0.0", "1.99.99"));
        for (const auto &tag : {"1.2.3", "1.2.2", "v1.2.3-beta", "01.3.0", "1.3", "garbage", "18446744073709551616.0.0"})
            QVERIFY2(!newerVersion(tag, "1.2.3"), tag);
        QVERIFY(!newerVersion("1.0.0", "dev"));
    }
    void releases()
    {
        auto asset = [](QString name) {
            return QJsonObject{{"name", name}, {"browser_download_url", "https://github.com/aShanki/wisp/releases/download/v1.2.3/" + name}};
        };
        QJsonObject release{{"tag_name", "v1.2.3"}, {"assets", QJsonArray{
            asset("wisp-1.2.3-macos-arm64.zip"), asset("wisp-1.2.3-linux-x86_64.tar.gz"), asset("SHA256SUMS")}}};
        auto parse = [&](const QString &platform) { return updateRelease(QJsonDocument(release).toJson(), "1.0.0", platform); };
        QCOMPARE(parse("macos-arm64.zip").archive.fileName(), "wisp-1.2.3-macos-arm64.zip");
        QCOMPARE(parse("linux-x86_64.tar.gz").version, "1.2.3");
        QVERIFY(parse("unsupported").version.isEmpty());
        release["prerelease"] = true;
        QVERIFY(parse("macos-arm64.zip").version.isEmpty());
        release["prerelease"] = false;
        release["draft"] = true;
        QVERIFY(parse("macos-arm64.zip").version.isEmpty());
        release["draft"] = false;
        release["assets"] = QJsonArray{asset("wisp-1.2.3-macos-arm64.zip")};
        QVERIFY(parse("macos-arm64.zip").version.isEmpty());
        auto bad = asset("SHA256SUMS");
        bad["browser_download_url"] = "https://example.com/SHA256SUMS";
        release["assets"] = QJsonArray{asset("wisp-1.2.3-macos-arm64.zip"), bad};
        QVERIFY(parse("macos-arm64.zip").version.isEmpty());
        QVERIFY(updateRelease("broken json", "1.0.0", "macos-arm64.zip").version.isEmpty());
    }
};
QTEST_GUILESS_MAIN(UpdateTest)
#include "updatetest.moc"
