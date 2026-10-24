// Full-window video player. A click or Space plays and pauses, the bar
// below seeks, Esc closes. GIFs loop silently. A right click offers to open
// the file in another app.
import QtQuick
import QtQuick.Controls
import QtMultimedia
import Wisp

Popup {
    id: root
    property string msgId
    property bool gif

    function show(id, url, isGif) {
        msgId = id
        gif = isGif
        player.source = url
        open()
        player.play()
    }

    function toggle() {
        if (player.playbackState === MediaPlayer.PlayingState)
            player.pause()
        else
            player.play()
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
        player.stop()
        player.source = ""
        msgId = ""
    }
    background: Rectangle { color: Qt.rgba(0, 0, 0, 0.9) }

    MediaPlayer {
        id: player
        objectName: "videoPlayer"
        videoOutput: video.videoSink
        audioOutput: AudioOutput { muted: root.gif }
        loops: root.gif ? MediaPlayer.Infinite : 1
        onErrorOccurred: (error, message) => Daemon.errorOccurred("Could not play the video")
    }

    contentItem: Item {
        focus: true
        Keys.onSpacePressed: root.toggle()
        Keys.onLeftPressed: player.position = Math.max(0, player.position - 5000)
        Keys.onRightPressed: player.position = Math.min(player.duration, player.position + 5000)

        VideoSurface {
            id: video
            anchors.fill: parent
            anchors.margins: 32
            anchors.bottomMargin: root.gif ? 32 : 72
        }
        MouseArea {
            id: area
            anchors.fill: parent
            acceptedButtons: Qt.LeftButton | Qt.RightButton
            onClicked: mouse => {
                const r = video.contentRect
                const p = area.mapToItem(video, mouse.x, mouse.y)
                const inside = p.x >= r.x && p.x <= r.x + r.width && p.y >= r.y && p.y <= r.y + r.height
                if (mouse.button === Qt.RightButton && inside)
                    menu.openAt(area, mouse.x, mouse.y)
                else if (mouse.button === Qt.LeftButton)
                    inside ? root.toggle() : root.close()
            }
        }
        Rectangle { // play mark while paused
            visible: player.playbackState !== MediaPlayer.PlayingState && !root.gif
            anchors.centerIn: video
            width: 72
            height: 72
            radius: 36
            color: Qt.rgba(0, 0, 0, 0.5)
            Icon {
                anchors.centerIn: parent
                name: parent.visible ? "play_arrow" : ""
                size: 48
                color: "white"
            }
        }

        Item { // controls
            visible: !root.gif
            x: 32
            width: parent.width - 64
            y: parent.height - 56
            height: 40

            MouseArea {
                id: playButton
                width: 40
                height: 40
                cursorShape: Qt.PointingHandCursor
                onClicked: root.toggle()
                Icon {
                    anchors.centerIn: parent
                    name: player.playbackState === MediaPlayer.PlayingState ? "pause" : "play_arrow"
                    size: 28
                    color: "white"
                }
            }
            Text {
                id: time
                anchors.right: parent.right
                anchors.verticalCenter: parent.verticalCenter
                text: Format.duration(Math.floor(player.position / 1000)) + " / "
                      + Format.duration(Math.floor(player.duration / 1000))
                color: "white"
                font.pixelSize: 13
            }
            MouseArea { // seek bar
                id: seek
                anchors.left: playButton.right
                anchors.leftMargin: 8
                anchors.right: time.left
                anchors.rightMargin: 12
                height: parent.height
                cursorShape: Qt.PointingHandCursor
                function seekTo(x) {
                    if (player.seekable && player.duration > 0)
                        player.position = Math.max(0, Math.min(1, x / width)) * player.duration
                }
                onPressed: mouse => seekTo(mouse.x)
                onPositionChanged: mouse => { if (pressed) seekTo(mouse.x) }
                Rectangle {
                    anchors.verticalCenter: parent.verticalCenter
                    width: parent.width
                    height: 4
                    radius: 2
                    color: Qt.rgba(1, 1, 1, 0.3)
                    Rectangle {
                        width: player.duration > 0 ? parent.width * player.position / player.duration : 0
                        height: parent.height
                        radius: 2
                        color: Theme.accent
                    }
                }
            }
        }
    }

    PopupMenu {
        id: menu
        objectName: "videoMenu"
        PopupMenu.Entry {
            text: "Open in another app"
            onTriggered: {
                player.pause()
                Conversation.openMedia(root.msgId)
            }
        }
    }
}
