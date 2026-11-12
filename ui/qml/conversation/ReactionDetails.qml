// WhatsApp Web's reaction details, opened from a message's reaction pill: how
// many reacted, a chip per emoji with its count, and everyone with their
// emoji, us first. A chip reacts with its emoji, or takes ours back; so does
// our own row. The first chip offers every emoji. Chips keep their place
// while it is open, at 0 when their last reaction goes. It shows once the
// daemon has listed the people, follows the message's reactions, and closes
// when none are left.
pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Templates as T
import Wisp

T.Popup {
    id: root
    objectName: "reactionDetails"

    property string msgId
    property var people: [] // {jid, name, phone, emoji, mine}, ours first
    property var order: []  // the chips' emojis
    property rect anchorRect // the pill, in window coordinates
    property bool alignRight // share the pill's right edge, as under our own messages
    property bool above // over the pill: where it opened, whatever its height becomes
    readonly property string mine: {
        const p = people.find(p => p.mine)
        return p ? p.emoji : ""
    }
    readonly property var chips: {
        const counts = {}
        for (const p of people)
            counts[p.emoji] = (counts[p.emoji] || 0) + 1
        return order.map(e => ({ emoji: e, count: counts[e] || 0, mine: e === mine }))
    }
    // The + chip: every emoji, in a panel placed at (atX, atY) of the window.
    signal moreRequested(string id, string mine, real atX, real atY)

    // emojis: those on the pill, in its order.
    function show(id, pill, right, emojis) {
        const p = pill.mapToItem(null, 0, 0)
        anchorRect = Qt.rect(p.x, p.y, pill.width, pill.height)
        alignRight = right
        order = emojis
        msgId = id
        Conversation.watchReactors(id)
    }
    function load(list) {
        if (list.length === 0) {
            if (visible)
                close()
            else
                stop()
            return
        }
        const added = list.map(p => p.emoji).filter((e, i, all) => !order.includes(e) && all.indexOf(e) === i)
        if (added.length > 0)
            order = order.concat(added)
        people = list
        if (visible)
            return
        const room = parent.height - 8, h = paneHeight(list.length)
        above = anchorRect.y + anchorRect.height + 4 + h > room && anchorRect.y - 4 - h >= 8
        open()
    }
    // Web's list is between 151 and 296 px tall, about five and a half rows.
    function paneHeight(rows) {
        return list.y + Math.max(151, Math.min(296, rows * 52 + 16))
    }
    function stop() {
        msgId = ""
        people = []
        Conversation.watchReactors("")
    }
    function react(emoji) {
        Conversation.react(msgId, emoji === mine ? "" : emoji)
    }
    // Position v of a pane size long, moved to stay 8 px inside the window.
    function inside(v, size, room) {
        return Math.max(8, Math.min(v, room - size - 8))
    }

    Connections {
        target: Conversation
        function onReactorsLoaded(id, list) {
            if (id === root.msgId)
                root.load(list)
        }
    }
    onClosed: stop()
    // The pill moves with the window's size.
    Connections {
        target: root.parent
        enabled: root.visible
        function onWidthChanged() { root.close() }
        function onHeightChanged() { root.close() }
    }

    parent: T.Overlay.overlay
    popupType: T.Popup.Item
    // A press outside closes it without also landing on what is under it:
    // on the pill it would open it again.
    modal: true
    dim: false
    focus: true
    closePolicy: T.Popup.CloseOnEscape | T.Popup.CloseOnPressOutside
    padding: 0
    width: Math.min(375, parent ? parent.width - 16 : 375)
    height: paneHeight(people.length)
    x: inside(alignRight ? anchorRect.x + anchorRect.width - width : anchorRect.x, width, parent ? parent.width : 0)
    y: inside(above ? anchorRect.y - height - 4 : anchorRect.y + anchorRect.height + 4, height, parent ? parent.height : 0)

    background: Rectangle {
        radius: 16
        color: Theme.elevated
        border.width: 1
        border.color: Theme.divider
    }

    component Chip: Rectangle {
        id: chip
        property bool selected: false
        property alias hovered: chipMouse.containsMouse
        signal clicked()

        height: 32
        radius: 16
        color: selected ? Theme.chip : hovered ? Theme.hover : Theme.elevated
        border.width: selected ? 0 : 1
        border.color: Theme.divider

        MouseArea {
            id: chipMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: chip.clicked()
        }
    }

    contentItem: Item {
        clip: true // the rows, at the card's round corners

        Text {
            id: title
            objectName: "reactionCount"
            x: 12
            y: 16
            height: 20
            verticalAlignment: Text.AlignVCenter
            text: root.people.length === 1 ? "1 reaction" : root.people.length + " reactions"
            color: Theme.textMuted
            font.pixelSize: 14
            font.weight: Font.Medium
            font.variableAxes: ({ wght: Font.Medium })
        }

        // One row of chips; more than fit scroll sideways.
        Flickable {
            id: chipBar
            y: title.y + title.height + 8
            width: parent.width
            height: 32
            leftMargin: 12
            rightMargin: 12
            contentWidth: chipRow.width
            contentHeight: height
            flickableDirection: Flickable.HorizontalFlick
            boundsBehavior: Flickable.StopAtBounds
            interactive: contentWidth + 24 > width
            clip: true

            Row {
                id: chipRow
                spacing: 8

                Chip {
                    objectName: "addReaction"
                    width: 44
                    onClicked: {
                        const id = root.msgId, mine = root.mine
                        const p = root.contentItem.mapToItem(null, 0, 0)
                        root.moreRequested(id, mine, p.x, p.y)
                        root.close()
                    }
                    Icon {
                        anchors.centerIn: parent
                        name: "add_reaction"
                        size: 20
                    }
                }
                Repeater {
                    model: root.chips
                    Chip {
                        id: emojiChip
                        required property var modelData
                        readonly property string emoji: modelData.emoji
                        objectName: "reactionChip"
                        width: chipEmoji.width + chipCount.implicitWidth + 28
                        selected: modelData.mine
                        onClicked: root.react(emoji)
                        Text {
                            id: chipEmoji
                            x: 10
                            width: 20
                            anchors.verticalCenter: parent.verticalCenter
                            horizontalAlignment: Text.AlignHCenter
                            text: emojiChip.emoji
                            textFormat: Text.PlainText
                            color: Theme.text
                            font.pixelSize: 16
                        }
                        Text {
                            id: chipCount
                            x: chipEmoji.x + chipEmoji.width + 6
                            anchors.verticalCenter: parent.verticalCenter
                            text: emojiChip.modelData.count
                            color: emojiChip.selected ? Theme.chipText : Theme.textMuted
                            font.pixelSize: 12
                            font.weight: Font.Medium
                            font.variableAxes: ({ wght: Font.Medium })
                        }
                    }
                }
            }
        }

        Rectangle {
            id: divider
            y: chipBar.y + chipBar.height + 12
            width: parent.width
            height: 1
            color: Theme.divider
        }

        ListView {
            id: list
            objectName: "reactorList"
            y: divider.y + 1
            width: parent.width
            height: parent.height - y
            topMargin: 8
            bottomMargin: 8
            model: root.people
            clip: true
            interactive: contentHeight + 16 > height
            acceptedButtons: Qt.NoButton
            boundsBehavior: Flickable.StopAtBounds
            ScrollBar.vertical: ScrollLine {}
            WheelScroll { flickable: list }

            delegate: Item {
                id: row
                required property var modelData
                readonly property bool mine: modelData.mine
                readonly property string emoji: modelData.emoji
                readonly property string name: modelData.name
                readonly property string detail: mine ? "Click to remove" : modelData.phone
                objectName: mine ? "reactorMe" : "reactor"
                width: ListView.view.width
                height: 52

                Rectangle {
                    id: cell
                    x: 10
                    width: parent.width - 20
                    height: parent.height
                    radius: Theme.radius
                    color: row.mine && rowMouse.containsMouse ? Theme.hover : Theme.elevated

                    Avatar {
                        x: 2
                        anchors.verticalCenter: parent.verticalCenter
                        size: 40
                        name: row.name
                        source: row.modelData.jid !== "" ? "image://wisp/avatar/" + row.modelData.jid : ""
                        maskColor: cell.color
                    }
                    Column {
                        x: 57
                        width: parent.width - x - 50
                        anchors.verticalCenter: parent.verticalCenter
                        Text {
                            width: parent.width
                            text: row.name
                            textFormat: Text.PlainText
                            color: Theme.text
                            font.pixelSize: 15
                            elide: Text.ElideRight
                        }
                        Text {
                            width: parent.width
                            height: 16
                            visible: row.detail !== ""
                            verticalAlignment: Text.AlignVCenter
                            text: row.detail
                            textFormat: Text.PlainText
                            color: Theme.textMuted
                            font.pixelSize: 12
                            elide: Text.ElideRight
                        }
                    }
                    Text {
                        anchors.right: parent.right
                        anchors.rightMargin: 10
                        anchors.verticalCenter: parent.verticalCenter
                        width: 32
                        horizontalAlignment: Text.AlignHCenter
                        text: row.emoji
                        textFormat: Text.PlainText
                        color: Theme.text
                        font.pixelSize: 24
                    }
                    MouseArea {
                        id: rowMouse
                        anchors.fill: parent
                        enabled: row.mine
                        hoverEnabled: true
                        cursorShape: Qt.PointingHandCursor
                        onClicked: root.react(row.emoji)
                    }
                }
            }
        }
    }
}
