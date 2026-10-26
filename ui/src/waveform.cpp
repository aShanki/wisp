#include "waveform.h"

#include <QPainter>

#include <cmath>

namespace {

// WhatsApp's resampling: linear interpolation up, averaging runs down.
QList<qreal> resample(const QByteArray &levels, int n)
{
    QList<qreal> in;
    for (char c : levels)
        in.append(qBound(0, int(uchar(c)), 100) / 100.0);
    if (n == in.size())
        return in;
    if (in.isEmpty())
        return QList<qreal>(n, 0);
    QList<qreal> out;
    if (n > in.size()) {
        const qreal step = in.size() == 1 ? 0 : 1 / (1 + qreal(n - in.size()) / (in.size() - 1));
        out.append(in.first());
        for (int i = 1; i < n - 1; ++i) {
            const qreal at = i * step;
            const int lo = int(std::floor(at));
            out.append(in[lo] + (in[int(std::ceil(at))] - in[lo]) * (at - lo));
        }
        if (n >= 2)
            out.append(in.last());
        return out;
    }
    const qreal ratio = qreal(n) / in.size();
    qreal sum = 0, carry = 0;
    int count = 0;
    for (qreal v : in) {
        sum += v;
        ++count;
        carry += ratio;
        if (carry >= 1) {
            out.append(sum / count);
            sum = 0;
            count = 0;
            carry -= 1;
        }
    }
    if (count > 0)
        out.append(sum / count);
    return out;
}

} // namespace

Waveform::Waveform(QQuickItem *parent)
    : QQuickPaintedItem(parent)
{
    setAntialiasing(true);
    connect(this, &Waveform::colorsChanged, this, [this] { update(); });
    connect(this, &Waveform::progressChanged, this, [this] { update(); });
}

void Waveform::setLevels(const QByteArray &levels)
{
    if (levels == m_levels)
        return;
    m_levels = levels;
    emit levelsChanged();
    update();
}

void Waveform::paint(QPainter *painter)
{
    constexpr qreal bar = 2.5, pitch = bar + 1.8, thumb = 6;
    const qreal w = width(), h = height();
    const int n = int(std::floor((w - 2 * thumb) / pitch));
    if (n <= 0)
        return;
    const qreal inset = (w - pitch * (n - 1)) / 2;
    const QList<qreal> levels = resample(m_levels, n);
    painter->setRenderHint(QPainter::Antialiasing);
    painter->setBrush(m_barColor);
    const QPen pen(m_barColor, bar, Qt::SolidLine, Qt::RoundCap);
    for (int i = 0; i < levels.size(); ++i) {
        const qreal x = inset + i * pitch, len = bar + levels[i] * (h - bar);
        if (len - bar < 0.01) { // silence is a dot
            painter->setPen(Qt::NoPen);
            painter->drawEllipse(QPointF(x, h / 2), bar / 2, bar / 2);
        } else {
            painter->setPen(pen);
            painter->drawLine(QPointF(x, (h - len + bar) / 2), QPointF(x, (h + len - bar) / 2));
        }
    }
    painter->setPen(Qt::NoPen);
    painter->setBrush(m_thumbColor);
    painter->drawEllipse(QPointF(inset + qBound(0.0, m_progress, 1.0) * pitch * (n - 1), h / 2), thumb, thumb);
}
