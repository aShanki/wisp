// One message row, drawn like WhatsApp: a bubble with a tail on the first
// message of a group, the time and ticks at its bottom right. Plain Text and
// Rectangle items with explicit geometry; rare parts sit in Loaders.
pragma ComponentBehavior: Bound
import QtQuick
import Wisp

Item {
    id: root

    required property string msgId
    required property string senderName
    required property string senderJid
    required property int senderColor
    required property bool fromMe
    required property string timeText
    required property string type
    required property string status
    required property bool edited
    required property bool deleted
    required property string richText
    required property bool hasMentions
    required property int emojiCount
    required property bool emojiOnly
    required property var replyTo
    required property var reactions
    required property bool hasMedia
    required property int mediaWidth
    required property int mediaHeight
    required property int mediaDuration
    required property var mediaWaveform
    required property string sender
    required property string mediaFileName
    required property real mediaSize
    required property bool mediaLoading
    required property int mediaProgress
    required property string thumbSource
    required property string mediaSource
    required property url mediaUrl
    required property string dayLabel
    required property bool firstOfDay
    required property bool firstInGroup

    property bool isGroup: false
    property real spaceWidth: 4
    property real windowWidth: width
    signal imageClicked(string id, string source, int w, int h)

    readonly property bool isSystem: type === "system"
    readonly property bool isDeleted: deleted || type === "revoked"
    readonly property bool isVisual: !isDeleted && hasMedia
        && (type === "image" || type === "video" || type === "gif" || type === "sticker")
    readonly property bool isFile: !isDeleted && hasMedia && !isVisual
    readonly property bool isAudio: isFile && (type === "voice" || type === "audio")
    readonly property bool hasQuote: !isDeleted && !!replyTo
    // One to three emoji are drawn large, in Web's image boxes: one alone
    // without a bubble (in the bubble at 48 px when quoting), two or three in it.
    readonly property bool bigEmoji: emojiOnly && !isDeleted
    readonly property int emojiBox: !bigEmoji ? 0 : emojiCount === 1 ? (hasQuote ? 48 : 60) : emojiCount === 2 ? 36 : 24
    readonly property bool bareEmoji: bigEmoji && emojiCount === 1 && !hasQuote
    readonly property bool isSticker: type === "sticker" && isVisual
    readonly property bool bare: bareEmoji || isSticker
    // A bare emoji's sender sits in its own 29 px pill, 5 px above the block.
    readonly property real emojiY: bareEmoji ? (showSender ? 40 : 12) : bubble.bodyY + 6
    readonly property bool showSender: isGroup && !fromMe && firstInGroup && !isSystem
    readonly property bool tail: firstInGroup && !bare
    // Group chats keep a column left of incoming bubbles for the sender's avatar.
    readonly property int avatarCol: isGroup && !fromMe && !isSystem ? 38 : 0
    readonly property string kindLabel: type === "location" ? "Location" : type === "contact" ? "Contact"
        : type === "poll" ? "Poll" : type === "unsupported" ? "Unsupported message" : ""
    readonly property string bodyText: isDeleted ? (fromMe ? "You deleted this message" : "This message was deleted")
        : kindLabel === "" ? richText
        : "<font color=\"" + Theme.bubbleMeta + "\">" + kindLabel + "</font>" + (richText !== "" ? "  " + richText : "")
    readonly property bool hasBody: bodyText !== "" && !isSystem
    // The time sits over the image when there is no caption.
    readonly property bool metaOnImage: isVisual && !hasBody && !bare
    // A deleted message's text follows Web's 22 px icon in a 24 px slot.
    readonly property int bodyIndent: isDeleted ? 29 : 0
    readonly property Item bubbleItem: bubble

    // Geometry, in WhatsApp Web's pixels. Its row padding and bubble width
    // follow the window width: fixed sides in 1:1 chats, a share of the row
    // in groups.
    readonly property real groupSide: width * (0.05 + 0.005
        * [800, 856, 912, 968, 1024, 1088, 1152, 1216, Infinity].findIndex(w => windowWidth <= w))
    readonly property real sideStart: isGroup ? groupSide : windowWidth <= 968 ? 25 : 63
    readonly property real sideEnd: isGroup ? groupSide : windowWidth <= 968 ? 19 : 63
    readonly property real maxBubble: (width - sideStart - sideEnd - avatarCol)
        * (windowWidth <= 900 ? 0.95 : windowWidth <= 1024 ? 0.85 : windowWidth <= 1300 ? 0.75 : 0.65)
    readonly property int padL: 9
    readonly property int padR: 7
    readonly property int padT: 6
    readonly property int padB: 8

    // Photos and videos are cropped as WhatsApp Web does: 330 px wide, 240
    // when portrait, the height by the aspect ratio kept within 0.71 to 4.
    // Stickers are letterboxed in a 190 px square.
    readonly property real aspect: (mediaWidth > 0 ? mediaWidth : 4) / (mediaHeight > 0 ? mediaHeight : 3)
    readonly property int imageW: !isVisual ? 0 : isSticker ? 190
        : aspect < 1 ? 240 : Math.round(Math.min(330, maxBubble - 6))
    readonly property int imageH: !isVisual ? 0 : isSticker ? 190
        : Math.round(aspect < 1 ? 240 / Math.max(0.71, aspect) : imageW / Math.min(4, aspect))
    // Only text messages use Web's Body2 text; captions and the deleted
    // placeholder take the bubble's 14.2 px type on 19 px lines.
    readonly property bool body2: !bigEmoji && !isDeleted && !hasMedia

    readonly property real contentMax: isVisual ? imageW - padL - padR + 6 : maxBubble - padL - padR
    readonly property real innerW: isVisual ? contentMax : Math.min(contentMax, Math.max(
        showSender ? sender.implicitWidth : 0,
        quote.item ? quote.item.implicitWidth - 10 : 0,
        file.item ? file.item.implicitWidth - 10 : 0,
        bigEmoji ? emojiCount * emojiBox + 32 : hasBody ? body.implicitWidth + bodyIndent : 0,
        meta.width))

    readonly property bool unreadMark: msgId === Conversation.unreadFrom
    readonly property real dayH: (firstOfDay ? 36 : 0) + (unreadMark ? 46 : 0)
    readonly property real gap: firstInGroup ? 12 : 2
    implicitHeight: dayH + gap + (isSystem ? system.height + 4
        : bubble.height + (reactionsLoader.active ? 26 : 0))

    // A rounded fill with WhatsApp's 1 px drop shadow.
    component Pill: Rectangle {
        radius: 7.5
        Rectangle {
            z: -1
            y: 1
            width: parent.width
            height: parent.height
            radius: parent.radius
            topLeftRadius: parent.topLeftRadius
            color: Theme.bubbleEdge
        }
    }

    Loader {
        active: root.firstOfDay
        y: 12
        anchors.horizontalCenter: parent.horizontalCenter
        sourceComponent: Chip {
            text: root.dayLabel
            font.weight: 545
            font.variableAxes: ({ wght: 545 })
        }
    }
    Loader {
        active: root.unreadMark
        y: root.dayH - 40
        width: root.width
        height: 34
        sourceComponent: Rectangle {
            color: Theme.quote
            Chip {
                anchors.centerIn: parent
                text: Conversation.unreadCount === 1 ? "1 unread message" : Conversation.unreadCount + " unread messages"
            }
        }
    }

    Chip {
        id: system
        visible: root.isSystem
        y: root.dayH + root.gap
        anchors.horizontalCenter: parent.horizontalCenter
        maxWidth: root.width - root.sideStart - root.sideEnd
        text: visible ? root.richText : ""
    }

    // WhatsApp's 1 px drop shadow, without effects.
    Rectangle {
        visible: !root.isSystem && !root.bare
        x: bubble.x
        y: bubble.y + 1
        width: bubble.width
        height: bubble.height
        radius: bubble.radius
        topLeftRadius: bubble.topLeftRadius
        topRightRadius: bubble.topRightRadius
        color: Theme.bubbleEdge
    }

    Rectangle {
        id: bubble
        visible: !root.isSystem
        readonly property real pad: root.isVisual && !root.bare ? 3 : 0
        readonly property real senderH: root.showSender ? 22 : 0
        // The quote overlaps the padding: 3 px from the top (6 on a voice
        // note), or 5 px under the sender.
        readonly property real quoteY: root.showSender ? root.padT + senderH + 5 : root.isAudio ? 6 : 3
        // Media sits 3 px inside the bubble, 5 px under the sender or 3 under
        // a quote (a voice note 8); a sticker 6 px, or 5 px under the
        // sender's 29 px pill.
        readonly property real mediaY: root.hasQuote ? quoteY + quote.height + (root.isAudio ? 8 : root.isVisual || root.isFile ? 3 : 6)
            : root.isSticker ? (root.showSender ? 34 : 6)
            : root.isVisual || root.isFile && !root.isAudio ? (root.showSender ? root.padT + senderH + 5 : 3)
            : root.padT + senderH
        readonly property real bodyY: mediaY + (root.isVisual ? root.imageH + 7 : root.isFile ? file.height + 7 : 0)
        x: root.fromMe ? root.width - root.sideEnd - width : root.sideStart + root.avatarCol
        y: root.dayH + root.gap
        // Web's emoji block: 6 px above the images, 15 and 17 px beside them,
        // the image's line descent and 14 px below; a 19 px line holds the time.
        // A sticker has 9 px before it, 4 after and its time pill 3 px under.
        // A voice note is 336 px, its player 6 px inside.
        width: root.isSticker ? 203 : root.isVisual ? root.imageW + 2 * pad : root.bareEmoji ? root.emojiBox + 45
            : root.isAudio ? Math.min(336, root.maxBubble)
            : root.innerW + root.padL + root.padR
        height: root.bareEmoji ? root.emojiY + root.emojiBox + 38
              : root.isSticker ? mediaY + root.imageH + 24
              : root.bigEmoji ? bodyY + root.emojiBox + 52
              : root.hasBody ? bodyY + body.height + root.padB + (root.isDeleted ? 4 : 0)
              : root.isVisual ? mediaY + root.imageH + pad
              : root.isAudio ? mediaY + 61
              : root.isFile ? mediaY + file.height + 25
              : bodyY + meta.height + root.padB - 4
        radius: 7.5
        topLeftRadius: root.tail && !root.fromMe ? 0 : 7.5
        topRightRadius: root.tail && root.fromMe ? 0 : 7.5
        color: root.bare ? "transparent" : root.fromMe ? Theme.bubbleOut : Theme.bubbleIn

        // Without a bubble, the sender and the time sit in pills of its colour.
        Loader {
            active: root.bare
            anchors.fill: parent
            sourceComponent: Item {
                readonly property color fill: root.fromMe ? Theme.bubbleOut : Theme.bubbleIn
                Pill {
                    visible: root.showSender
                    color: parent.fill
                    width: bubble.width
                    height: 29
                    topLeftRadius: 0
                }
                Pill {
                    color: parent.fill
                    x: meta.x - 6
                    y: meta.y - 3
                    width: meta.width + 13
                    height: 21
                }
            }
        }

        Icon {
            visible: root.tail && !root.isSystem
            x: root.fromMe ? bubble.width : -8
            width: 8
            height: 13
            sourceSize: Qt.size(8, 13)
            source: visible ? "qrc:/assets/tail.svg" : ""
            mirror: root.fromMe
            color: bubble.color
        }

        Text {
            id: sender
            visible: root.showSender
            x: root.isAudio ? 6 : root.padL
            y: root.bare ? 4 : root.padT
            width: root.innerW
            height: 22
            verticalAlignment: Text.AlignVCenter
            text: visible ? root.senderName : ""
            textFormat: Text.PlainText
            color: Theme.senderColor(root.senderColor)
            font.pixelSize: 13
            font.weight: Font.Medium
            font.variableAxes: ({ wght: Font.Medium })
            elide: Text.ElideRight
        }
        Loader {
            id: quote
            active: root.hasQuote
            x: root.isVisual ? 3 : root.padL - 6
            y: bubble.quoteY
            sourceComponent: QuoteBlock {
                width: root.isVisual ? root.imageW : root.isAudio ? bubble.width - 6 : root.innerW + 10
                quote: root.replyTo
            }
        }
        Loader {
            active: root.isVisual
            x: root.isSticker ? 9 : bubble.pad
            y: bubble.mediaY
            width: root.imageW
            height: root.imageH
            sourceComponent: MessageImage {
                id: image
                thumbSource: root.thumbSource
                mediaSource: root.mediaSource
                playable: root.type === "video" || root.type === "gif"
                gif: root.type === "gif"
                videoUrl: root.mediaUrl
                Component.onCompleted: if (gif && videoUrl.toString() === "" && !loading) Conversation.fetchMedia(root.msgId)
                duration: root.mediaDuration
                loading: root.mediaLoading
                progress: root.mediaProgress
                maskColor: bubble.color
                sticker: root.isSticker
                onClicked: image.playable ? Conversation.playVideo(root.msgId)
                           : root.mediaSource !== "" && !image.failed
                           ? root.imageClicked(root.msgId, root.mediaSource, root.mediaWidth, root.mediaHeight)
                           : Conversation.openMedia(root.msgId)
            }
        }
        Loader {
            active: root.isAudio
            x: 6
            y: bubble.mediaY
            width: bubble.width - 12
            sourceComponent: VoiceNote {
                msgId: root.msgId
                sender: root.sender
                fromMe: root.fromMe
                played: root.status === "played"
                duration: root.mediaDuration
                loading: root.mediaLoading
                levels: root.mediaWaveform
                fill: bubble.color
            }
        }
        Loader {
            id: file
            active: root.isFile && !root.isAudio
            x: root.padL - 6
            y: bubble.mediaY
            sourceComponent: MessageFile {
                width: root.innerW + 10
                msgId: root.msgId
                fileName: root.mediaFileName
                size: root.mediaSize
                loading: root.mediaLoading
            }
        }
        Loader {
            active: root.isDeleted
            x: root.padL - 2
            y: bubble.bodyY - 1
            sourceComponent: Icon { name: "block"; size: 22; color: Theme.bubbleMeta }
        }
        // A run of no-break spaces reserves room for the time on the last
        // line: Web's spacer is the time at 11 px with 4 px either side.
        // Qt sets fixed-height lines' text at their top, Web centres it in
        // the 20 px line, hence the 2 px.
        Text {
            id: body
            visible: root.hasBody
            x: root.padL + (root.bigEmoji ? 15 : root.bodyIndent)
            y: root.bigEmoji ? root.emojiY + (root.emojiBox - implicitHeight) / 2
                : bubble.bodyY + (root.isDeleted ? 3 : root.body2 ? 2 : 1) - richDrop
            // StyledText sets a fixed-height line's baseline at the font's
            // ascent, RichText's QTextDocumentLayout at 4/5 of the line; lift
            // RichText's to match. baselineOffset is the ascent in both.
            readonly property real richDrop: textFormat === Text.RichText && lineHeightMode === Text.FixedHeight
                ? Math.round(lineHeight * 4 / 5) - Math.round(baselineOffset) : 0
            width: root.bigEmoji ? root.emojiCount * root.emojiBox : Math.min(root.innerW - root.bodyIndent, implicitWidth)
            horizontalAlignment: root.bigEmoji ? Text.AlignHCenter : Text.AlignLeft
            text: !visible ? "" : root.bigEmoji ? root.bodyText
                : mentionStyle + root.bodyText + " "
                  + "&nbsp;".repeat(Math.round((meta.width + 5.5) / (root.spaceWidth + font.letterSpacing)) - 1)
            // StyledText underlines every link; WhatsApp's mentions are plain
            // coloured names, so their rare rows take RichText's CSS.
            readonly property string mentionStyle: root.hasMentions
                ? "<style>a{color:" + Theme.link + "}a.m{text-decoration:none}</style>" : ""
            textFormat: root.hasMentions ? Text.RichText : Text.StyledText
            wrapMode: root.bigEmoji ? Text.NoWrap : Text.Wrap
            color: root.isDeleted ? Theme.bubbleMeta : Theme.text
            linkColor: Theme.link
            font.pixelSize: root.bigEmoji ? root.emojiBox / Theme.emojiAdvance : 14
            font.italic: root.isDeleted
            font.letterSpacing: root.body2 ? 0.16 : 0
            lineHeight: root.bigEmoji ? 1 : root.body2 ? 20 : 19
            lineHeightMode: root.bigEmoji ? Text.ProportionalHeight : Text.FixedHeight
            onLinkActivated: link => {
                if (!link.startsWith("mention:"))
                    Qt.openUrlExternally(link)
                else
                    Conversation.open(link.slice(8))
            }
        }

        Rectangle {
            visible: root.metaOnImage
            x: bubble.pad
            y: bubble.height - bubble.pad - 28
            width: root.imageW
            height: 28
            bottomLeftRadius: 6
            bottomRightRadius: 6
            gradient: Gradient {
                GradientStop { position: 0; color: "transparent" }
                GradientStop { position: 1; color: Qt.rgba(0, 0, 0, 0.35) }
            }
        }
        Row {
            id: meta
            // A voice note's time clears their photo.
            x: bubble.width - width - (root.metaOnImage ? 8 : root.isAudio && !root.fromMe ? 85 : root.padR)
            y: bubble.height - height - (root.metaOnImage ? 6 : root.isAudio ? 4 : 3)
            height: 15
            spacing: 3
            readonly property color tint: root.metaOnImage ? "white" : Theme.bubbleMeta
            // 12 px in a 16 px line on a 15 px row, as Web sets it.
            Text {
                anchors.verticalCenter: parent.verticalCenter
                anchors.verticalCenterOffset: 1
                text: (root.edited && !root.isDeleted ? "Edited  " : "") + root.timeText
                textFormat: Text.PlainText
                color: meta.tint
                font.pixelSize: 12
            }
            Icon {
                visible: root.fromMe && !root.isDeleted
                anchors.verticalCenter: parent.verticalCenter
                size: 16
                name: !visible ? "" : root.status === "pending" ? "schedule"
                    : root.status === "sent" ? "done" : root.status === "error" ? "error" : "done_all"
                color: root.status === "read" || root.status === "played" ? Theme.read
                    : root.status === "error" ? Theme.danger : meta.tint
            }
        }
    }

    Loader {
        active: root.avatarCol > 0 && root.firstInGroup
        x: root.sideStart
        y: bubble.y
        sourceComponent: Avatar {
            size: 28
            name: root.senderName
            initialColor: Theme.senderColor(root.senderColor)
            source: root.senderJid !== "" ? "image://wisp/avatar/" + root.senderJid : ""
            maskColor: "transparent"
        }
    }

    Loader {
        id: reactionsLoader
        active: !root.isSystem && !root.isDeleted && root.reactions.length > 0
        // WhatsApp Web's pill: 6 px in from the bubble's edge, over its last 4 px.
        x: root.fromMe ? bubble.x + bubble.width - width - 6 : bubble.x + 6
        y: bubble.y + bubble.height - 4
        sourceComponent: Rectangle {
            readonly property int total: root.reactions.reduce((n, r) => n + r.count, 0)
            width: emojis.width + (count.visible ? count.implicitWidth + 7 : 0) + 10
            height: 26
            radius: 13
            color: Theme.bubbleIn
            border.width: 1
            border.color: Theme.conversation
            // Each emoji takes a 20 px cell, as Web's emoji images do.
            Row {
                id: emojis
                x: 5
                anchors.verticalCenter: parent.verticalCenter
                Repeater {
                    model: root.reactions
                    Text {
                        required property var modelData
                        width: 20
                        horizontalAlignment: Text.AlignHCenter
                        text: modelData.emoji
                        textFormat: Text.PlainText
                        color: Theme.text
                        font.pixelSize: 16
                    }
                }
            }
            Text {
                id: count
                visible: parent.total > 1
                x: emojis.x + emojis.width + 4
                anchors.verticalCenter: parent.verticalCenter
                text: parent.total
                color: Theme.bubbleMeta
                font.pixelSize: 14
            }
        }
    }
}
