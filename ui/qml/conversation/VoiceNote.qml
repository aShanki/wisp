// A voice note laid out as in WhatsApp Web: the play button, the waveform
// (a plain track when there is none) with the length under it, and the
// sender's photo with a mic badge, right of theirs and left of ours. Green
// until played, then blue; grey on ours until played. A click plays it in
// place, downloading it first if needed, and pauses it; a click or drag on
// the track seeks.
import QtQuick
import QtMultimedia
import Wisp

Item {
    id: root

    property string msgId
    property string sender
    property bool fromMe
    property bool played
    property int duration
    property bool loading
    property var levels
    property url mediaUrl // the local file, once downloaded
    property color fill // the bubble's

    readonly property bool hasLevels: levels !== undefined && levels.byteLength > 0
    readonly property real playerX: fromMe ? 70 : 8
    readonly property real trackX: playerX + 46
    readonly property real trackEnd: fromMe ? width - 8 : avatar.x - 19
    readonly property bool playing: player.playbackState === MediaPlayer.PlayingState
    readonly property real progress: player.duration > 0 ? player.position / player.duration : 0
    property string pendingMsgId // clicked while downloading: play once it lands

    implicitWidth: 324
    implicitHeight: 55
    clip: true // the photo's rounding ring

    Avatar {
        id: avatar
        x: root.fromMe ? 4 : root.width - 59
        size: 55
        source: root.sender !== "" ? "image://wisp/avatar/" + root.sender : ""
        maskColor: root.fill
    }
    Rectangle {
        x: avatar.x + (root.fromMe ? 34 : 1)
        y: 37
        width: 20
        height: 20
        radius: 10
        color: root.fill
        Icon {
            anchors.centerIn: parent
            name: "mic_filled"
            size: 16
            color: root.played ? (root.fromMe ? Theme.voicePlayedMine : Theme.voicePlayed)
                : root.fromMe ? Theme.voiceMicMine : Theme.voiceNew
        }
    }

    MediaPlayer {
        id: player
        source: root.mediaUrl
        audioOutput: AudioOutput {}
        onMediaStatusChanged: if (mediaStatus === MediaPlayer.EndOfMedia) stop()
    }
    function resetPlayback() {
        pendingMsgId = ""
        player.stop()
    }
    onMsgIdChanged: resetPlayback()
    onMediaUrlChanged: Qt.callLater(function() {
        if (pendingMsgId !== "" && pendingMsgId === msgId && mediaUrl.toString() !== "") {
            pendingMsgId = ""
            player.play()
        }
    })

    Icon {
        x: root.playerX + 5
        y: 12.5
        name: root.playing ? "pause" : "play_arrow"
        color: Theme.text
    }
    Waveform {
        visible: root.hasLevels
        x: root.trackX
        y: 13
        width: root.trackEnd - x
        height: 24
        levels: root.levels
        barColor: root.fromMe ? Theme.voiceBarMine : Theme.voiceBar
        thumbColor: root.played ? Theme.voicePlayed : root.fromMe ? Theme.voiceThumbMine : Theme.voiceNew
        progress: root.progress
    }
    Item {
        visible: !root.hasLevels
        x: root.trackX
        y: 18
        width: root.trackEnd - x
        height: 13
        Rectangle {
            y: 5
            width: parent.width
            height: 3
            radius: 2
            color: root.fromMe ? Theme.voiceTrackMine : Theme.voiceTrack
        }
        Rectangle {
            x: root.progress * (parent.width - width)
            width: 13
            height: 13
            radius: 6.5
            color: root.played ? (root.fromMe ? Theme.voicePlayedMine : Theme.voicePlayedTrack)
                : root.fromMe ? Theme.voiceTrackThumbMine : Theme.voiceNewTrack
        }
    }
    Text {
        x: root.trackX
        y: 43
        height: 15
        verticalAlignment: Text.AlignVCenter
        text: root.loading ? "Loading…"
            : Format.duration(root.playing || player.position > 0 ? Math.floor(player.position / 1000) : root.duration)
        color: Theme.bubbleMeta
        font.pixelSize: 11
    }

    function toggle() {
        if (playing)
            player.pause()
        else if (mediaUrl.toString() !== "")
            player.play()
        else {
            pendingMsgId = msgId
            Conversation.fetchMedia(msgId)
        }
    }

    MouseArea {
        anchors.fill: parent
        cursorShape: Qt.PointingHandCursor
        onClicked: root.toggle()
    }
    MouseArea { // the track: seeks once the file is loaded, else plays
        x: root.trackX - 6
        y: 6
        width: root.trackEnd - root.trackX + 12
        height: 37
        cursorShape: Qt.PointingHandCursor
        preventStealing: true
        function seek(mx: real) {
            player.position = Math.max(0, Math.min(1, (mx - 6) / (width - 12))) * player.duration
        }
        onPressed: mouse => player.duration > 0 ? seek(mouse.x) : root.toggle()
        onPositionChanged: mouse => { if (pressed && player.duration > 0) seek(mouse.x) }
    }
}
