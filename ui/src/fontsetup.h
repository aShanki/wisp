#pragma once

#include <QDir>
#include <QFont>
#include <QFontDatabase>
#include <QStandardPaths>

inline QFont applicationFont(const QString &directory = {})
{
    QString family = QStringLiteral("Arial");
    for (const auto &name : {QStringLiteral("Roboto.woff2"), QStringLiteral("Roboto-Italic.woff2")}) {
        const QString path = directory.isEmpty()
            ? QStandardPaths::locate(QStandardPaths::GenericDataLocation, QStringLiteral("wisp/") + name)
            : QDir(directory).filePath(name);
        if (path.isEmpty())
            continue;
        const int id = QFontDatabase::addApplicationFont(path);
        const auto families = QFontDatabase::applicationFontFamilies(id);
        if (!families.isEmpty())
            family = families.first();
    }
    // Roboto is a variable font: QML sets weights on its wght axis, which
    // FreeType would otherwise embolden a second time.
    if (family != QLatin1String("Arial"))
        qputenv("QT_NO_SYNTHESIZED_BOLD", "1");
    return QFont(family);
}
