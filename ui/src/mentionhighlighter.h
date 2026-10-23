// Colours the "@Name" mentions chosen in the composer, as WhatsApp does
// (MentionHighlighter { textDocument: input.textDocument }).
#pragma once

#include <QColor>
#include <QPointer>
#include <QQuickTextDocument>
#include <QStringList>
#include <QSyntaxHighlighter>
#include <QtQml/qqmlregistration.h>

class MentionHighlighter : public QSyntaxHighlighter
{
    Q_OBJECT
    QML_ELEMENT

    Q_PROPERTY(QQuickTextDocument *textDocument READ textDocument WRITE setTextDocument NOTIFY textDocumentChanged)
    Q_PROPERTY(QStringList names READ names WRITE setNames NOTIFY namesChanged)
    Q_PROPERTY(QColor color READ color WRITE setColor NOTIFY colorChanged)

public:
    explicit MentionHighlighter(QObject *parent = nullptr) : QSyntaxHighlighter(parent) {}

    QQuickTextDocument *textDocument() const { return m_textDocument; }
    void setTextDocument(QQuickTextDocument *doc);
    QStringList names() const { return m_names; }
    void setNames(const QStringList &names);
    QColor color() const { return m_color; }
    void setColor(const QColor &color);

signals:
    void textDocumentChanged();
    void namesChanged();
    void colorChanged();

protected:
    void highlightBlock(const QString &text) override;

private:
    QPointer<QQuickTextDocument> m_textDocument;
    QStringList m_names;
    QColor m_color;
};
