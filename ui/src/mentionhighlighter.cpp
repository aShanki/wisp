#include "mentionhighlighter.h"

#include "format.h"

void MentionHighlighter::setTextDocument(QQuickTextDocument *doc)
{
    if (doc == m_textDocument)
        return;
    m_textDocument = doc;
    setDocument(doc ? doc->textDocument() : nullptr);
    emit textDocumentChanged();
}

void MentionHighlighter::setNames(const QStringList &names)
{
    if (names == m_names)
        return;
    m_names = names;
    rehighlight();
    emit namesChanged();
}

void MentionHighlighter::setColor(const QColor &color)
{
    if (color == m_color)
        return;
    m_color = color;
    rehighlight();
    emit colorChanged();
}

void MentionHighlighter::highlightBlock(const QString &text)
{
    for (const auto &[at, length] : Format::nameTokens(text, m_names))
        setFormat(int(at), int(length), m_color);
}
