// VideoSurface: paints a media player's frames, letterboxed. Wisp renders
// the scene in software, where VideoOutput draws nothing, so frames come
// through a QVideoSink as images instead.
#pragma once

#include <QImage>
#include <QQuickPaintedItem>
#include <QtQml/qqmlregistration.h>

#include <QVideoSink>

class VideoSurface : public QQuickPaintedItem
{
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QVideoSink *videoSink READ videoSink CONSTANT)
    Q_PROPERTY(QRectF contentRect READ contentRect NOTIFY contentRectChanged)

public:
    explicit VideoSurface(QQuickItem *parent = nullptr);

    QVideoSink *videoSink() const { return m_sink; }
    // Where the frame is drawn, in item coordinates.
    QRectF contentRect() const;
    void paint(QPainter *painter) override;

signals:
    void contentRectChanged();

private:
    QVideoSink *m_sink;
    QImage m_frame;
};
