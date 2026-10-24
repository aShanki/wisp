#pragma once

#include <QObject>
#include <QNetworkAccessManager>
#include <QTimer>
#include <QUrl>
#include <QtQml/qqmlregistration.h>
#include <functional>

struct UpdateRelease {
    QString version;
    QUrl archive;
    QUrl checksums;
};

// Only stable semver releases with both assets can be installed.
UpdateRelease updateRelease(const QByteArray &json, const QString &current, const QString &platform);
bool newerVersion(const QString &candidate, const QString &current);

class Updater : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    QML_SINGLETON
    Q_PROPERTY(QString version READ version CONSTANT)
    Q_PROPERTY(bool enabled READ enabled WRITE setEnabled NOTIFY changed)
    Q_PROPERTY(QString available READ available NOTIFY changed)
    Q_PROPERTY(bool busy READ busy NOTIFY changed)
    Q_PROPERTY(QString error READ error NOTIFY changed)
public:
    explicit Updater(QObject *parent = nullptr);
    QString version() const { return QStringLiteral(WISP_VERSION); }
    bool enabled() const;
    void setEnabled(bool value);
    QString available() const { return m_release.version; }
    bool busy() const { return m_busy; }
    QString error() const { return m_error; }
    Q_INVOKABLE void install();
    Q_INVOKABLE void later();
signals:
    void changed();
private:
    void check();
    void get(const QUrl &url, qsizetype limit, std::function<void(QByteArray)> done, const QString &destination = {});
    void fail(const QString &message);
    QNetworkAccessManager m_network;
    QTimer m_timer;
    UpdateRelease m_release;
    bool m_busy = false;
    QString m_error;
};
