// image://wisp/avatar/<jid>, image://wisp/thumb/<chat>/<id>, image://wisp/media/<chat>/<id>,
// image://wisp/sticker/<id>.
// wispd resolves the file over RPC; it is decoded at the requested size on a
// small thread pool. A missing image is a transparent 1×1.
#pragma once

#include <QQuickAsyncImageProvider>

class ImageProvider : public QQuickAsyncImageProvider
{
public:
    QQuickImageResponse *requestImageResponse(const QString &id, const QSize &requestedSize) override;
};
