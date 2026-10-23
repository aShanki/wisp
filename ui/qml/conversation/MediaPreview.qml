pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import Wisp

FocusScope {
    id: root
    objectName: "mediaPreview"
    property int currentIndex: 0
    property var current: ({})
    property bool restoring: false
    signal sent()
    signal addRequested()

    function select(index) {
        if (index < 0 || index >= Conversation.attachments.count)
            return
        currentIndex = index
        current = Conversation.attachments.get(index)
        restoring = true
        caption.text = current.caption
        caption.cursorPosition = caption.length
        restoring = false
        caption.forceActiveFocus()
    }
    function send() {
        sent()
        Conversation.sendAttachments()
    }

    Component.onCompleted: select(0)
    Keys.onEscapePressed: Conversation.attachments.clear()
    Connections {
        target: Conversation.attachments
        function onCountChanged() {
            if (Conversation.attachments.count > 0)
                root.select(Math.min(root.currentIndex, Conversation.attachments.count - 1))
        }
    }

    Rectangle {
        anchors.fill: parent
        color: Theme.panel
    }

    MouseArea {
        anchors.fill: parent
        acceptedButtons: Qt.AllButtons
        onWheel: event => event.accepted = true
    }

    IconButton {
        objectName: "cancelAttachments"
        x: 16
        y: 12
        name: "close"
        onClicked: Conversation.attachments.clear()
    }

    Item {
        id: stage
        x: 24
        y: Theme.headerHeight
        width: parent.width - 48
        height: captionField.y - y - 24
        Image {
            objectName: "attachmentImage"
            anchors.centerIn: parent
            width: Math.min(parent.width, root.current.imageWidth || 0)
            height: Math.min(parent.height, root.current.imageHeight || 0)
            autoTransform: true
            source: root.current.isImage ? root.current.source : ""
            sourceSize: Qt.size(width, height)
            fillMode: Image.PreserveAspectFit
            asynchronous: true
            cache: false
        }
        Column {
            visible: !root.current.isImage
            anchors.centerIn: parent
            width: parent.width
            spacing: 16
            Icon {
                anchors.horizontalCenter: parent.horizontalCenter
                name: "description"
                size: 80
            }
            Text {
                width: parent.width
                text: root.current.fileName || ""
                textFormat: Text.PlainText
                color: Theme.text
                font.pixelSize: 16
                horizontalAlignment: Text.AlignHCenter
                elide: Text.ElideMiddle
            }
        }
    }

    Rectangle {
        id: captionField
        objectName: "captionField"
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.bottom: footer.top
        anchors.bottomMargin: 24
        width: Math.min(parent.width - 48, 720)
        height: Math.min(Math.max(48, caption.implicitHeight), 104)
        radius: 8
        color: Theme.field

        ScrollView {
            anchors.fill: parent
            ScrollBar.horizontal.policy: ScrollBar.AlwaysOff
            TextArea {
                id: caption
                objectName: "attachmentCaption"
                placeholderText: "Add a caption"
                placeholderTextColor: Theme.textMuted
                textFormat: TextEdit.PlainText
                color: Theme.text
                font.pixelSize: 15
                wrapMode: TextEdit.Wrap
                selectByMouse: true
                persistentSelection: true
                selectionColor: Theme.highlight
                selectedTextColor: Theme.text
                ContextMenu.menu: null
                ContextMenu.onRequested: position => captionMenu.openAt(position.x, position.y)
                padding: 14
                background: null
                onTextChanged: if (!root.restoring) Conversation.attachments.setCaption(root.currentIndex, text)
                Keys.forwardTo: [suggest]
                Keys.onPressed: event => {
                    if ((event.key === Qt.Key_Return || event.key === Qt.Key_Enter)
                            && !(event.modifiers & Qt.ShiftModifier) && !inputMethodComposing) {
                        root.send()
                        event.accepted = true
                    } else if (event.matches(StandardKey.Paste)) {
                        event.accepted = Conversation.stageClipboardImage()
                    }
                }
                TextMenu {
                    id: captionMenu
                    editor: caption
                    paste: () => {
                        if (!Conversation.stageClipboardImage())
                            caption.paste()
                    }
                }
            }
        }
    }

    EmojiSuggest {
        id: suggest
        input: caption
        x: captionField.x
        anchors.bottom: captionField.top
        anchors.bottomMargin: 8
        maxHeight: stage.height
    }

    Rectangle {
        id: footer
        anchors.bottom: parent.bottom
        width: parent.width
        height: 100
        color: Theme.field
        Rectangle {
            width: parent.width
            height: 1
            color: Theme.divider
        }
        ListView {
            id: thumbnails
            objectName: "attachmentThumbnails"
            x: Math.max(16, (parent.width - contentWidth - 100) / 2)
            anchors.verticalCenter: parent.verticalCenter
            width: Math.min(contentWidth, parent.width - 116)
            height: 64
            orientation: ListView.Horizontal
            spacing: 8
            clip: true
            model: Conversation.attachments
            currentIndex: root.currentIndex
            highlightRangeMode: ListView.ApplyRange
            delegate: Rectangle {
                id: thumb
                required property int index
                required property url source
                required property bool isImage
                width: 64
                height: 64
                radius: 6
                color: Theme.panel
                border.width: 3
                border.color: index === root.currentIndex ? Theme.accent : Theme.divider
                Image {
                    anchors.fill: parent
                    anchors.margins: 4
                    source: thumb.isImage ? thumb.source : ""
                    sourceSize: Qt.size(56, 56)
                    autoTransform: true
                    fillMode: Image.PreserveAspectCrop
                    asynchronous: true
                    cache: false
                }
                Icon {
                    visible: !thumb.isImage
                    anchors.centerIn: parent
                    name: "description"
                    size: 28
                }
                MouseArea {
                    id: hover
                    anchors.fill: parent
                    hoverEnabled: true
                    cursorShape: Qt.PointingHandCursor
                    onClicked: root.select(thumb.index)
                }
                Rectangle {
                    visible: hover.containsMouse || closeHover.containsMouse
                    anchors.right: parent.right
                    width: 22
                    height: 22
                    radius: 11
                    color: "#54656f"
                    Icon { anchors.centerIn: parent; name: "close"; size: 16; color: "white" }
                    MouseArea {
                        id: closeHover
                        anchors.fill: parent
                        hoverEnabled: true
                        cursorShape: Qt.PointingHandCursor
                        onClicked: Conversation.attachments.remove(thumb.index)
                    }
                }
            }
            footer: IconButton {
                objectName: "addAttachment"
                width: 64
                height: 64
                name: "add"
                onClicked: root.addRequested()
            }
        }
        Rectangle {
            objectName: "sendAttachments"
            anchors.right: parent.right
            anchors.rightMargin: 24
            anchors.verticalCenter: parent.verticalCenter
            width: 60
            height: 60
            radius: 30
            color: Theme.accent
            Icon { anchors.centerIn: parent; name: "send"; size: 28; color: Theme.accentText }
            MouseArea {
                anchors.fill: parent
                cursorShape: Qt.PointingHandCursor
                onClicked: root.send()
            }
        }
    }
}
