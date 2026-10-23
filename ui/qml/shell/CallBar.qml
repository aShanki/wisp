// The call in progress, as a slim bar: who, how long, mute, and a red end.
import QtQuick
import Wisp

Rectangle {
    id: bar

    property string callId
    property string chat
    property string name
    property string state
    property bool muted
    property bool peerMuted
    property int since
    property int now

    visible: callId !== ""
    width: 360
    height: 56
    radius: 28
    color: Theme.elevated
    border.width: 1
    border.color: Theme.divider

    Connections {
        target: Daemon
        function onEvent(name, params) {
            if (name !== "call.state")
                return
            const ours = params.callId === bar.callId
            if (params.state === "ended" || (params.incoming && params.state === "ringing")) {
                if (ours)
                    bar.callId = ""
                return
            }
            bar.chat = params.chat
            bar.name = params.name
            bar.state = params.state
            bar.muted = params.muted
            bar.peerMuted = params.peerMuted
            bar.since = params.since
            bar.now = Date.now() / 1000
            bar.callId = params.callId
        }
        function onConnectedChanged() { bar.callId = "" }
    }

    Timer {
        interval: 1000
        repeat: true
        running: bar.visible && bar.state === "active"
        onTriggered: bar.now = Date.now() / 1000
    }

    function clock(s) {
        const m = Math.floor(s / 60)
        return m + ":" + String(s % 60).padStart(2, "0")
    }

    MouseArea {
        anchors.fill: parent
        cursorShape: Qt.PointingHandCursor
        onClicked: Conversation.open(bar.chat)
    }

    Avatar {
        id: avatar
        x: 8
        anchors.verticalCenter: parent.verticalCenter
        size: 40
        maskColor: bar.color
        name: bar.name
        source: bar.chat ? "image://wisp/avatar/" + bar.chat : ""
    }

    Column {
        anchors.left: avatar.right
        anchors.leftMargin: 12
        anchors.right: mute.left
        anchors.rightMargin: 8
        anchors.verticalCenter: parent.verticalCenter
        spacing: 2

        Text {
            width: parent.width
            text: bar.name
            textFormat: Text.PlainText
            elide: Text.ElideRight
            color: Theme.text
            font.pixelSize: 15
            font.weight: Font.Medium
        }
        Text {
            width: parent.width
            text: (bar.state === "active" ? bar.clock(Math.max(0, bar.now - bar.since))
                   : bar.state === "connecting" ? "Connecting…" : "Ringing…")
                  + (bar.peerMuted ? " · muted" : "")
            elide: Text.ElideRight
            color: Theme.textMuted
            font.pixelSize: 13
            font.features: {"tnum": 1}
        }
    }

    IconButton {
        id: mute
        objectName: "callMute"
        anchors.right: end.left
        anchors.rightMargin: 8
        anchors.verticalCenter: parent.verticalCenter
        visible: bar.state !== "ringing"
        name: bar.muted ? "mic_off" : "mic"
        checked: bar.muted
        onClicked: Daemon.call("calls.mute", {callId: bar.callId, muted: !bar.muted})
    }

    Rectangle {
        id: end
        objectName: "callEnd"
        anchors.right: parent.right
        anchors.rightMargin: 8
        anchors.verticalCenter: parent.verticalCenter
        width: 40
        height: 40
        radius: 20
        color: endMouse.containsMouse ? Qt.darker("#ea0038", 1.1) : "#ea0038"

        Icon {
            anchors.centerIn: parent
            name: "call"
            size: 22
            rotation: 135
            color: "#ffffff"
        }
        MouseArea {
            id: endMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: Daemon.call("calls.end", {callId: bar.callId})
        }
    }
}
