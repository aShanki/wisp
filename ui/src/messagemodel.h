// Messages of the open chat (Conversation.messages). Row 0 is the newest.
#pragma once

#include <QAbstractListModel>
#include <QJsonArray>
#include <QJsonObject>
#include <QList>
#include <QString>
#include <QtQml/qqmlregistration.h>

#include "format.h"

class MessageModel : public QAbstractListModel
{
    Q_OBJECT
    QML_ELEMENT
    QML_UNCREATABLE("Use Conversation.messages")

public:
    enum Roles {
        MsgIdRole = Qt::UserRole + 1,
        SenderNameRole,
        FromMeRole,
        TimeTextRole,
        TypeRole,
        StatusRole,
        EditedRole,
        DeletedRole,
        RichTextRole,
        EmojiOnlyRole,
        EmojiCountRole,
        ReplyToRole,
        ReactionsRole,
        HasMediaRole,
        MediaWidthRole,
        MediaHeightRole,
        MediaDurationRole,
        MediaFileNameRole,
        MediaSizeRole,
        MediaLoadingRole,
        MediaProgressRole,
        MediaWaveformRole,
        SenderRole,
        ThumbSourceRole,
        MediaSourceRole,
        MediaUrlRole,
        DayLabelRole,
        FirstOfDayRole,
        FirstInGroupRole,
        SenderColorRole,
        HasMentionsRole,
        SenderJidRole,
    };

    struct Reaction {
        QString emoji;
        int count = 0;
        bool mine = false;
        bool operator==(const Reaction &) const = default;
    };

    struct Msg {
        QString id, sender, senderName, type, text, status;
        QString replyId, replySender, replyPreview;
        QString fileName, path;
        QByteArray waveform;
        QList<Reaction> reactions;
        QList<Format::Mention> mentions;
        qint64 timestamp = 0, day = 0, size = 0;
        int width = 0, height = 0, duration = 0;
        int senderColor = -1, replyColor = -1;
        bool fromMe = false, edited = false, deleted = false, isReply = false;
        bool hasMedia = false, isImage = false, hasThumb = false, mediaLoading = false;
        int emojiCount = 0;
        int mediaProgress = -1; // download percent while loading, -1 unknown
        mutable bool richDone = false;
        mutable QString rich;
    };

    explicit MessageModel(QObject *parent = nullptr);

    int rowCount(const QModelIndex &parent = {}) const override;
    QVariant data(const QModelIndex &index, int role) const override;
    QHash<int, QByteArray> roleNames() const override;

    // Switch chats ("" for none). The only reset.
    void reset(const QString &chat);
    // Append an older page (newest first, as messages.list returns it).
    void appendOlder(const QJsonArray &page);
    // Insert or update by id. Returns true if a row was inserted.
    bool upsert(const QJsonObject &msg);
    // Update a loaded message; unknown ids are ignored.
    void update(const QJsonObject &msg);
    // Drop a message (deleted for us); unknown ids are ignored.
    void remove(const QString &id);

    Q_INVOKABLE int indexOf(const QString &id) const;
    const Msg *find(const QString &id) const;
    const Msg *oldest() const { return m_rows.isEmpty() ? nullptr : &m_rows.last(); }
    const Msg &at(int row) const { return m_rows[row]; }
    void setMediaLoading(const QString &id, bool loading);
    void setMediaProgress(const QString &id, int percent);
    void setMediaPath(const QString &id, const QString &path);
    // {id, senderName, preview} for the reply bar.
    QVariantMap quote(const QString &id) const;

private:
    static Msg parse(const QJsonObject &o);
    void replace(int row, Msg &&n);
    void derivedChanged(int row);
    bool firstOfDay(int row) const;
    bool firstInGroup(int row) const;

    QString m_chat;
    QList<Msg> m_rows; // newest first
};
