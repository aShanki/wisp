#pragma once

#include <QAbstractListModel>
#include <QSize>
#include <QUrl>
#include <QtQml/qqmlregistration.h>

class AttachmentModel : public QAbstractListModel
{
    Q_OBJECT
    QML_ANONYMOUS
    Q_PROPERTY(int count READ count NOTIFY countChanged)

public:
    enum Roles { SourceRole = Qt::UserRole + 1, FileNameRole, ImageRole, CaptionRole, ImageWidthRole, ImageHeightRole };
    struct File { QString path, caption; bool image = false, temporary = false; QSize size; };

    explicit AttachmentModel(QObject *parent = nullptr) : QAbstractListModel(parent) {}
    ~AttachmentModel() override;
    int count() const { return int(m_files.size()); }
    int rowCount(const QModelIndex &parent = {}) const override;
    QVariant data(const QModelIndex &index, int role) const override;
    QHash<int, QByteArray> roleNames() const override;
    Q_INVOKABLE QVariantMap get(int row) const;
    Q_INVOKABLE void setCaption(int row, const QString &text);
    Q_INVOKABLE void remove(int row);
    Q_INVOKABLE void clear();
    void add(const QString &path, const QString &caption, bool temporary = false);
    QList<File> take();

signals:
    void countChanged();

private:
    QList<File> m_files;
};
