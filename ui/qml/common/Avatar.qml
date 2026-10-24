// Round avatar: WhatsApp's grey silhouette, the photo on top. The photo is
// rounded by an opaque ring in `maskColor` (the background behind it),
// which is cheaper than a mask effect.
import QtQuick
import Wisp

Item {
    id: root

    property string name
    property string source
    property bool group: false
    property int size: Theme.avatarSize
    property color maskColor: Theme.panel
    // When set, a picture-less avatar shows the name's initial in this colour.
    property color initialColor: "transparent"
    readonly property string initial: {
        if (initialColor.a === 0 || name === "")
            return ""
        const c = String.fromCodePoint(name.codePointAt(0))
        return c.toUpperCase() !== c.toLowerCase() ? c.toUpperCase() : ""
    }

    width: size
    height: size

    Rectangle {
        anchors.fill: parent
        radius: width / 2
        color: Theme.dark ? "#242626" : "#eeeeee"

        Icon {
            visible: root.initial === ""
            anchors.centerIn: parent
            size: Math.round(root.size * 0.6)
            name: root.group ? "group" : "person"
            color: Theme.dark ? "#bdbdbd" : "#757778"
        }
        Text {
            visible: root.initial !== ""
            anchors.centerIn: parent
            text: root.initial
            textFormat: Text.PlainText
            color: root.initialColor
            font.pixelSize: Math.round(root.size * 0.5)
            font.weight: Font.Medium
        }
    }

    Image {
        id: img
        anchors.fill: parent
        source: root.source
        sourceSize: Qt.size(root.size, root.size)
        asynchronous: true
        fillMode: Image.PreserveAspectCrop
    }

    // Inner edge is the circle; outer edge covers the image's corners, so
    // it reaches 0.21 × size past the avatar: keep that much room around it.
    Rectangle {
        readonly property int ring: Math.ceil(root.size * 0.21) + 1
        visible: img.status === Image.Ready && img.implicitWidth > 1
        anchors.centerIn: parent
        width: root.size + 2 * ring
        height: width
        radius: width / 2
        color: "transparent"
        border.width: ring
        border.color: root.maskColor
    }
}
