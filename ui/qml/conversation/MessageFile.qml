// A document card, as in WhatsApp. A click opens it.
import QtQuick
import Wisp

Rectangle {
    id: root
    property string msgId
    property string fileName
    property real size
    property bool loading

    readonly property string ext: fileName.indexOf(".") > 0 ? fileName.slice(fileName.lastIndexOf(".") + 1).toUpperCase() : ""
    readonly property string detail: loading ? "Loading…"
        : [ext, size > 0 ? Format.fileSize(size) : ""].filter(s => s !== "").join(" • ")

    // A document is WhatsApp Web's card: 330 px, 13 px above and below
    // the 26 x 30 icon and the name with its details.
    implicitWidth: 330
    height: 26 + Math.max(30, name.lineCount * 19 + 21)
    radius: 6
    color: Theme.quote

    // An icon, the name, and its type and size.
    Icon {
        x: 13
        y: 10
        name: "description"
        size: 36
        color: Theme.senderColor(3)
    }
    Text {
        id: name
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
