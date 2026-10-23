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

    // A document is WhatsApp Web's card: 330 px, 13 px above and below
    // the 26 x 30 icon and the name with its details.
    implicitWidth: audio ? 290 : 330
    height: audio ? 46 : 26 + Math.max(30, name.lineCount * 19 + 21)
    radius: audio ? 7.5 : 6
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
        x: 13
        y: 10
        name: visible ? "description" : ""
        size: 36
        color: Theme.senderColor(3)
    }
    Text {
        id: name
        visible: !root.audio
        x: 55
        y: 12
        width: parent.width - x - 29
        text: root.fileName !== "" ? root.fileName : "Untitled"
        textFormat: Text.PlainText
        wrapMode: Text.WrapAnywhere
        maximumLineCount: 2
        elide: Text.ElideRight
        color: Theme.text
        font.pixelSize: 14
        lineHeight: 19
        lineHeightMode: Text.FixedHeight
    }
    Text {
        visible: !root.audio
        x: name.x
        y: 11 + name.lineCount * 19 + 3
        width: name.width
        height: 15
        verticalAlignment: Text.AlignVCenter
        text: root.detail
        textFormat: Text.PlainText
        elide: Text.ElideRight
        color: Theme.bubbleMeta
        font.pixelSize: 11
    }

    MouseArea {
        anchors.fill: parent
        cursorShape: Qt.PointingHandCursor
        onClicked: Conversation.openMedia(root.msgId)
    }
}
