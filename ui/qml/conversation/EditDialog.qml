// WhatsApp Web's "Edit message": the message on the wallpaper, and a field
// below holding its text with a round check to save. Enter saves,
// Shift+Enter adds a line, Escape cancels.
import QtQuick
import QtQuick.Controls
import QtQuick.Templates as T
import Wisp

T.Popup {
    id: dialog
    objectName: "editDialog"

    property string msgId
    property string richText
    property string timeText

    function show(id, rich, time) {
        msgId = id
        richText = rich
        timeText = time
        input.text = Conversation.editableText(id)
        input.cursorPosition = input.length
        open()
        input.forceActiveFocus()
    }
    function save() {
        const t = input.text.trim()
        if (t !== "")
            Conversation.edit(msgId, t)
        close()
    }

    parent: T.Overlay.overlay
    popupType: T.Popup.Item
    modal: true
    focus: true
    anchors.centerIn: parent
    width: Math.min(560, parent.width - 32)
    padding: 0
    closePolicy: T.Popup.CloseOnEscape | T.Popup.CloseOnPressOutside
    implicitHeight: implicitContentHeight

    T.Overlay.modal: Rectangle {
        color: Theme.dark ? Qt.rgba(0, 0, 0, 0.6) : Qt.rgba(1, 1, 1, 0.75)
    }
    background: Rectangle {
        color: Theme.panel
        radius: 16
    }

    contentItem: Column {
        // Header: close and the title.
        Item {
            width: parent.width
            height: 60
            IconButton {
                objectName: "editClose"
                x: 12
                anchors.verticalCenter: parent.verticalCenter
                name: "close"
                color: Theme.text
                onClicked: dialog.close()
            }
            Text {
                x: 64
                anchors.verticalCenter: parent.verticalCenter
                text: "Edit message"
                color: Theme.text
                font.pixelSize: Theme.fontSizeLarge
                font.weight: Font.Medium
            }
        }

        // The message as sent, on the chat's wallpaper.
        Rectangle {
            width: parent.width
            height: Math.min(260, bubble.height + 48)
            color: Theme.conversation
            clip: true
            Image {
                anchors.fill: parent
                visible: status === Image.Ready
                source: Theme.wallpaper
                fillMode: Image.Tile
                asynchronous: true
            }
            Rectangle {
                id: bubble
                anchors.right: parent.right
                anchors.rightMargin: 24
                anchors.bottom: parent.bottom
                anchors.bottomMargin: 24
                width: Math.min(body.implicitWidth + time.implicitWidth + 30, dialog.width * 0.75)
                height: body.height + 14
                radius: 8
                topRightRadius: 0
                color: Theme.bubbleOut
                Text {
                    id: body
                    x: 9
                    y: 6
                    width: parent.width - 18
                    text: dialog.richText
                    textFormat: Text.RichText
                    wrapMode: Text.Wrap
                    color: Theme.text
                    font.pixelSize: Theme.fontSize + 1
                    rightPadding: time.implicitWidth + 6
                }
                Text {
                    id: time
                    anchors.right: parent.right
                    anchors.rightMargin: 8
                    anchors.bottom: parent.bottom
                    anchors.bottomMargin: 5
                    text: dialog.timeText
                    color: Theme.bubbleMeta
                    font.pixelSize: 11
                }
            }
        }

        // The new text and the check to save it.
        Item {
            width: parent.width
            height: field.height + 24
            Rectangle {
                id: field
                x: 12
                y: 12
                width: parent.width - 24 - save.width - 8
                height: Math.min(input.implicitHeight, 160) + 12
                radius: 24
                color: Theme.composer
                border.width: Theme.dark ? 0 : 1
                border.color: Theme.divider
                ScrollView {
                    anchors.fill: parent
                    anchors.leftMargin: 18
                    anchors.rightMargin: 8
                    anchors.topMargin: 6
                    anchors.bottomMargin: 6
                    ScrollBar.horizontal.policy: ScrollBar.AlwaysOff
                    TextArea {
                        id: input
                        objectName: "editInput"
                        color: Theme.text
                        wrapMode: TextEdit.Wrap
                        textFormat: TextEdit.PlainText
                        font.pixelSize: 15
                        selectByMouse: true
                        selectionColor: Theme.highlight
                        selectedTextColor: Theme.text
                        leftPadding: 0
                        rightPadding: 0
                        topPadding: Math.max(0, (36 - contentHeight) / 2)
                        bottomPadding: topPadding
                        background: null
                        Keys.onPressed: event => {
                            if ((event.key === Qt.Key_Return || event.key === Qt.Key_Enter)
                                    && !(event.modifiers & Qt.ShiftModifier) && !input.inputMethodComposing) {
                                dialog.save()
                                event.accepted = true
                            }
                        }
                    }
                }
            }
            Rectangle {
                id: save
                objectName: "editSave"
                anchors.right: parent.right
                anchors.rightMargin: 12
                anchors.bottom: field.bottom
                width: 48
                height: 48
                radius: 24
                readonly property bool ready: input.text.trim() !== ""
                color: ready ? Theme.accent : Theme.hover
                Icon {
                    anchors.centerIn: parent
                    name: "done"
                    color: save.ready ? Theme.accentText : Theme.textMuted
                }
                MouseArea {
                    anchors.fill: parent
                    enabled: save.ready
                    cursorShape: Qt.PointingHandCursor
                    onClicked: dialog.save()
                }
            }
        }
    }
}
