// One chat list row, laid out like WhatsApp's: fixed height, plain Text.
import QtQuick
import Wisp

Item {
    id: row

    required property int index
    required property string jid
    required property string name
    required property bool isGroup
    required property string avatarSource
    required property string preview
    required property string richPreview
    required property string previewSender
    required property string timeText
    required property int unread
    required property bool muted
    required property bool pinned
    required property string lastStatus
    required property string typingText

    property bool current: false
    readonly property bool open: jid === Conversation.jid
    readonly property bool hovered: mouse.containsMouse

    signal clicked()
    signal contextMenuRequested(real x, real y)
    signal menuRequested(Item button)

    width: ListView.view ? ListView.view.width : 0
    height: Theme.rowHeight

    Rectangle {
        id: bg
        objectName: "chatBackground"
        x: 10
        width: parent.width - 20
        height: parent.height
        radius: 12
        color: row.open ? Theme.selected
             : row.current || row.hovered ? Theme.hover
             : Theme.panel
    }

    Avatar {
        id: avatar
        objectName: "chatAvatar"
        x: 20
        anchors.verticalCenter: parent.verticalCenter
        name: row.name
        group: row.isGroup
        source: row.avatarSource
        maskColor: bg.color
    }

    Text {
        id: nameText
        objectName: "chatName"
        x: avatar.x + avatar.width + 14
        y: (row.height - 43) / 2
        height: 21
        topPadding: 1
        verticalAlignment: Text.AlignVCenter
        width: timeText.x - x - 6
        text: row.name
        textFormat: Text.PlainText
        elide: Text.ElideRight
        color: Theme.text
        font.pixelSize: 16
    }

    Text {
        id: timeText
        objectName: "chatTime"
        anchors.right: parent.right
        anchors.rightMargin: 25
        y: nameText.y + 5
        height: 14
        topPadding: 1
        verticalAlignment: Text.AlignVCenter
        text: row.timeText
        color: row.unread > 0 && !row.muted ? Theme.accent : Theme.textMuted
        font.pixelSize: Theme.fontSizeSmall
        font.weight: row.unread > 0 ? Font.Medium : Font.Normal
    }

    Icon {
        id: tick
        x: nameText.x
        anchors.verticalCenter: previewText.verticalCenter
        visible: row.typingText === "" && row.lastStatus !== "" && row.lastStatus !== "error"
        size: 18
        name: row.lastStatus === "pending" ? "schedule" : row.lastStatus === "sent" ? "done" : "done_all"
        color: row.lastStatus === "read" ? Theme.read : Theme.textMuted
    }

    Text {
        id: previewText
        objectName: "chatPreview"
        anchors.left: tick.visible ? tick.right : nameText.left
        anchors.leftMargin: tick.visible ? 3 : 0
        anchors.right: meta.left
        anchors.rightMargin: meta.width > 0 ? 6 : 0
        y: nameText.y + nameText.height + 2
        height: 20
        topPadding: 1
        clip: true
        verticalAlignment: Text.AlignVCenter
        // WhatsApp markup (*bold*, _italic_, ~strike~, ```mono```) shown plain.
        readonly property bool mentions: row.typingText === "" && row.richPreview !== ""
        readonly property string linkStyle: "<style>a{color:" + Theme.link + ";text-decoration:none}</style>"
        readonly property string rich: (row.previewSender !== "" && row.previewSender !== "You" ? row.previewSender.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;") + ": " : "")
              + row.richPreview
        text: mentions ? linkStyle + Format.elideRichText(linkStyle + rich, font, width)
            : row.typingText !== "" ? row.typingText
            : (row.previewSender !== "" && row.previewSender !== "You" ? row.previewSender + ": " : "")
              + row.preview.replace(/(```|[*_~])(\S(?:.*?\S)?)\1/g, "$2")
        textFormat: mentions ? Text.RichText : Text.PlainText
        maximumLineCount: 1
        elide: Text.ElideRight
        color: row.typingText !== "" ? Theme.accent : Theme.textMuted
        font.pixelSize: Theme.fontSize
    }

    Row {
        id: meta
        objectName: "chatMeta"
        anchors.right: parent.right
        anchors.rightMargin: 25
        anchors.verticalCenter: previewText.verticalCenter
        spacing: 4

        Icon {
            visible: row.muted
            anchors.verticalCenter: parent.verticalCenter
            size: 18
            name: "notifications_off"
            color: Theme.textMuted
        }
        Icon {
            visible: row.pinned
            anchors.verticalCenter: parent.verticalCenter
            size: 18
            name: "push_pin"
            color: Theme.textMuted
        }
        Rectangle {
            visible: row.unread > 0
            anchors.verticalCenter: parent.verticalCenter
            height: 20
            width: Math.max(height, count.implicitWidth + 12)
            radius: height / 2
            color: row.muted ? Theme.badgeMuted : Theme.accent

            Text {
                id: count
                anchors.centerIn: parent
                text: row.unread > 999 ? "999+" : row.unread
                color: row.muted ? Theme.textMuted : Theme.accentText
                font.pixelSize: Theme.fontSizeSmall
                font.weight: Font.DemiBold
            }
        }
        Icon {
            id: chevron
            objectName: "rowMenuButton"
            visible: row.hovered
            anchors.verticalCenter: parent.verticalCenter
            size: 22
            name: "keyboard_arrow_down"
            color: Theme.textMuted
        }
    }

    MouseArea {
        id: mouse
        anchors.fill: parent
        hoverEnabled: true
        acceptedButtons: Qt.LeftButton | Qt.RightButton
        onClicked: mouse => {
            if (mouse.button === Qt.RightButton)
                row.contextMenuRequested(mouse.x, mouse.y)
            else if (chevron.visible && mouse.x >= meta.x + chevron.x - 4 && mouse.y >= meta.y - 6)
                row.menuRequested(chevron)
            else {
                const p = previewText.mapFromItem(row, mouse.x, mouse.y)
                const link = previewText.linkAt(p.x, p.y)
                if (link.startsWith("mention:"))
                    Conversation.open(link.slice(8))
                else
                    row.clicked()
            }
        }
    }
}
