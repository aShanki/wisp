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
    required property string mediaFileName
    required property real mediaSize
    required property bool mediaLoading
    required property string thumbSource
    required property string mediaSource
    required property string dayLabel
    required property bool firstOfDay
    required property bool firstInGroup

    property bool isGroup: false
    property real spaceWidth: 4
    property real windowWidth: width
    signal imageClicked(string source, int w, int h)

    readonly property bool isSystem: type === "system"
    readonly property bool isDeleted: deleted || type === "revoked"
    readonly property bool isVisual: !isDeleted && hasMedia
        && (type === "image" || type === "video" || type === "gif" || type === "sticker")
    readonly property bool isFile: !isDeleted && hasMedia && !isVisual
    readonly property bool isAudio: isFile && (type === "voice" || type === "audio")
    readonly property bool hasQuote: !isDeleted && !!replyTo
    readonly property bool bare: emojiOnly && !hasQuote && !isDeleted || type === "sticker" && isVisual
    readonly property bool showSender: isGroup && !fromMe && firstInGroup && !isSystem
    readonly property bool tail: firstInGroup && !bare
    readonly property string kindLabel: type === "location" ? "Location" : type === "contact" ? "Contact"
        : type === "poll" ? "Poll" : type === "unsupported" ? "Unsupported message" : ""
    readonly property string bodyText: isDeleted ? (fromMe ? "You deleted this message" : "This message was deleted")
        : kindLabel === "" ? richText
        : "<font color=\"" + Theme.bubbleMeta + "\">" + kindLabel + "</font>" + (richText !== "" ? "  " + richText : "")
    readonly property bool hasBody: bodyText !== "" && !isSystem
    // The time sits over the image when there is no caption.
    readonly property bool metaOnImage: isVisual && !hasBody && !bare
    readonly property int bodyIndent: isDeleted ? 21 : 0
    readonly property Item bubbleItem: bubble

    // Geometry, in WhatsApp Web's pixels. Its row padding and bubble width
    // follow the window width: fixed sides in 1:1 chats, a share of the row
    // in groups.
    readonly property real groupSide: width * (0.05 + 0.005
        * [800, 856, 912, 968, 1024, 1088, 1152, 1216, Infinity].findIndex(w => windowWidth <= w))
    readonly property real sideStart: isGroup ? groupSide : windowWidth <= 968 ? 25 : 63
    readonly property real sideEnd: isGroup ? groupSide : windowWidth <= 968 ? 19 : 63
    readonly property real maxBubble: (width - sideStart - sideEnd)
        * (windowWidth <= 900 ? 0.95 : windowWidth <= 1024 ? 0.85 : windowWidth <= 1300 ? 0.75 : 0.65)
    readonly property int padL: 9
    readonly property int padR: 7
    readonly property int padT: 6
    readonly property int padB: 8

    readonly property real imageScale: {
        const w = mediaWidth > 0 ? mediaWidth : 4, h = mediaHeight > 0 ? mediaHeight : 3
        const maxW = type === "sticker" ? 190 : Math.min(330, maxBubble - 6)
        return Math.min(maxW / w, 338 / h)
    }
    readonly property int imageW: isVisual ? Math.max(120, Math.round((mediaWidth > 0 ? mediaWidth : 4) * imageScale)) : 0
    readonly property int imageH: isVisual ? Math.max(80, Math.round((mediaHeight > 0 ? mediaHeight : 3) * imageScale)) : 0

    readonly property real contentMax: isVisual ? imageW - padL - padR + 6 : maxBubble - padL - padR
    readonly property real innerW: isVisual ? contentMax : Math.min(contentMax, Math.max(
        showSender ? sender.implicitWidth : 0,
        quote.item ? quote.item.implicitWidth - 10 : 0,
        file.active ? file.implicitWidth : 0,
        hasBody ? body.implicitWidth + bodyIndent : 0,
        meta.width))

    readonly property bool unreadMark: msgId === Conversation.unreadFrom
    readonly property real dayH: (firstOfDay ? 46 : 0) + (unreadMark ? 46 : 0)
    readonly property real gap: firstInGroup ? 12 : 2
    implicitHeight: dayH + gap + (isSystem ? system.height + 4
        : bubble.height + (reactionsLoader.active ? 18 : 0))

    Loader {
        active: root.firstOfDay
        y: 12
        anchors.horizontalCenter: parent.horizontalCenter
        sourceComponent: Chip { text: root.dayLabel.toUpperCase() }
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
        // The quote overlaps the padding: 3 px from the top, or 5 px under the sender.
        readonly property real quoteY: root.showSender ? root.padT + senderH + 5 : 3
        readonly property real mediaY: root.hasQuote ? quoteY + quote.height + 6
            : root.isVisual && !root.showSender ? 3 : root.padT + senderH
        readonly property real bodyY: mediaY + (root.isVisual ? root.imageH + 4 : root.isFile ? file.height + 4 : 0)
        x: root.fromMe ? root.width - root.sideEnd - width : root.sideStart
        y: root.dayH + root.gap
        width: root.isVisual ? root.imageW + 2 * pad : root.innerW + root.padL + root.padR
        height: root.bare ? (root.isVisual ? mediaY + root.imageH : bodyY + body.height) + meta.height + 2
              : root.hasBody ? bodyY + body.height + root.padB
              : root.isVisual ? mediaY + root.imageH + pad
              : root.isAudio ? mediaY + file.height + 4
              : bodyY + meta.height + root.padB - 4
        radius: 7.5
        topLeftRadius: root.tail && !root.fromMe ? 0 : 7.5
        topRightRadius: root.tail && root.fromMe ? 0 : 7.5
        color: root.bare ? "transparent" : root.fromMe ? Theme.bubbleOut : Theme.bubbleIn

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
            x: root.padL
            y: root.padT
            width: root.innerW
            height: 22
            verticalAlignment: Text.AlignVCenter
            text: visible ? root.senderName : ""
            textFormat: Text.PlainText
            color: Theme.senderColor(root.senderColor)
            font.pixelSize: 13
            font.weight: Font.Medium
            elide: Text.ElideRight
        }
        Loader {
            id: quote
            active: root.hasQuote
            x: root.isVisual ? 3 : root.padL - 6
            y: bubble.quoteY
            sourceComponent: QuoteBlock {
                width: root.isVisual ? root.imageW : root.innerW + 10
                quote: root.replyTo
            }
        }
        Loader {
            active: root.isVisual
            x: bubble.pad
            y: bubble.mediaY
            width: root.imageW
            height: root.imageH
            sourceComponent: MessageImage {
                id: image
                thumbSource: root.thumbSource
                mediaSource: root.mediaSource
                playable: root.type === "video" || root.type === "gif"
                duration: root.mediaDuration
                loading: root.mediaLoading
                maskColor: bubble.color
                sticker: root.bare
                onClicked: root.mediaSource !== "" && !image.failed
                           ? root.imageClicked(root.mediaSource, root.mediaWidth, root.mediaHeight)
                           : Conversation.openMedia(root.msgId)
            }
        }
        Loader {
            id: file
            active: root.isFile
            x: root.padL - 6
            y: bubble.mediaY
            width: root.innerW + 10
            sourceComponent: MessageFile {
                msgId: root.msgId
                type: root.type
                fileName: root.mediaFileName
                size: root.mediaSize
                duration: root.mediaDuration
                loading: root.mediaLoading
                fromMe: root.fromMe
            }
        }
        Loader {
            active: root.isDeleted
            x: root.padL
            y: bubble.bodyY + 1
            sourceComponent: Icon { name: "block"; size: 17; color: Theme.bubbleMeta }
        }
        // A run of no-break spaces reserves room for the time on the last
        // line: Web's spacer is the time at 11 px with 4 px either side.
        // Qt sets fixed-height lines' text at their top, Web centres it in
        // the 20 px line, hence the 2 px.
        Text {
            id: body
            visible: root.hasBody
            x: root.padL + root.bodyIndent
            y: bubble.bodyY + (root.bare ? 0 : 2)
            width: Math.min(root.innerW - root.bodyIndent, implicitWidth)
            text: !visible ? "" : root.bare ? root.bodyText
                : mentionStyle + root.bodyText + " "
                  + "&nbsp;".repeat(Math.round((meta.width + 5.5) / (root.spaceWidth + font.letterSpacing)) - 1)
            // StyledText underlines every link; WhatsApp's mentions are plain
            // coloured names, so their rare rows take RichText's CSS.
            readonly property string mentionStyle: root.hasMentions
                ? "<style>a{color:" + Theme.link + "}a.m{text-decoration:none}</style>" : ""
            textFormat: root.hasMentions ? Text.RichText : Text.StyledText
            wrapMode: Text.Wrap
            color: root.isDeleted ? Theme.bubbleMeta : Theme.text
            linkColor: Theme.link
            font.pixelSize: root.bare ? (root.emojiCount === 1 ? 64 : root.emojiCount === 2 ? 48 : 40) : 14
            font.italic: root.isDeleted
            font.letterSpacing: root.bare ? 0 : 0.16
            lineHeight: root.bare ? 1 : 20
            lineHeightMode: root.bare ? Text.ProportionalHeight : Text.FixedHeight
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
        Rectangle {
            visible: root.bare
            x: meta.x - 4
            y: meta.y - 1
            width: meta.width + 8
            height: meta.height + 2
            radius: 6
            color: root.fromMe ? Theme.bubbleOut : Theme.bubbleIn
        }
        Row {
            id: meta
            x: bubble.width - width - (root.metaOnImage ? 7 : root.padR)
            y: bubble.height - height - (root.metaOnImage ? 5 : root.bare ? 0 : 3)
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
                color: root.status === "read" ? Theme.read : root.status === "error" ? Theme.danger : meta.tint
            }
        }
    }

    Loader {
        id: reactionsLoader
        active: !root.isSystem && !root.isDeleted && root.reactions.length > 0
        x: root.fromMe ? bubble.x + bubble.width - width - 8 : bubble.x + 8
        y: bubble.y + bubble.height - 4
        sourceComponent: Rectangle {
            readonly property int total: root.reactions.reduce((n, r) => n + r.count, 0)
            width: emojis.implicitWidth + 12
            height: 22
            radius: 11
            color: root.fromMe ? Theme.bubbleOut : Theme.bubbleIn
            border.width: 1
            border.color: Theme.conversation
            Text {
                id: emojis
                anchors.centerIn: parent
                text: root.reactions.map(r => r.emoji).join("") + (parent.total > 1 ? " " + parent.total : "")
                textFormat: Text.PlainText
                color: Theme.bubbleMeta
                font.pixelSize: 13
            }
        }
    }
}
