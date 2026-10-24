// WhatsApp's emoji suggestions above the composer: typing ":sku" lists the
// matching emoji with the best one highlighted. Up and Down move, Enter or
// Tab insert, Esc closes, a click inserts; a typed ":skull:" is replaced when
// its colon is typed. The composer forwards its keys here (Keys.forwardTo),
// which only reach this item while it is shown.
pragma ComponentBehavior: Bound
import QtQuick
import Wisp

Rectangle {
    id: root
    required property TextEdit input
    property int start: -1 // where ":query" begins in input, or -1
    property string query
    property string dismissed // the query Esc closed
    property real maxHeight: Infinity
    readonly property int rowHeight: 40

    function update() {
        const pos = input.cursorPosition
        const from = Math.max(0, pos - 50) // longer than any alias
        const m = /(^|\s):([a-z0-9_+-]{2,})$/i.exec(input.getText(from, pos))
        if (!m || (m[1] === "" && from > 0)) {
            start = -1
            dismissed = ""
            return
        }
        const q = m[2].toLowerCase()
        if (pos - q.length - 1 === start && q === query)
            return
        start = pos - q.length - 1
        query = q
        EmojiSearch.query(q)
        list.currentIndex = 0
    }

    function insert(emoji) {
        const at = start
        input.remove(at, input.cursorPosition)
        input.insert(at, emoji)
        input.cursorPosition = at + emoji.length
        input.forceActiveFocus()
    }

    visible: start >= 0 && query !== dismissed && EmojiSearch.count > 0 && input.activeFocus
    width: 320
    // Whole rows, as many as fit above the composer; the rest scroll.
    height: Math.max(1, Math.min(EmojiSearch.count, Math.floor((maxHeight - 12) / rowHeight))) * rowHeight + 12
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
        const exact = event.text === ":" ? EmojiSearch.lookup(query) : ""
        if (event.key === Qt.Key_Up) {
            list.decrementCurrentIndex()
        } else if (event.key === Qt.Key_Down) {
            list.incrementCurrentIndex()
        } else if (event.key === Qt.Key_Tab
                   || ((event.key === Qt.Key_Return || event.key === Qt.Key_Enter) && !(event.modifiers & Qt.ShiftModifier))) {
            list.forceLayout() // the rows may have changed since the last frame
            insert((list.currentItem as Suggestion).emoji)
        } else if (event.key === Qt.Key_Escape) {
            dismissed = query
        } else if (exact !== "") {
            insert(exact)
        } else {
            return
        }
        event.accepted = true
    }

    component Suggestion: Rectangle {
        id: row
        required property int index
        required property string emoji
        required property string name
        signal chosen()

        width: ListView.view.width
        color: ListView.isCurrentItem ? Theme.selected : "transparent"
        radius: Theme.radius

        Text {
            x: 10
            anchors.verticalCenter: parent.verticalCenter
            text: row.emoji
            font.pixelSize: 24
        }
        Text {
            x: 48
            width: parent.width - x - 10
            anchors.verticalCenter: parent.verticalCenter
            text: ":" + row.name + ":"
            color: Theme.text
            font.pixelSize: Theme.fontSize
            elide: Text.ElideRight
        }
        MouseArea {
            anchors.fill: parent
            hoverEnabled: true
            onEntered: row.ListView.view.currentIndex = row.index
            onClicked: row.chosen()
        }
    }

    ListView {
        id: list
        anchors.fill: parent
        anchors.margins: 6
        model: EmojiSearch
        interactive: contentHeight > height
        acceptedButtons: Qt.NoButton
        clip: true
        keyNavigationWraps: true
        delegate: Suggestion {
            id: suggestion
            height: root.rowHeight
            onChosen: root.insert(suggestion.emoji)
        }
    }
}
