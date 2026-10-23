// WhatsApp's confirm dialog: a message, Cancel and one action.
import QtQuick
import QtQuick.Controls
import Wisp

Dialog {
    id: sheet

    property alias text: label.text
    property string acceptText: "OK"

    modal: true
    anchors.centerIn: Overlay.overlay
    width: 440
    padding: 24
    topPadding: 24
    bottomPadding: 20

    background: Rectangle {
        color: Theme.elevated
        radius: 24
    }
    Overlay.modal: Rectangle {
        color: Theme.dark ? Qt.rgba(0, 0, 0, 0.6) : Qt.rgba(1, 1, 1, 0.75)
    }

    contentItem: Column {
        spacing: 28

        Text {
            id: label
            width: parent.width
            wrapMode: Text.Wrap
            color: Theme.textMuted
            font.pixelSize: 15
        }
        Row {
            anchors.right: parent.right
            spacing: 12

            PillButton {
                text: "Cancel"
                onClicked: sheet.reject()
            }
            PillButton {
                text: sheet.acceptText
                primary: true
                onClicked: sheet.accept()
            }
        }
    }
}
