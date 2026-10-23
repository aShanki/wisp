// The open chat's header, as in WhatsApp: avatar, name and status on the
// left; a voice-call button (1:1 chats, when the daemon offers calls) and the
// menu on the right.
import QtQuick
import Wisp

Rectangle {
    id: root
    implicitHeight: Theme.headerHeight
    color: Theme.panel

    Avatar {
        id: avatar
        x: 16
        anchors.verticalCenter: parent.verticalCenter
        size: 40
        name: Conversation.name
        group: Conversation.isGroup
        source: Conversation.jid !== "" ? "image://wisp/avatar/" + Conversation.jid : ""
    }

    Column {
        anchors.left: avatar.right
        anchors.leftMargin: 12
        anchors.right: actions.left
        anchors.rightMargin: 12
        anchors.verticalCenter: parent.verticalCenter
        Text {
            objectName: "chatName"
            width: parent.width
            text: Conversation.name
            textFormat: Text.PlainText
            color: Theme.text
            font.pixelSize: Theme.fontSizeLarge
            font.weight: Font.Medium
            elide: Text.ElideRight
        }
        Text {
            width: parent.width
            visible: text !== ""
            text: Conversation.subtitle
            textFormat: Text.PlainText
            color: Theme.textMuted
            font.pixelSize: 13
            elide: Text.ElideRight
        }
    }

    Row {
        id: actions
        anchors.right: parent.right
        anchors.rightMargin: 12
        anchors.verticalCenter: parent.verticalCenter
        spacing: 4
        IconButton {
            visible: Daemon.callsAvailable && !Conversation.isGroup
            name: "call"
            onClicked: Daemon.call("calls.start", {chat: Conversation.jid})
        }
        IconButton {
            id: menuButton
            objectName: "chatMenuButton"
            name: "more_vert"
            checked: chatMenu.visible
            onClicked: chatMenu.openFrom(menuButton, false, true)
        }
    }

    PopupMenu {
        id: chatMenu
        objectName: "chatMenu"
        PopupMenu.Entry {
            text: Conversation.muted ? "Unmute notifications" : "Mute notifications"
            onTriggered: ChatList.setMuted(Conversation.jid, !Conversation.muted)
        }
        PopupMenu.Entry {
            text: "Close chat"
            onTriggered: Conversation.close()
        }
    }

    Rectangle {
        anchors.bottom: parent.bottom
        width: parent.width
        height: 1
        color: Theme.divider
    }
}
