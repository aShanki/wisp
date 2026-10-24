#include "imageprovider.h"

#include "rpcclient.h"

#include <QImageReader>
#include <QPainter>
#include <QThreadPool>

#include <atomic>

using namespace Qt::StringLiterals;

namespace {

QThreadPool *decodePool()
{
    static QThreadPool *pool = [] {
        auto *p = new QThreadPool(QCoreApplication::instance());
        p->setMaxThreadCount(2);
        return p;
    }();
    return pool;
}

// Scaled to cover `want` (either side may be 0), never enlarged.
QImage decode(const QString &path, QSize want)
{
    QImageReader reader(path);
    reader.setAutoTransform(true);
    if (reader.transformation() & QImageIOHandler::TransformationRotate90)
        want.transpose();
    const QSize src = reader.size();
    if (src.isValid()) {
        const qreal sx = want.width() > 0 ? qreal(want.width()) / src.width() : 0;
        const qreal sy = want.height() > 0 ? qreal(want.height()) / src.height() : 0;
        const qreal s = qMax(sx, sy);
        if (s > 0 && s < 1)
            reader.setScaledSize((QSizeF(src) * s).toSize().expandedTo({1, 1}));
    }
    return reader.read();
}

// Avatars arrive as circles, so they sit on any background (the wallpaper too)
// without a mask.
QImage circle(const QImage &src)
{
    if (src.isNull())
        return src;
    const int d = qMin(src.width(), src.height());
    QImage out(d, d, QImage::Format_ARGB32_Premultiplied);
    out.fill(Qt::transparent);
    QPainter p(&out);
    p.setRenderHint(QPainter::Antialiasing);
    p.setPen(Qt::NoPen);
    p.setBrush(src);
    p.setBrushOrigin(-(src.width() - d) / 2, -(src.height() - d) / 2);
    p.drawEllipse(0, 0, d, d);
    return out;
}

// A missing avatar is a transparent 1x1. Missing media is an error, which the
// pixmap cache doesn't keep, so the next load asks again.
class Response : public QQuickImageResponse
{
public:
    explicit Response(bool media) : m_media(media) {}

    QQuickTextureFactory *textureFactory() const override
    {
        return QQuickTextureFactory::textureFactoryForImage(m_image);
    }
    QString errorString() const override { return m_error; }
    void cancel() override { m_cancelled = true; }

    // Any thread; exactly once.
    void finish(QImage image)
    {
        if (image.isNull() && m_media) {
            m_error = u"not available"_s;
        } else if (image.isNull()) {
            image = QImage(1, 1, QImage::Format_ARGB32_Premultiplied);
            image.fill(Qt::transparent);
        }
        m_image = std::move(image);
        // Emitted from the response's own thread: the engine deletes the
        // response once it hears, which must not race the emission.
        QMetaObject::invokeMethod(this, &Response::finished, Qt::QueuedConnection);
    }

    std::atomic_bool m_cancelled = false;

private:
    const bool m_media;
    QImage m_image;
    QString m_error;
};

} // namespace

QQuickImageResponse *ImageProvider::requestImageResponse(const QString &id, const QSize &requestedSize)
{
    const QStringList parts = id.split(u'/');
    const bool sticker = parts.size() == 2 && parts[0] == "sticker"_L1;
    const bool media = sticker || parts.size() == 3 && (parts[0] == "thumb"_L1 || parts[0] == "media"_L1);
    auto *response = new Response(media);
    QString method;
    QJsonObject params;
    if (parts.size() == 2 && parts[0] == "avatar"_L1) {
        method = u"avatars.get"_s;
        params = {{u"jid"_s, parts[1]}};
    } else if (sticker) {
        method = u"stickers.fetch"_s;
        params = {{u"id"_s, parts[1]}};
    } else if (media) {
        method = parts[0] == "thumb"_L1 ? u"media.thumb"_s : u"media.fetch"_s;
        params = {{u"chat"_s, parts[1]}, {u"id"_s, parts[2]}};
    } else {
        response->finish({});
        return response;
    }

    // Called on the image loader thread; RPC lives on the GUI thread.
    QMetaObject::invokeMethod(RpcClient::instance(), [response, media, method, params, requestedSize] {
        if (response->m_cancelled) {
            response->finish({});
            return;
        }
        RpcClient::instance()->call(method, params, [=](const QJsonValue &result, const QJsonObject &error) {
            const QString path = result.toObject().value("path"_L1).toString();
            if (!error.isEmpty() || path.isEmpty() || response->m_cancelled) {
                response->finish({});
                return;
            }
            decodePool()->start([=] {
                if (response->m_cancelled)
                    response->finish({});
                else
                    response->finish(media ? decode(path, requestedSize) : circle(decode(path, requestedSize)));
            });
        });
    }, Qt::QueuedConnection);
    return response;
}
