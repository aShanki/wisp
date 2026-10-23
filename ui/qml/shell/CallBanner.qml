// An incoming call, floating over the top of the window until it stops ringing:
// Accept (voice calls) and Decline.
import QtQuick
import Wisp

Rectangle {
    id: banner

    property string callId
    property string chat
    property string name
    property bool video
    property bool group

    visible: callId !== ""
    width: 360
    height: 64
    radius: 12
    color: Theme.elevated
    border.width: 1
    border.color: Theme.divider

    Connections {
        target: Daemon
        function onEvent(name, params) {
            if (name !== "call.state")
                return
            if (params.incoming && params.state === "ringing") {
                banner.chat = params.chat
                banner.name = params.name
                banner.video = params.video
                banner.group = params.group
                banner.callId = params.callId
            } else if (params.callId === banner.callId) {
                banner.callId = ""
            }
        }
        function onConnectedChanged() { banner.callId = "" }
    }

    MouseArea {
        anchors.fill: parent
        cursorShape: Qt.PointingHandCursor
        onClicked: Conversation.open(banner.chat)
    }

    Avatar {
        id: avatar
        x: 12
        anchors.verticalCenter: parent.verticalCenter
        size: 40
        maskColor: banner.color
        name: banner.name
        group: banner.group
        source: banner.chat ? "image://wisp/avatar/" + banner.chat : ""
    }

    Column {
        anchors.left: avatar.right
        anchors.leftMargin: 12
        anchors.right: accept.visible ? accept.left : decline.left
        anchors.rightMargin: 12
        anchors.verticalCenter: parent.verticalCenter
        spacing: 2

        Text {
            width: parent.width
            text: banner.name
            textFormat: Text.PlainText
            elide: Text.ElideRight
            color: Theme.text
            font.pixelSize: 15
            font.weight: Font.Medium
            font.variableAxes: ({ wght: Font.Medium })
        }
        Text {
            width: parent.width
            text: banner.group ? (banner.video ? "Group video call" : "Group voice call")
                               : (banner.video ? "Video call" : "Voice call")
            elide: Text.ElideRight
            color: Theme.textMuted
            font.pixelSize: 13
        }
    }

    Rectangle { // voice calls only: video and group calls just ring
        id: accept
        objectName: "callAccept"
        anchors.right: decline.left
        anchors.rightMargin: 12
        anchors.verticalCenter: parent.verticalCenter
        visible: !banner.video && !banner.group
        width: 40
        height: 40
        radius: 20
        color: acceptMouse.containsMouse ? Qt.darker("#25d366", 1.1) : "#25d366"

        Icon {
            anchors.centerIn: parent
            name: "call"
            size: 22
            color: "#ffffff"
        }
        MouseArea {
            id: acceptMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: Daemon.call("calls.accept", {callId: banner.callId})
        }
    }

    Rectangle {
        id: decline
        objectName: "callDecline"
        anchors.right: parent.right
        anchors.rightMargin: 12
        anchors.verticalCenter: parent.verticalCenter
        width: 40
        height: 40
        radius: 20
        color: declineMouse.containsMouse ? Qt.darker("#ea0038", 1.1) : "#ea0038"

        Icon {
            anchors.centerIn: parent
            name: "call"
            size: 22
            rotation: 135
            color: "#ffffff"
        }
        MouseArea {
            id: declineMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: {
                Daemon.call("calls.reject", {callId: banner.callId}, () => {})
                banner.callId = ""
            }
        }
    }
}
