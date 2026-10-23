// A document card or a voice note, as in WhatsApp. A click opens it.
import QtQuick
import Wisp

Rectangle {
    id: root
    property string msgId
    property string type
    property string fileName
    property real size
    property int duration
    property bool loading
    property bool fromMe

    readonly property bool audio: type === "voice" || type === "audio"
    readonly property string ext: fileName.indexOf(".") > 0 ? fileName.slice(fileName.lastIndexOf(".") + 1).toUpperCase() : ""
    readonly property string detail: loading ? "Loading…"
        : [ext, size > 0 ? Format.fileSize(size) : ""].filter(s => s !== "").join(" • ")

    implicitWidth: audio ? 280 : Math.max(240, 62 + Math.min(260, name.implicitWidth))
    height: audio ? 46 : 62
    radius: 7.5
    color: audio ? "transparent" : Theme.quote

    // Voice: a play button, a track and the duration.
    Icon {
        visible: root.audio
        x: 4
        anchors.verticalCenter: parent.verticalCenter
        name: visible ? "play_arrow" : ""
        size: 34
        color: Theme.bubbleMeta
    }
    Rectangle {
        visible: root.audio
        x: 46
        y: 18
        width: parent.width - 58
        height: 3
        radius: 1.5
        color: Theme.bubbleMeta
        opacity: 0.4
    }
    Rectangle {
        visible: root.audio
        x: 44
        y: 13
        width: 13
        height: 13
        radius: 6.5
        color: root.fromMe ? Theme.quoteMine : Theme.read
    }
    Text {
        visible: root.audio
        x: 46
        y: 30
        text: root.loading ? "Loading…" : Format.duration(root.duration)
        color: Theme.bubbleMeta
        font.pixelSize: 11
    }

    // Document: an icon, the name and its type and size.
    Icon {
        visible: !root.audio
        x: 12
        anchors.verticalCenter: parent.verticalCenter
        name: visible ? "description" : ""
        size: 34
        color: Theme.senderColor(3)
    }
    Text {
        id: name
        visible: !root.audio
        x: 52
        y: 12
        width: parent.width - 62
        text: root.fileName !== "" ? root.fileName : "Document"
        textFormat: Text.PlainText
        elide: Text.ElideMiddle
        color: Theme.text
        font.pixelSize: 14
    }
    Text {
        visible: !root.audio
        x: 52
        y: 33
        width: parent.width - 62
        text: root.detail
        textFormat: Text.PlainText
        elide: Text.ElideRight
        color: Theme.bubbleMeta
        font.pixelSize: 12
    }

    MouseArea {
        anchors.fill: parent
        cursorShape: Qt.PointingHandCursor
        onClicked: Conversation.openMedia(root.msgId)
    }
}
