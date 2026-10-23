// A centred chip on the wallpaper: day separators and system messages.
import QtQuick
import Wisp

Rectangle {
    property alias text: label.text
    property real maxWidth: 10000

    width: Math.min(maxWidth, label.implicitWidth + 24)
    height: label.height + 11
    radius: 7.5
    color: Theme.bubbleSystem
    border.width: 1
    border.color: Theme.bubbleEdge

    Text {
        id: label
        x: 12
        y: 5
        width: parent.width - 24
        horizontalAlignment: Text.AlignHCenter
        textFormat: Text.StyledText
        wrapMode: Text.Wrap
        color: Theme.textMuted
        font.pixelSize: 12
    }
}
