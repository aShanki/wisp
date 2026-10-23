// Format: text formatting for QML (singleton `Format`) and the C++ models.
#pragma once

#include <QList>
#include <QObject>
#include <QString>
#include <QStringList>
#include <QtQml/qqmlregistration.h>

#include <utility>

class Format : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    QML_SINGLETON

public:
    using QObject::QObject;

    // Someone @mentioned in a message; its text has "@" and jid's user part.
    struct Mention {
        QString jid, name;
        bool operator==(const Mention &) const = default;
    };

    static QString time(qint64 ts);                     // "14:05"
    static QString day(qint64 ts);                      // "Today", "Yesterday", "Monday", "20 October 2026"
    Q_INVOKABLE static QString fileSize(qint64 bytes);  // "512 bytes", "180 kB", "1.2 MB"
    Q_INVOKABLE static QString duration(qint64 s);      // "1:05", "1:02:03"
    // StyledText: HTML escaped, *bold* _italic_ ~strike~ ```mono```, links, <br>.
    // A mention's "@<user>" becomes "@Name", linked to "mention:<jid>" with class "m".
    static QString richText(const QString &text, const QList<Mention> &mentions = {});
    // The text with each mention's "@<user>" as "@Name".
    static QString mentionNames(const QString &text, const QList<Mention> &mentions);
    // The "@Name" tokens of names in typed text, as (start, length), left to
    // right; where names overlap, the longest.
    static QList<std::pair<qsizetype, qsizetype>> nameTokens(QStringView text, const QStringList &names);
    static QString user(const QString &jid); // "61400000001" of "61400000001:3@s.whatsapp.net"
    static QString fold(const QString &s);   // case and accents folded, for matching names
};
