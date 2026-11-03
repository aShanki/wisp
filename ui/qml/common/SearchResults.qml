pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import Wisp

ListView {
    id: root
    required property SearchModel results
    signal chosen()
    clip: true
    reuseItems: true
    cacheBuffer: 0
    model: results
    currentIndex: -1
    boundsBehavior: Flickable.StopAtBounds
    ScrollBar.vertical: ScrollLine {}
    WheelScroll { flickable: root }
    function choose() {
        if (currentIndex < 0 && count > 0) currentIndex = 0
        results.open(currentIndex)
        if (currentIndex >= 0) chosen()
    }
    function move(delta) {
        currentIndex = Math.max(0, Math.min(count - 1, currentIndex + delta))
        if (currentIndex >= 0) positionViewAtIndex(currentIndex, ListView.Contain)
    }
    Keys.onReturnPressed: choose()
    Keys.onEnterPressed: choose()
    Keys.onDownPressed: move(1)
    Keys.onUpPressed: move(-1)
    delegate: Rectangle {
        id: row
        required property int index
        required property string chat
        required property string chatName
        required property string senderName
        required property string richSnippet
        required property double timestamp
        width: root.width
        height: 78
        color: ListView.isCurrentItem || mouse.containsMouse ? Theme.hover : Theme.panel
        Avatar {
            id: avatar
            x: 14
            anchors.verticalCenter: parent.verticalCenter
            size: 40
            name: row.chatName
            source: "image://wisp/avatar/" + row.chat
            group: row.chat.endsWith("@g.us")
        }
        Text {
            id: title
            x: 68; y: 12
            width: parent.width - x - date.width - 24
            text: row.chatName
            textFormat: Text.PlainText
            elide: Text.ElideRight
            color: Theme.text
            font.pixelSize: 16
        }
        Text {
            id: date
            anchors.right: parent.right
            anchors.rightMargin: 14
            y: 14
            text: Qt.formatDateTime(new Date(row.timestamp * 1000), "d MMM yy")
            color: Theme.textMuted
            font.pixelSize: 11
        }
        Text {
            x: 68; y: 36
            width: parent.width - x - 14
            text: row.richSnippet
            textFormat: Text.RichText
            elide: Text.ElideRight
            color: Theme.textMuted
            font.pixelSize: 13
            maximumLineCount: 2
            wrapMode: Text.Wrap
        }
        MouseArea {
            id: mouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: {
                root.currentIndex = row.index
                root.choose()
            }
        }
    }
    footer: Item {
        width: root.width
        height: root.results.busy || root.results.hasMore ? 48 : 0
        Button {
            anchors.centerIn: parent
            visible: parent.height > 0
            text: root.results.busy ? "Searching…" : "More results"
            enabled: !root.results.busy
            onClicked: root.results.loadMore()
        }
    }
    Text {
        anchors.centerIn: parent
        width: parent.width - 32
        horizontalAlignment: Text.AlignHCenter
        wrapMode: Text.Wrap
        visible: root.count === 0 && !root.results.busy
        text: root.results.error !== "" ? root.results.error
            : root.results.query === "" ? "Search messages" : "No messages found"
        textFormat: Text.PlainText
        color: Theme.textMuted
        font.pixelSize: 14
    }
}
