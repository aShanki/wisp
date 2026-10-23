#include "format.h"

#include <QDateTime>
#include <QLocale>
#include <QRegularExpression>

using namespace Qt::StringLiterals;

QString Format::time(qint64 ts)
{
    if (ts <= 0)
        return {};
    const QLocale locale;
    const QString format = locale.timeFormat(QLocale::ShortFormat).contains("AP"_L1, Qt::CaseInsensitive)
        ? u"h:mm AP"_s : u"HH:mm"_s;
    return locale.toString(QDateTime::fromSecsSinceEpoch(ts).time(), format);
}

QString Format::day(qint64 ts)
{
    if (ts <= 0)
        return {};
    const QDate d = QDateTime::fromSecsSinceEpoch(ts).date();
    const qint64 ago = d.daysTo(QDate::currentDate());
    if (ago == 0)
        return tr("Today");
    if (ago == 1)
        return tr("Yesterday");
    if (ago > 1 && ago < 7)
        return QLocale().dayName(d.dayOfWeek());
    return QLocale().toString(d, u"d MMMM yyyy");
}

QString Format::fileSize(qint64 bytes)
{
    return QLocale().formattedDataSize(bytes, bytes < 1024 * 1024 ? 0 : 1, QLocale::DataSizeTraditionalFormat);
}

QString Format::duration(qint64 s)
{
    s = qMax<qint64>(s, 0);
    const auto two = [](qint64 n) { return u"%1"_s.arg(n, 2, 10, u'0'); };
    if (s >= 3600)
        return u"%1:%2:%3"_s.arg(s / 3600).arg(two(s / 60 % 60), two(s % 60));
    return u"%1:%2"_s.arg(s / 60).arg(two(s % 60));
}

namespace {

void appendEscaped(QString &out, QStringView text, bool pre = false)
{
    for (QChar c : text) {
        switch (c.unicode()) {
        case '&': out += "&amp;"_L1; break;
        case '<': out += "&lt;"_L1; break;
        case '>': out += "&gt;"_L1; break;
        case '"': out += "&quot;"_L1; break;
        case '\n': out += pre ? "\n"_L1 : "<br>"_L1; break;
        default: out += c;
        }
    }
}

// A link, a ```code block``` (eating one newline on each side, as <pre> breaks
// lines itself), *bold* _italic_ ~strike~ on one line, delimited by
// non-alphanumerics and hugging non-space text, or a mention's "@<user>".
const QRegularExpression tokens(
    uR"re((?<url>\b(?:https?://|www\.)[^\s<>"]*[^\s<>".,;:!?'")\]*_~])|\n?```(?<pre>[\s\S]+?)```\n?|(?<![\p{L}\p{N}])(?<mark>[*_~])(?<inner>(?=\S)[^\n]*?\S)\k<mark>(?![\p{L}\p{N}])|@(?<user>[0-9]+))re"_s);

const QRegularExpression userTokens(u"@([0-9]+)"_s);

const Format::Mention *mentionOf(QStringView user, const QList<Format::Mention> &mentions)
{
    for (const Format::Mention &m : mentions)
        if (!m.name.isEmpty() && Format::user(m.jid) == user)
            return &m;
    return nullptr;
}

void appendRich(QString &out, QStringView text, const QList<Format::Mention> &mentions)
{
    qsizetype done = 0;
    for (auto it = tokens.globalMatchView(text); it.hasNext();) {
        const QRegularExpressionMatch m = it.next();
        appendEscaped(out, text.sliced(done, m.capturedStart() - done));
        done = m.capturedEnd();
        if (const QStringView url = m.capturedView(u"url"); !url.isEmpty()) {
            out += "<a href=\""_L1;
            if (url.startsWith("www."_L1, Qt::CaseInsensitive))
                out += "https://"_L1;
            out += url; // already free of " < >; StyledText takes href verbatim
            out += "\">"_L1;
            appendEscaped(out, url);
            out += "</a>"_L1;
        } else if (const QStringView pre = m.capturedView(u"pre"); !pre.isEmpty()) {
            out += "<pre>"_L1;
            appendEscaped(out, pre, true);
            out += "</pre>"_L1;
        } else if (const QStringView user = m.capturedView(u"user"); !user.isEmpty()) {
            if (const Format::Mention *mention = mentionOf(user, mentions)) {
                out += "<a class=\"m\" href=\"mention:"_L1;
                out += mention->jid; // a JID has no " < > &
                out += "\">@"_L1;
                appendEscaped(out, mention->name);
                out += "</a>"_L1;
            } else {
                appendEscaped(out, m.capturedView());
            }
        } else {
            const QStringView mark = m.capturedView(u"mark");
            const QLatin1StringView tag = mark == u"*" ? "b"_L1 : mark == u"_" ? "i"_L1 : "s"_L1;
            out += u'<';
            out += tag;
            out += u'>';
            appendRich(out, m.capturedView(u"inner"), mentions);
            out += "</"_L1;
            out += tag;
            out += u'>';
        }
    }
    appendEscaped(out, text.sliced(done));
}

} // namespace

QString Format::richText(const QString &text, const QList<Mention> &mentions)
{
    QString out;
    out.reserve(text.size() + 16);
    appendRich(out, text, mentions);
    return out;
}

QString Format::mentionNames(const QString &text, const QList<Mention> &mentions)
{
    if (mentions.isEmpty())
        return text;
    QString out;
    qsizetype done = 0;
    for (auto it = userTokens.globalMatch(text); it.hasNext();) {
        const QRegularExpressionMatch m = it.next();
        if (const Mention *mention = mentionOf(m.capturedView(1), mentions)) {
            out += QStringView(text).sliced(done, m.capturedStart() - done);
            out += u'@';
            out += mention->name;
            done = m.capturedEnd();
        }
    }
    out += QStringView(text).sliced(done);
    return out;
}

QList<std::pair<qsizetype, qsizetype>> Format::nameTokens(QStringView text, const QStringList &names)
{
    const auto word = [&](qsizetype i) { return i >= 0 && i < text.size() && text[i].isLetterOrNumber(); };
    QList<std::pair<qsizetype, qsizetype>> found;
    for (qsizetype at = text.indexOf(u'@'); at >= 0; at = text.indexOf(u'@', at + 1)) {
        if (word(at - 1))
            continue;
        qsizetype best = 0;
        for (const QString &name : names)
            if (name.size() > best && text.sliced(at + 1).startsWith(name) && !word(at + 1 + name.size()))
                best = name.size();
        if (best > 0) {
            found.append({at, best + 1});
            at += best;
        }
    }
    return found;
}

QString Format::user(const QString &jid)
{
    return jid.section(u'@', 0, 0).section(u':', 0, 0);
}

QString Format::fold(const QString &s)
{
    QString out;
    for (QChar c : s.normalized(QString::NormalizationForm_D))
        if (c.category() != QChar::Mark_NonSpacing)
            out.append(c);
    return out.toCaseFolded();
}
