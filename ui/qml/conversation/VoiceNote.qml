// A voice note laid out as in WhatsApp Web: the play button, the waveform
// (a plain track when there is none) with the length under it, and the
// sender's photo with a mic badge, right of theirs and left of ours. Green
// until played, then blue; grey on ours until played. A click opens it.
import QtQuick
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
    property color fill // the bubble's

    readonly property bool hasLevels: levels !== undefined && levels.byteLength > 0
    readonly property real playerX: fromMe ? 70 : 8
    readonly property real trackX: playerX + 46
    readonly property real trackEnd: fromMe ? width - 8 : avatar.x - 19

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

    Icon {
        x: root.playerX + 5
        y: 12.5
        name: "play_arrow"
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
        text: root.loading ? "Loading…" : Format.duration(root.duration)
        color: Theme.bubbleMeta
        font.pixelSize: 11
    }

    MouseArea {
        anchors.fill: parent
        cursorShape: Qt.PointingHandCursor
        onClicked: Conversation.openMedia(root.msgId)
    }
}
