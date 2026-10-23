// Full-window image view. Esc or a click closes it.
import QtQuick
import QtQuick.Controls

Popup {
    id: root
    property string source
    property int imageWidth
    property int imageHeight

    function show(src, w, h) {
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
    onClosed: source = ""
    background: Rectangle { color: Qt.rgba(0, 0, 0, 0.85) }

    contentItem: MouseArea {
        onClicked: root.close()
        Image {
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
}
