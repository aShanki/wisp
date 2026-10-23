// A Material Symbols icon from qrc:/assets/icons, tinted to `color`.
import QtQuick
import QtQuick.Controls.impl
import Wisp

ColorImage {
    property string name
    property int size: 24

    width: size
    height: size
    sourceSize: Qt.size(size, size)
    source: name !== "" ? "qrc:/assets/icons/" + name + ".svg" : ""
    color: Theme.icon
}
