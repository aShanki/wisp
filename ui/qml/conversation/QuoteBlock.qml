// A quoted message, as in WhatsApp: a coloured bar, the sender in their
// colour and one line of preview on a darker translucent card.
// quote = {senderName, preview, color}.
import QtQuick
import Wisp

Rectangle {
    id: root
    property var quote
    readonly property color accent: Theme.senderColor(quote ? quote.color : -1)

    implicitWidth: 20 + Math.max(name.implicitWidth, preview.implicitWidth)
    implicitHeight: 52
    radius: 7.5
    color: Theme.quote
    clip: true

    // The bar follows the card's rounded corners.
    Item {
        width: 4
        height: parent.height
        clip: true
        Rectangle {
            width: root.width
            height: parent.height
            radius: root.radius
            color: root.accent
        }
    }
    Text {
        id: name
        x: 12
        y: 7
        width: parent.width - 20
        text: root.quote ? root.quote.senderName : ""
        textFormat: Text.PlainText
        color: root.accent
        font.pixelSize: 13
        font.weight: Font.Medium
        elide: Text.ElideRight
    }
    Text {
        id: preview
        x: 12
        y: 27
        width: parent.width - 20
        text: root.quote ? root.quote.preview : ""
        textFormat: Text.PlainText
        color: Theme.bubbleMeta
        font.pixelSize: 13
        elide: Text.ElideRight
        maximumLineCount: 1
    }
}
