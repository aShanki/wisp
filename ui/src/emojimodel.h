// Emoji shortcode search for the composer's :name: suggestions (QML singleton
// `EmojiSearch`). The rows are the matches of the last query(). The gemoji
// table (assets/emoji/emoji.tsv) is read on the first query, not at startup.
#pragma once

#include <QAbstractListModel>
#include <QByteArray>
#include <QList>
#include <QString>
#include <QtQml/qqmlregistration.h>

class EmojiModel : public QAbstractListModel
{
    Q_OBJECT
    QML_NAMED_ELEMENT(EmojiSearch)
    QML_SINGLETON

    Q_PROPERTY(int count READ count NOTIFY countChanged)

public:
    enum Roles {
        EmojiRole = Qt::UserRole + 1,
        NameRole, // the matched alias, or the primary one for a tag match
    };
    static constexpr int kMaxRows = 8;

    using QAbstractListModel::QAbstractListModel;

    int rowCount(const QModelIndex &parent = {}) const override;
    QVariant data(const QModelIndex &index, int role) const override;
    QHash<int, QByteArray> roleNames() const override;
    int count() const { return int(m_rows.size()); }

    // Exact alias first, then alias prefix, then word prefix in an alias or
    // tag ("cross" finds skull_and_crossbones), then substring; ties keep
    // gemoji's order.
    Q_INVOKABLE void query(const QString &text);
    // The emoji with exactly this alias, or "".
    Q_INVOKABLE QString lookup(const QString &alias);

signals:
    void countChanged();

private:
    struct Entry {
        int emoji, aliases, tags, end; // offsets into m_data
    };
    struct Row {
        QString emoji, name;
        bool operator==(const Row &) const = default;
    };

    void load();
    void setRows(const QList<Row> &rows);

    bool m_loaded = false;
    QByteArray m_data;
    QList<Entry> m_entries;
    QList<Row> m_rows;
};
