// WhatsApp's rounded button: green when primary, outlined otherwise.
import QtQuick
import Wisp

Rectangle {
    id: button

    property alias text: label.text
    property bool primary: false

    signal clicked()

    implicitWidth: label.implicitWidth + 48
    implicitHeight: 40
    radius: height / 2
    opacity: enabled ? 1 : 0.5
    color: primary ? (mouse.containsMouse ? Qt.darker(Theme.accent, 1.08) : Theme.accent)
         : mouse.containsMouse ? Theme.hover : "transparent"
    border.width: primary ? 0 : 1
    border.color: Theme.divider

    Text {
        id: label
        anchors.centerIn: parent
        color: button.primary ? Theme.accentText : Theme.accentStrong
        font.pixelSize: 14
        font.weight: Font.Medium
    }
    MouseArea {
        id: mouse
        anchors.fill: parent
        hoverEnabled: true
        cursorShape: Qt.PointingHandCursor
        onClicked: button.clicked()
    }
}
