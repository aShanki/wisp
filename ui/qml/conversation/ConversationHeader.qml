// The open chat's header, as in WhatsApp: avatar, name and status on the
// left; a voice-call button (1:1 chats, when the daemon offers calls) and the
// menu on the right. Its shadow is drawn by ConversationView, over the messages.
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

    // WhatsApp Web's title block: a 21 px name line and a 20 px status line,
    // centred together, 15 px after the avatar.
    Text {
        id: title
        objectName: "headerName"
        x: avatar.x + avatar.width + 15
        y: subtitle.visible ? 11 : 21
        width: actions.x - 16 - x
        height: 21
        verticalAlignment: Text.AlignVCenter
        text: Conversation.name
        textFormat: Text.PlainText
        color: Theme.text
        font.pixelSize: Theme.fontSizeLarge
        font.weight: Theme.dark ? Font.DemiBold : Font.Normal
        elide: Text.ElideRight
    }
    Text {
        id: subtitle
        objectName: "chatSubtitle"
        x: title.x
        y: title.y + 21
        width: title.width
        height: 20
        verticalAlignment: Text.AlignVCenter
        visible: text !== ""
        text: Conversation.subtitle
        textFormat: Text.PlainText
        color: Theme.textMuted
        font.pixelSize: 13
        elide: Text.ElideRight
    }

    Row {
        id: actions
        anchors.right: parent.right
        anchors.rightMargin: 16
        anchors.verticalCenter: parent.verticalCenter
        spacing: 16
        IconButton {
            visible: Daemon.callsAvailable && !Conversation.isGroup
            name: "call"
            color: Theme.text
            onClicked: Daemon.call("calls.start", {chat: Conversation.jid})
        }
        IconButton {
            id: menuButton
            objectName: "chatMenuButton"
            name: "more_vert"
            color: Theme.text
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

}
