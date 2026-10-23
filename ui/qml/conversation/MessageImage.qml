// An image in a bubble: the thumbnail, blurred by upscaling, until the full
// image (decoded at display size) is ready. A ring in the bubble colour
// rounds the corners without layers. Videos and GIFs show a play mark.
import QtQuick
import Wisp

Item {
    id: root
    property string thumbSource
    property string mediaSource
    property bool playable
    property int duration
    property bool loading
    property bool sticker
    property color maskColor
    readonly property bool failed: full.status === Image.Error
    signal clicked()

    Rectangle {
        anchors.fill: parent
        visible: !root.sticker && full.status !== Image.Ready && thumb.status !== Image.Ready
        color: Theme.quote
    }
    Image {
        id: thumb
        anchors.fill: parent
        visible: full.status !== Image.Ready
        source: root.thumbSource
        sourceSize: Qt.size(48, 48)
        fillMode: root.sticker ? Image.PreserveAspectFit : Image.PreserveAspectCrop
        asynchronous: true
    }
    Image {
        id: full
        anchors.fill: parent
        source: root.mediaSource
        sourceSize: Qt.size(width, height)
        fillMode: root.sticker ? Image.PreserveAspectFit : Image.PreserveAspectCrop
        asynchronous: true
    }
    Rectangle {
        visible: !root.sticker
        anchors.fill: parent
        anchors.margins: -3
        radius: 9
        color: "transparent"
        border.width: 3
        border.color: root.maskColor
    }
    Rectangle {
        visible: root.playable
        anchors.centerIn: parent
        width: 56
        height: 56
        radius: 28
        color: Qt.rgba(0, 0, 0, 0.4)
        Icon {
            anchors.centerIn: parent
            name: parent.visible ? "play_arrow" : ""
            size: 36
            color: "white"
        }
    }
    Text {
        visible: root.playable && root.duration > 0 || root.loading
        x: 8
        y: parent.height - height - 6
        text: root.loading ? "Loading…" : Format.duration(root.duration)
        color: "white"
        font.pixelSize: 11
    }
    MouseArea {
        anchors.fill: parent
        cursorShape: Qt.PointingHandCursor
        onClicked: root.clicked()
    }
}
