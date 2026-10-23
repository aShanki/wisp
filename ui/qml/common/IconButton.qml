// A round icon button, as in WhatsApp's headers.
import QtQuick
import Wisp

Item {
    id: button

    property alias name: icon.name
    property alias color: icon.color
    property bool checked: false

    signal clicked()

    width: 40
    height: 40

    Rectangle {
        anchors.fill: parent
        radius: width / 2
        color: button.checked || mouse.pressed ? Theme.selected : Theme.hover
        visible: button.checked || mouse.containsMouse
    }
    Icon {
        id: icon
        anchors.centerIn: parent
    }
    MouseArea {
        id: mouse
        anchors.fill: parent
        hoverEnabled: true
        onClicked: button.clicked()
    }
}
