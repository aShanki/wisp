// The chat list panel: header, connection banner, search, filter chips and
// the chats.
pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import Wisp

FocusScope {
    id: sidebar

    readonly property string problem: !Daemon.connected ? "Wisp isn't running"
        : Daemon.connectionState === "offline" ? "Computer not connected"
        : Daemon.connectionState === "disconnected" ? "Disconnected from WhatsApp"
        : Daemon.connectionState === "connecting" ? "Connecting to WhatsApp"
        : ""

    signal settingsRequested()
    signal logoutRequested()
    signal chatChosen() // opened, or back to the open chat with Esc

    function focusSearch() {
        search.forceActiveFocus()
        search.selectAll()
    }

    function open(jid) {
        Conversation.open(jid)
        chatChosen()
    }

    function openCurrent() {
        if (list.currentItem)
            open((list.currentItem as ChatRow).jid)
    }

    // Close the open chat and select its row.
    function closeChat() {
        list.currentIndex = ChatList.indexOf(Conversation.jid)
        list.forceActiveFocus()
        Conversation.close()
    }

    function move(delta) {
        if (list.count > 0)
            list.currentIndex = Math.max(0, Math.min(list.count - 1, list.currentIndex + delta))
    }

    function openFromSearch() {
        if (list.currentIndex < 0)
            move(1)
        openCurrent()
        search.clear()
    }

    component Chip: Rectangle {
        id: chip
        required property string label
        required property int filter
        readonly property bool selected: ChatList.filter === filter

        width: chipText.implicitWidth + 26
        height: 32
        radius: 16
        color: selected ? Theme.chip : chipMouse.containsMouse ? Theme.hover : Theme.panel
        border.width: selected ? 0 : 1
        border.color: Theme.divider

        Text {
            id: chipText
            anchors.centerIn: parent
            text: chip.label
            color: chip.selected ? Theme.chipText : Theme.textMuted
            font.pixelSize: 14
            font.weight: Font.Medium
            font.variableAxes: ({ wght: Font.Medium })
        }
        MouseArea {
            id: chipMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: {
                ChatList.filter = chip.filter
                list.positionViewAtBeginning()
            }
        }
    }

    Keys.onEscapePressed: if (Conversation.jid !== "") chatChosen()

    Rectangle {
        anchors.fill: parent
        color: Theme.panel
    }

    Item {
        id: header
        width: parent.width
        height: Theme.headerHeight

        Text {
            x: 20
            anchors.verticalCenter: parent.verticalCenter
            text: "WhatsApp"
            color: Theme.text
            font.pixelSize: Theme.fontSizeTitle
            font.weight: Font.Bold
            font.variableAxes: ({ wght: Font.Bold })
        }
        IconButton {
            id: menuButton
            objectName: "listMenuButton"
            anchors.right: parent.right
            anchors.rightMargin: 12
            anchors.verticalCenter: parent.verticalCenter
            name: "more_vert"
            checked: menu.visible
            onClicked: menu.openFrom(menuButton, false, true)

            PopupMenu {
                id: menu
                objectName: "listMenu"
                PopupMenu.Entry {
                    text: "Settings"
                    onTriggered: sidebar.settingsRequested()
                }
                PopupMenu.Entry {
                    text: "Log out"
                    onTriggered: sidebar.logoutRequested()
                }
            }
        }
    }

    // WhatsApp's "Computer not connected" banner.
    Rectangle {
        id: banner
        anchors.top: header.bottom
        width: parent.width
        height: sidebar.problem !== "" ? 76 : 0
        visible: height > 0
        color: Daemon.connectionState === "connecting" && Daemon.connected ? Theme.field : Theme.warning

        Rectangle {
            id: bannerIcon
            x: 16
            anchors.verticalCenter: parent.verticalCenter
            width: 44
            height: 44
            radius: 22
            color: Theme.panel

            Icon {
                anchors.centerIn: parent
                name: Daemon.connectionState === "connecting" && Daemon.connected ? "schedule" : "wifi_off"
                color: Daemon.connectionState === "connecting" && Daemon.connected ? Theme.textMuted : Theme.warningIcon
            }
        }
        Column {
            anchors.left: bannerIcon.right
            anchors.leftMargin: 14
            anchors.right: parent.right
            anchors.rightMargin: 16
            anchors.verticalCenter: parent.verticalCenter
            spacing: 2

            Text {
                width: parent.width
                text: sidebar.problem
                elide: Text.ElideRight
                color: Theme.text
                font.pixelSize: 16
            }
            Text {
                width: parent.width
                text: !Daemon.connected ? "Reconnecting to the Wisp service…"
                    : Daemon.connectionState === "offline" ? "Make sure your computer has an active Internet connection."
                    : Daemon.connectionState === "disconnected" ? "Trying to reconnect…"
                    : "Messages will arrive once connected."
                wrapMode: Text.Wrap
                maximumLineCount: 2
                elide: Text.ElideRight
                color: Theme.textMuted
                font.pixelSize: 14
            }
        }
    }

    Field {
        id: search
        anchors.top: banner.bottom
        anchors.topMargin: banner.visible ? 10 : 0
        x: 12
        width: parent.width - 24
        iconName: "search"
        placeholderText: "Search"
        onTextChanged: {
            ChatList.filterText = text
            list.currentIndex = text !== "" && list.count > 0 ? 0 : -1
        }
        Keys.onUpPressed: sidebar.move(-1)
        Keys.onDownPressed: sidebar.move(1)
        Keys.onReturnPressed: sidebar.openFromSearch()
        Keys.onEnterPressed: sidebar.openFromSearch()
        Keys.onEscapePressed: event => {
            event.accepted = text !== ""
            clear()
        }
    }

    Row {
        id: chips
        anchors.top: search.bottom
        anchors.topMargin: 10
        x: 12
        spacing: 8

        Chip { objectName: "filterAll"; label: "All"; filter: ChatList.All }
        Chip { objectName: "filterUnread"; label: "Unread"; filter: ChatList.Unread }
        Chip { objectName: "filterGroups"; label: "Groups"; filter: ChatList.Groups }
    }

    // History sync progress.
    Item {
        id: sync
        anchors.top: chips.bottom
        anchors.topMargin: visible ? 8 : 0
        width: parent.width
        height: Daemon.syncPercent >= 0 ? 40 : 0
        visible: height > 0

        Text {
            x: 20
            anchors.verticalCenter: parent.verticalCenter
            text: "Syncing chats from your phone · " + Daemon.syncPercent + "%"
            color: Theme.textMuted
            font.pixelSize: 13
        }
        Rectangle {
            anchors.bottom: parent.bottom
            width: parent.width
            height: 3
            color: Theme.field

            Rectangle {
                width: parent.width * Math.max(0, Daemon.syncPercent) / 100
                height: parent.height
                color: Theme.accent
            }
        }
    }

    ListView {
        id: list
        anchors.top: sync.bottom
        anchors.topMargin: sync.visible ? 0 : 8
        anchors.bottom: parent.bottom
        width: parent.width
        clip: true
        focus: true
        reuseItems: true
        cacheBuffer: 0
        currentIndex: -1
        boundsBehavior: Flickable.StopAtBounds
        highlightFollowsCurrentItem: false
        model: ChatList
        ScrollBar.vertical: ScrollLine {}

        delegate: ChatRow {
            id: row
            current: ListView.isCurrentItem && (list.activeFocus || search.activeFocus)
            onClicked: {
                list.currentIndex = row.index
                sidebar.open(row.jid)
            }
            onContextMenuRequested: (x, y) => {
                rowMenu.choose(row)
                rowMenu.openAt(row, x, y)
            }
            onMenuRequested: button => {
                rowMenu.choose(row)
                rowMenu.openFrom(button, false, true)
            }
        }

        Keys.onReturnPressed: sidebar.openCurrent()
        Keys.onEnterPressed: sidebar.openCurrent()
        Keys.onEscapePressed: event => {
            event.accepted = search.text !== ""
            search.clear()
        }
        Keys.onUpPressed: {
            if (currentIndex > 0) {
                decrementCurrentIndex()
            } else {
                currentIndex = -1
                search.forceActiveFocus()
            }
        }
        // Typing anywhere in the list searches.
        Keys.onPressed: event => {
            if (/^[^\x00-\x20\x7f]+$/.test(event.text) && !(event.modifiers & (Qt.ControlModifier | Qt.AltModifier))) {
                search.forceActiveFocus()
                search.insert(search.length, event.text)
                event.accepted = true
            }
        }

        Text {
            anchors.horizontalCenter: parent.horizontalCenter
            y: 48
            visible: list.count === 0 && !ChatList.loading && Daemon.syncPercent < 0
            text: search.text !== "" ? "No chats, contacts or messages found"
                : ChatList.filter === ChatList.Unread ? "No unread chats"
                : ChatList.filter === ChatList.Groups ? "No groups"
                : "No chats yet"
            color: Theme.textMuted
            font.pixelSize: Theme.fontSize
        }
    }

    // One menu for every row: the chevron's dropdown, or a right-click.
    PopupMenu {
        id: rowMenu
        objectName: "rowMenu"

        property string jid
        property bool pinned
        property bool muted
        property int unread

        function choose(row: ChatRow) {
            jid = row.jid
            pinned = row.pinned
            muted = row.muted
            unread = row.unread
        }

        PopupMenu.Entry {
            text: rowMenu.pinned ? "Unpin chat" : "Pin chat"
            onTriggered: ChatList.setPinned(rowMenu.jid, !rowMenu.pinned)
        }
        PopupMenu.Entry {
            objectName: "menuMute"
            text: rowMenu.muted ? "Unmute notifications" : "Mute notifications"
            onTriggered: ChatList.setMuted(rowMenu.jid, !rowMenu.muted)
        }
        PopupMenu.Entry {
            enabled: rowMenu.unread > 0
            text: "Mark as read"
            onTriggered: ChatList.markRead(rowMenu.jid)
        }
    }
}
