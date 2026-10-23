// A centred chip on the wallpaper, as WhatsApp Web's system bubble: day
// separators and system messages, 4 px above and below 16 px lines.
import QtQuick
import Wisp

Rectangle {
    property alias text: label.text
    property alias font: label.font
    property real maxWidth: 10000

    width: Math.min(maxWidth, label.implicitWidth + 24)
    height: label.height - label.topPadding + 8
    radius: 6
    color: Theme.bubbleSystem

    Text {
        id: label
        x: 12
        y: 4
        width: parent.width - 24
        horizontalAlignment: Text.AlignHCenter
        textFormat: Text.StyledText
        wrapMode: Text.Wrap
        color: Theme.textMuted
        font.pixelSize: 12
        lineHeight: 16
        lineHeightMode: Text.FixedHeight
        topPadding: 1
    }
}
