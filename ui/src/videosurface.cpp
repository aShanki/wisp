#include "videosurface.h"

#include <QPainter>
#include <QVideoFrame>

VideoSurface::VideoSurface(QQuickItem *parent)
    : QQuickPaintedItem(parent), m_sink(new QVideoSink(this))
{
    connect(m_sink, &QVideoSink::videoFrameChanged, this, [this](const QVideoFrame &frame) {
        const QSize old = m_frame.size();
        m_frame = frame.isValid() ? frame.toImage() : QImage();
        if (m_frame.size() != old)
            emit contentRectChanged();
        update();
    });
    connect(this, &QQuickItem::widthChanged, this, &VideoSurface::contentRectChanged);
    connect(this, &QQuickItem::heightChanged, this, &VideoSurface::contentRectChanged);
}

QRectF VideoSurface::contentRect() const
{
    if (m_frame.isNull())
        return {};
    const QSizeF size = QSizeF(m_frame.size()).scaled(width(), height(), Qt::KeepAspectRatio);
    return {(width() - size.width()) / 2, (height() - size.height()) / 2, size.width(), size.height()};
}

void VideoSurface::paint(QPainter *painter)
{
    if (m_frame.isNull())
        return;
    painter->setRenderHint(QPainter::SmoothPixmapTransform);
    painter->drawImage(contentRect(), m_frame);
}
