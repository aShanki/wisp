// WhatsApp's left navigation bar: Chats at the top; Settings and the profile
// photo at the bottom.
import QtQuick
import Wisp

Rectangle {
    id: rail

    property string selfJid
    property string selfName
    property bool settings: false

    signal chatsClicked()
    signal settingsClicked()

    width: Theme.railWidth
    color: Theme.rail

    IconButton {
        objectName: "navChats"
        anchors.horizontalCenter: parent.horizontalCenter
        y: 12
        name: rail.settings ? "chat" : "chat_filled"
        checked: !rail.settings
        color: rail.settings ? Theme.icon : Theme.text
        onClicked: rail.chatsClicked()
    }

    Column {
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.bottom: parent.bottom
        anchors.bottomMargin: 12
        spacing: 12

        IconButton {
            objectName: "navSettings"
            name: rail.settings ? "settings_filled" : "settings"
            checked: rail.settings
            color: rail.settings ? Theme.text : Theme.icon
            onClicked: rail.settingsClicked()
        }
        Item {
            width: 40
            height: 40

            Avatar {
                anchors.centerIn: parent
                size: 30
                name: rail.selfName
                source: rail.selfJid !== "" ? "image://wisp/avatar/" + rail.selfJid : ""
                maskColor: Theme.rail
            }
            MouseArea {
                anchors.fill: parent
                cursorShape: Qt.PointingHandCursor
                onClicked: rail.settingsClicked()
            }
        }
    }

    Rectangle {
        anchors.right: parent.right
        width: 1
        height: parent.height
        color: Theme.divider
    }
}
