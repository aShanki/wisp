// A quoted message, as in WhatsApp: a coloured bar, the sender in their
// colour and up to two lines of preview on a darker translucent card.
// quote = {senderName, preview, color}.
import QtQuick
import Wisp

Rectangle {
    id: root
    property var quote
    property int padTop: 4
    property int padBottom: 8
    property int padStart: 8
    property int padEnd: 12
    property int lines: 2
    readonly property color accent: Theme.senderColor(quote ? quote.color : -1)

    // The 4 px bar, then the text between its paddings.
    implicitWidth: 4 + padStart + padEnd + Math.max(name.implicitWidth, preview.implicitWidth)
    implicitHeight: Math.max(42, Math.min(82, padTop + 22 + 20 * preview.lineCount + padBottom))
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
        x: 4 + root.padStart
        y: root.padTop
        width: parent.width - x - root.padEnd
        height: 22
        verticalAlignment: Text.AlignVCenter
        text: root.quote ? root.quote.senderName : ""
        textFormat: Text.PlainText
        color: root.accent
        font.pixelSize: 13
        font.weight: Font.Medium
        font.variableAxes: ({ wght: Font.Medium })
        elide: Text.ElideRight
    }
    Text {
        id: preview
        x: name.x
        y: root.padTop + 24
        width: name.width
        text: root.quote ? root.quote.preview : ""
        textFormat: Text.PlainText
        color: Theme.bubbleMeta
        font.pixelSize: 13
        lineHeight: 20
        lineHeightMode: Text.FixedHeight
        wrapMode: Text.Wrap
        elide: Text.ElideRight
        maximumLineCount: root.lines
    }
}
