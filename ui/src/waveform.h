// Waveform: a voice note's levels (bytes 0-100) drawn as WhatsApp Web draws
// them: round 2.5 px bars every 4.3 px, at least 2.5 px tall, resampled to
// fill the width, and the 6 px playback thumb at the start.
#pragma once

#include <QColor>
#include <QQuickPaintedItem>
#include <QtQml/qqmlregistration.h>

class Waveform : public QQuickPaintedItem
{
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QByteArray levels READ levels WRITE setLevels NOTIFY levelsChanged)
    Q_PROPERTY(QColor barColor MEMBER m_barColor NOTIFY colorsChanged)
    Q_PROPERTY(QColor thumbColor MEMBER m_thumbColor NOTIFY colorsChanged)

public:
    explicit Waveform(QQuickItem *parent = nullptr);

    QByteArray levels() const { return m_levels; }
    void setLevels(const QByteArray &levels);
    void paint(QPainter *painter) override;

signals:
    void levelsChanged();
    void colorsChanged();

private:
    QByteArray m_levels;
    QColor m_barColor, m_thumbColor;
};
