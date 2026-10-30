// A message's dropdown, from its hover chevron or a right click. Messages
// that can take a reaction get WhatsApp's quick reactions in a pill just above
// the card. The pill is part of the menu, so one click outside closes both.
pragma ComponentBehavior: Bound
import QtQuick
import Wisp

PopupMenu {
    id: menu
    property MessageDelegate target: null
    signal replyRequested(string id)
    signal copyRequested(string richText)
    signal reactRequested(string id, string mine)
    signal moreReactionsRequested(string id, string mine)
    signal editRequested(string id, string richText, string timeText)
    signal deleteRequested(string id, bool forEveryone)

    // Our own messages exist on WhatsApp once sent.
    function sent(m) { return !m.fromMe || (m.status !== "pending" && m.status !== "error") }
    function canReply(m) { return !m.isDeleted && !Conversation.readOnly }
    function canCopy(m) { return !m.isDeleted && m.richText !== "" }
    function canOpen(m) { return !m.isDeleted && m.hasMedia }
    function canCopyImage(m) { return canOpen(m) && (m.type === "image" || m.isSticker) }
    function canReact(m) { return !m.isDeleted && sent(m) }
    function canDeleteForEveryone(m) { return m.fromMe && !m.isDeleted && sent(m) }
    function myReaction(m) {
        const r = m.reactions.find(r => r.mine)
        return r ? r.emoji : ""
    }
    // Whether m gets a menu at all: system messages don't. Any other can be
    // deleted for us.
    function offers(m) { return !!m && !m.isSystem }

    onClosed: target = null

    readonly property bool quick: !!target && canReact(target)
    readonly property real quickHeight: quick ? quickBar.height + 8 : 0
    function quickReact(emoji) {
        const id = target.msgId, mine = myReaction(target)
        close()
        Conversation.react(id, emoji === mine ? "" : emoji)
    }
    function moreReactions() {
        const id = target.msgId, mine = myReaction(target)
        close()
        moreReactionsRequested(id, mine)
    }

    topPadding: 8 + quickHeight
    // The card starts below the pill. Clicks only reach the content item,
    // so the pill lives there, drawn above the entries.
    background: Item {
        implicitWidth: Math.max(236, menu.quick ? quickBar.width : 0)
        Rectangle {
            y: menu.quickHeight
            width: parent.width
            height: parent.height - y
            radius: 16
            color: Theme.elevated
            border.width: 1
            border.color: Theme.divider
        }
    }
    contentItem: Item {
        implicitHeight: entries.contentHeight
        ListView {
            id: entries
            anchors.fill: parent
            model: menu.contentModel
            interactive: false
            currentIndex: menu.currentIndex
        }
        Rectangle {
            id: quickBar
            objectName: "quickReactions"
            x: -menu.leftPadding
            y: -menu.topPadding
            visible: menu.quick
            width: quickRow.width + 12
            height: 52
            radius: 26
            color: Theme.elevated
            border.width: 1
            border.color: Theme.divider
            Row {
                id: quickRow
                anchors.centerIn: parent
                spacing: 2
                Repeater {
                    model: ["👍", "❤️", "😂", "😮", "😢", "🙏"]
                    delegate: Rectangle {
                        id: cell
                        required property string modelData
                        readonly property bool chosen: !!menu.target && menu.myReaction(menu.target) === modelData
                        width: 40
                        height: 40
                        radius: 20
                        color: chosen ? Theme.selected : cellMouse.containsMouse ? Theme.hover : "transparent"
                        Text {
                            anchors.centerIn: parent
                            text: cell.modelData
                            textFormat: Text.PlainText
                            font.pixelSize: 24
                        }
                        MouseArea {
                            id: cellMouse
                            anchors.fill: parent
                            hoverEnabled: true
                            cursorShape: Qt.PointingHandCursor
                            onClicked: menu.quickReact(cell.modelData)
                        }
                    }
                }
                Rectangle {
                    objectName: "quickMore"
                    width: 40
                    height: 40
                    radius: 20
                    color: moreMouse.containsMouse ? Theme.hover : Theme.selected
                    Icon {
                        anchors.centerIn: parent
                        name: "add"
                        size: 22
                    }
                    MouseArea {
                        id: moreMouse
                        anchors.fill: parent
                        hoverEnabled: true
                        cursorShape: Qt.PointingHandCursor
                        onClicked: menu.moreReactions()
                    }
                }
            }
        }
    }

    PopupMenu.Entry {
        text: "Reply"
        iconName: "reply"
        enabled: !!menu.target && menu.canReply(menu.target)
        onTriggered: menu.replyRequested(menu.target.msgId)
    }
    PopupMenu.Entry {
        text: "Copy"
        iconName: "content_copy"
        enabled: !!menu.target && menu.canCopy(menu.target)
        onTriggered: menu.copyRequested(menu.target.richText)
    }
    PopupMenu.Entry {
        text: "React"
        iconName: "add_reaction"
        enabled: !!menu.target && menu.canReact(menu.target)
        onTriggered: menu.reactRequested(menu.target.msgId, menu.myReaction(menu.target))
    }
    PopupMenu.Entry {
        text: "Copy image"
        iconName: "image"
        enabled: !!menu.target && menu.canCopyImage(menu.target)
        onTriggered: Conversation.copyImage(menu.target.msgId)
    }
    PopupMenu.Entry {
        text: menu.target && menu.target.mediaSource !== "" ? "Open" : "Download"
        iconName: text === "Open" ? "open_in_new" : "download"
        enabled: !!menu.target && menu.canOpen(menu.target)
        onTriggered: Conversation.openMedia(menu.target.msgId)
    }
    PopupMenu.Entry {
        text: "Edit"
        iconName: "edit"
        enabled: !!menu.target && Conversation.canEdit(menu.target.msgId)
        onTriggered: menu.editRequested(menu.target.msgId, menu.target.richText, menu.target.timeText)
    }
    PopupMenu.Entry {
        text: "Delete for me"
        iconName: "delete"
        enabled: !!menu.target && (!menu.target.fromMe || menu.target.status !== "pending")
        onTriggered: menu.deleteRequested(menu.target.msgId, false)
    }
    PopupMenu.Entry {
        text: "Delete for everyone"
        iconName: "delete"
        enabled: !!menu.target && menu.canDeleteForEveryone(menu.target)
        onTriggered: menu.deleteRequested(menu.target.msgId, true)
    }
}
