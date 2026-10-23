// WhatsApp's rounded field, with an optional leading icon.
import QtQuick
import QtQuick.Controls
import Wisp

TextField {
    id: field

    property string iconName

    implicitHeight: 40
    leftPadding: iconName !== "" ? 44 : 16
    rightPadding: 16
    color: Theme.text
    placeholderTextColor: Theme.textMuted
    selectionColor: Theme.highlight
    selectedTextColor: Theme.text
    selectByMouse: true
    font.pixelSize: 15

    background: Rectangle {
        radius: height / 2
        color: Theme.field
        border.width: field.activeFocus ? 2 : 0
        border.color: Theme.accent

        Icon {
            visible: field.iconName !== ""
            x: 14
            anchors.verticalCenter: parent.verticalCenter
            size: 20
            name: field.iconName
            color: Theme.textMuted
        }
    }
}
