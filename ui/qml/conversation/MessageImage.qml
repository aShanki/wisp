// An image in a bubble: the thumbnail, blurred by upscaling, until the full
// image (decoded at display size) is ready. A ring in the bubble colour
// rounds the corners without layers. Videos and GIFs show a play mark,
// which turns into a filling ring with the percentage while downloading.
// GIFs download on sight and loop silently in place, with no play mark.
pragma ComponentBehavior: Bound
import QtQuick
import QtMultimedia
import Wisp

Item {
    id: root
    property string thumbSource
    property string mediaSource
    property bool playable
    property bool gif
    property url videoUrl // the local file, once downloaded
    property int duration
    property bool loading
    property int progress: -1 // download percent, -1 unknown
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
    Loader {
        active: root.gif && root.videoUrl.toString() !== ""
        anchors.fill: parent
        sourceComponent: VideoSurface {
            id: surface
            MediaPlayer {
                source: root.videoUrl
                videoOutput: surface.videoSink
                loops: MediaPlayer.Infinite
                Component.onCompleted: play()
            }
        }
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
        visible: root.playable && (!root.gif || root.loading)
        anchors.centerIn: parent
        width: 56
        height: 56
        radius: 28
        color: Qt.rgba(0, 0, 0, 0.4)
        Icon {
            anchors.centerIn: parent
            name: parent.visible && !root.loading ? "play_arrow" : ""
            size: 36
            color: "white"
        }
        Canvas {
            id: ring
            visible: root.loading
            anchors.fill: parent
            readonly property real fraction: Math.max(0, root.progress) / 100
            onFractionChanged: requestPaint()
            onVisibleChanged: requestPaint()
            onPaint: {
                const ctx = getContext("2d")
                ctx.reset()
                ctx.lineWidth = 3
                ctx.beginPath()
                ctx.strokeStyle = Qt.rgba(1, 1, 1, 0.3)
                ctx.arc(width / 2, height / 2, width / 2 - 4, 0, 2 * Math.PI)
                ctx.stroke()
                if (fraction > 0) {
                    ctx.beginPath()
                    ctx.strokeStyle = "white"
                    ctx.arc(width / 2, height / 2, width / 2 - 4, -Math.PI / 2, -Math.PI / 2 + 2 * Math.PI * fraction)
                    ctx.stroke()
                }
            }
        }
        Text {
            visible: root.loading
            anchors.centerIn: parent
            text: root.progress >= 0 ? root.progress + "%" : ""
            color: "white"
            font.pixelSize: 13
            font.weight: Font.Medium
        }
    }
    Text {
        visible: root.playable && !root.gif && root.duration > 0 || root.loading
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
