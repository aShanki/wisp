#include "attachmentmodel.h"

#include <QFile>
#include <QFileInfo>
#include <QImageReader>

#include <utility>

using namespace Qt::StringLiterals;

AttachmentModel::~AttachmentModel()
{
    clear();
}

int AttachmentModel::rowCount(const QModelIndex &parent) const
{
    return parent.isValid() ? 0 : count();
}

QVariant AttachmentModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() >= count())
        return {};
    const File &file = m_files.at(index.row());
    switch (role) {
    case SourceRole: return QUrl::fromLocalFile(file.path);
    case FileNameRole: return QFileInfo(file.path).fileName();
    case ImageRole: return file.image;
    case CaptionRole: return file.caption;
    case ImageWidthRole: return file.size.width();
    case ImageHeightRole: return file.size.height();
    }
    return {};
}

QHash<int, QByteArray> AttachmentModel::roleNames() const
{
    return {{SourceRole, "source"}, {FileNameRole, "fileName"}, {ImageRole, "isImage"}, {CaptionRole, "caption"},
            {ImageWidthRole, "imageWidth"}, {ImageHeightRole, "imageHeight"}};
}

QVariantMap AttachmentModel::get(int row) const
{
    QVariantMap result;
    const auto roles = roleNames();
    for (auto it = roles.cbegin(); it != roles.cend(); ++it)
        result.insert(QString::fromLatin1(it.value()), data(index(row), it.key()));
    return result;
}

void AttachmentModel::setCaption(int row, const QString &text)
{
    if (row < 0 || row >= count() || m_files[row].caption == text)
        return;
    m_files[row].caption = text;
    emit dataChanged(index(row), index(row), {CaptionRole});
}

void AttachmentModel::add(const QString &path, const QString &caption, bool temporary)
{
    const QFileInfo info(path);
    QImageReader reader(path);
    const bool image = reader.canRead();
    QSize size = image ? reader.size() : QSize();
    if (reader.transformation() & QImageIOHandler::TransformationRotate90)
        size.transpose();
    beginInsertRows({}, count(), count());
    m_files.append({info.absoluteFilePath(), caption, image, temporary, size});
    endInsertRows();
    emit countChanged();
}

void AttachmentModel::remove(int row)
{
    if (row < 0 || row >= count())
        return;
    beginRemoveRows({}, row, row);
    const File file = m_files.takeAt(row);
    endRemoveRows();
    if (file.temporary)
        QFile::remove(file.path);
    emit countChanged();
}

QList<AttachmentModel::File> AttachmentModel::take()
{
    if (m_files.isEmpty())
        return {};
    beginRemoveRows({}, 0, count() - 1);
    QList<File> files = std::exchange(m_files, {});
    endRemoveRows();
    emit countChanged();
    return files;
}

void AttachmentModel::clear()
{
    for (const File &file : take())
        if (file.temporary)
            QFile::remove(file.path);
}
