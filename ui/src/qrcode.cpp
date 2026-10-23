#include "qrcode.h"

#include "3rdparty/qrcodegen.hpp"

#include <QPainter>

QrCode::QrCode(QQuickItem *parent)
    : QQuickPaintedItem(parent)
{
    setImplicitSize(240, 240);
}

void QrCode::setText(const QString &text)
{
    if (text == m_text)
        return;
    m_text = text;
    m_modules = QImage();
    if (!text.isEmpty()) {
        try {
            const auto qr = qrcodegen::QrCode::encodeText(text.toUtf8().constData(), qrcodegen::QrCode::Ecc::LOW);
            constexpr int quiet = 4;
            const int n = qr.getSize();
            m_modules = QImage(n + 2 * quiet, n + 2 * quiet, QImage::Format_Grayscale8);
            m_modules.fill(255);
            for (int y = 0; y < n; ++y)
                for (int x = 0; x < n; ++x)
                    if (qr.getModule(x, y))
                        m_modules.scanLine(y + quiet)[x + quiet] = 0;
        } catch (const qrcodegen::data_too_long &) {
            qWarning("QrCode: text too long");
        }
    }
    emit textChanged();
    update();
}

void QrCode::paint(QPainter *painter)
{
    if (m_modules.isNull())
        return;
    const int fit = int(qMin(width(), height()));
    const int side = fit >= m_modules.width() ? fit - fit % m_modules.width() : fit;
    painter->setRenderHint(QPainter::SmoothPixmapTransform, false);
    painter->drawImage(QRect(int(width() - side) / 2, int(height() - side) / 2, side, side), m_modules);
}
