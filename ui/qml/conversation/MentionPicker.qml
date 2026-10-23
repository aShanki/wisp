// WhatsApp's mention picker above the composer in groups: "@", at the start
// or after a space, lists the members, narrowed by what follows it, with the
// first one highlighted. Up and Down move, Enter, Tab or a click choose, Esc
// closes. Choosing writes "@Name " over the typed "@query". Like
// EmojiSuggest, it gets the composer's keys only while it is shown.
pragma ComponentBehavior: Bound
import QtQuick
import Wisp

Rectangle {
    id: root
    required property TextEdit input
    property int start: -1 // where "@query" begins in input, or -1
    property int dismissed: -1 // the start Esc closed
    property real maxHeight: Infinity
    readonly property int rowHeight: 60
    signal chosen(string jid, string name)

    function update() {
        const pos = input.cursorPosition
        const from = Math.max(0, pos - 64)
        const m = Conversation.isGroup ? /(^|\s)@([^\s@]*)$/.exec(input.getText(from, pos)) : null
        if (!m || (m[1] === "" && from > 0)) {
            start = -1
            dismissed = -1
            return
        }
        start = pos - m[2].length - 1
        Members.query(Conversation.jid, m[2])
        list.currentIndex = 0
    }

    function choose(jid, name) {
        const at = start
        const token = "@" + name + " "
        chosen(jid, name)
        input.remove(at, input.cursorPosition)
        input.insert(at, token)
        input.cursorPosition = at + token.length
        input.forceActiveFocus()
    }

    visible: start >= 0 && start !== dismissed && Members.count > 0 && input.activeFocus
    // About six rows, fewer if they don't fit above the composer; the rest scroll.
    height: Math.max(1, Math.min(Members.count, 6, Math.floor((maxHeight - 12) / rowHeight))) * rowHeight + 12
    radius: Theme.radius * 2
    color: Theme.elevated
    border.color: Theme.divider

    Connections {
        target: root.input
        function onCursorPositionChanged() { root.update() }
        function onTextChanged() { root.update() }
    }

    Keys.onPressed: event => {
        if (input.inputMethodComposing)
            return
        if (event.key === Qt.Key_Up) {
            list.decrementCurrentIndex()
        } else if (event.key === Qt.Key_Down) {
            list.incrementCurrentIndex()
        } else if (event.key === Qt.Key_Tab
                   || ((event.key === Qt.Key_Return || event.key === Qt.Key_Enter) && !(event.modifiers & Qt.ShiftModifier))) {
            list.forceLayout() // the rows may have changed since the last frame
            const row = list.currentItem as Member
            if (row)
                choose(row.jid, row.name)
        } else if (event.key === Qt.Key_Escape) {
            dismissed = start
        } else {
            return
        }
        event.accepted = true
    }

    component Member: Rectangle {
        id: row
        required property int index
        required property string jid
        required property string name
        required property string phone
        signal clicked()

        width: ListView.view.width
        height: root.rowHeight
        color: ListView.isCurrentItem ? Theme.selected : mouse.containsMouse ? Theme.hover : Theme.elevated
        radius: Theme.radius

        Avatar {
            x: 10
            anchors.verticalCenter: parent.verticalCenter
            size: 40
            name: row.name
            source: "image://wisp/avatar/" + row.jid
            maskColor: row.color
        }
        Column {
            x: 64
            width: parent.width - x - 12
            anchors.verticalCenter: parent.verticalCenter
            spacing: 2
            Text {
                width: parent.width
                text: row.name
                textFormat: Text.PlainText
                color: Theme.text
                font.pixelSize: Theme.fontSizeLarge
                elide: Text.ElideRight
            }
            Text {
                width: parent.width
                visible: row.phone !== ""
                text: row.phone
                textFormat: Text.PlainText
                color: Theme.textMuted
                font.pixelSize: 13
                elide: Text.ElideRight
            }
        }
        MouseArea {
            id: mouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: row.clicked()
        }
    }

    ListView {
        id: list
        objectName: "mentionList"
        anchors.fill: parent
        anchors.margins: 6
        model: Members
        interactive: contentHeight > height
        clip: true
        keyNavigationWraps: true
        delegate: Member {
            id: member
            onClicked: root.choose(member.jid, member.name)
        }
    }
}
