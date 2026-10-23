// QrCode: draws `text` as a QR code, black on white with a quiet zone, at the
// largest whole-pixel module size that fits.
#pragma once

#include <QImage>
#include <QQuickPaintedItem>
#include <QtQml/qqmlregistration.h>

class QrCode : public QQuickPaintedItem
{
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QString text READ text WRITE setText NOTIFY textChanged)

public:
    explicit QrCode(QQuickItem *parent = nullptr);

    QString text() const { return m_text; }
    void setText(const QString &text);
    void paint(QPainter *painter) override;

signals:
    void textChanged();

private:
    QString m_text;
    QImage m_modules; // one pixel per module, quiet zone included
};
