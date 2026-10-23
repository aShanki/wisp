// A thin scroll bar, visible only while scrolling or hovered.
import QtQuick
import QtQuick.Controls
import Wisp

ScrollBar {
    id: bar

    implicitWidth: 10
    padding: 2
    minimumSize: 0.05
    background: null
    contentItem: Rectangle {
        implicitWidth: bar.hovered || bar.pressed ? 6 : 4
        radius: width / 2
        color: Theme.textFaint
        visible: bar.active && bar.size < 1
    }
}
