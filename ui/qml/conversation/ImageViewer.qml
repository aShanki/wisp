// Full-window image view. Esc or a click closes it; a right click offers to
// copy the image.
import QtQuick
import QtQuick.Controls
import Wisp

Popup {
    id: root
    property string msgId
    property string source
    property int imageWidth
    property int imageHeight

    function show(id, src, w, h) {
        msgId = id
        source = src
        imageWidth = w
        imageHeight = h
        open()
    }

    parent: Overlay.overlay
    width: parent ? parent.width : 0
    height: parent ? parent.height : 0
    padding: 0
    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape
    onClosed: {
        menu.close()
        source = ""
        msgId = ""
    }
    background: Rectangle { color: Qt.rgba(0, 0, 0, 0.85) }

    contentItem: MouseArea {
        id: area
        acceptedButtons: Qt.LeftButton | Qt.RightButton
        onClicked: mouse => {
            if (mouse.button === Qt.RightButton && image.contains(area.mapToItem(image, mouse.x, mouse.y)))
                menu.openAt(area, mouse.x, mouse.y)
            else if (mouse.button === Qt.LeftButton)
                root.close()
        }
        Image {
            id: image
            readonly property real fit: Math.min(1, (parent.width - 64) / Math.max(1, root.imageWidth),
                                                 (parent.height - 64) / Math.max(1, root.imageHeight))
            anchors.centerIn: parent
            width: Math.round(root.imageWidth * fit)
            height: Math.round(root.imageHeight * fit)
            source: root.source
            sourceSize: Qt.size(width, height)
            fillMode: Image.PreserveAspectFit
            asynchronous: true
        }
    }

    PopupMenu {
        id: menu
        objectName: "imageMenu"
        PopupMenu.Entry {
            text: "Copy image"
            onTriggered: Conversation.copyImage(root.msgId)
        }
        PopupMenu.Entry {
            text: "Open"
            onTriggered: Conversation.openMedia(root.msgId)
        }
    }
}
