// WhatsApp's expression panel above the composer, with a switcher at the
// bottom. Emoji: category tabs along the top, a search field, then every
// emoji in titled sections, recent ones first; a click inserts the emoji.
// Stickers: recent and favourite ones (starred on the phone); a click sends
// the sticker. Either way the panel stays open; Esc or a click outside closes
// it. Recent emoji are saved when it closes, so the grid holds still.
pragma ComponentBehavior: Bound
import QtCore
import QtQuick
import QtQuick.Controls
import QtQuick.Templates as T
import Wisp

T.Popup {
    id: root
    objectName: "emojiPanel"

    signal picked(string emoji)
    signal stickerPicked(string id)

    property string mode: "emoji" // or "sticker"; kept between openings
    property bool emojiOnly: false // no sticker switcher (reactions)
    readonly property int cell: 44
    readonly property int maxRecent: 32
    // gemoji lists emoji in category order; each category starts at `first`.
    readonly property var categories: [
        {icon: "schedule", title: "Recent", first: ""},
        {icon: "mood", title: "Smileys & emotion", first: "😀"},
        {icon: "emoji_people", title: "People & body", first: "👋"},
        {icon: "pets", title: "Animals & nature", first: "🐵"},
        {icon: "local_cafe", title: "Food & drink", first: "🍇"},
        {icon: "directions_car", title: "Travel & places", first: "🌍"},
        {icon: "sports_basketball", title: "Activities", first: "🎃"},
        {icon: "lightbulb", title: "Objects", first: "👓"},
        {icon: "emoji_symbols", title: "Symbols", first: "🏧"},
        {icon: "flag", title: "Flags", first: "🏁"}
    ]
    readonly property var all: EmojiSearch.all()
    readonly property var sections: {
        const starts = categories.map(c => c.first === "" ? -1 : all.indexOf(c.first))
        return categories.map((c, i) => ({
            icon: c.icon,
            title: c.title,
            emoji: i === 0 ? recent.list : all.slice(starts[i], i + 1 < starts.length ? starts[i + 1] : all.length)
        }))
    }
    property var used: [] // picked since the panel opened, newest first

    function pick(emoji) {
        used = [emoji].concat(used.filter(e => e !== emoji))
        picked(emoji)
    }
    function show(m) {
        mode = emojiOnly ? "emoji" : m
        if (m === "emoji") {
            search.clear()
            search.forceActiveFocus()
        } else {
            stickers.reload()
            stickers.forceActiveFocus()
        }
    }

    padding: 0
    focus: true
    closePolicy: T.Popup.CloseOnEscape | T.Popup.CloseOnPressOutsideParent
    onOpened: show(mode)
    onClosed: {
        if (used.length > 0)
            recent.list = used.concat(recent.list.filter(e => !used.includes(e))).slice(0, maxRecent)
        used = []
    }

    Settings {
        id: recent
        category: "emoji"
        property var list: []
    }

    background: Rectangle {
        radius: 16
        color: Theme.composer
        border.width: 1
        border.color: Theme.divider
    }

    contentItem: Item {
        Item {
            id: emojiPage
            visible: root.mode === "emoji"
            width: parent.width
            anchors.top: parent.top
            anchors.bottom: switcher.visible ? switcher.top : parent.bottom
            anchors.bottomMargin: 8

            // Category tabs; the one being shown is underlined.
            Row {
                id: tabs
                x: 8
                y: 4
                width: parent.width - 16
                visible: search.text === ""
                Repeater {
                    model: root.sections
                    delegate: Tab {
                        required property var modelData
                        required property int index
                        width: tabs.width / (recent.list.length > 0 ? root.sections.length : root.sections.length - 1)
                        visible: index > 0 || modelData.emoji.length > 0
                        icon: modelData.icon
                        current: index === list.currentSection
                        onClicked: list.positionViewAtIndex(index, ListView.Beginning)
                    }
                }
            }

            Field {
                id: search
                objectName: "emojiSearch"
                x: 12
                y: tabs.visible ? tabs.y + tabs.height + 8 : 12
                width: parent.width - 24
                iconName: "search"
                placeholderText: "Search emoji"
                Keys.onReturnPressed: if (results.count > 0) root.pick(results.model[0])
                Keys.onEnterPressed: if (results.count > 0) root.pick(results.model[0])
            }

            ListView {
                id: list
                WheelScroll { flickable: list }
                visible: search.text === ""
                x: 8
                anchors.top: search.bottom
                anchors.topMargin: 4
                anchors.bottom: parent.bottom
                width: parent.width - 16
                clip: true
                boundsBehavior: Flickable.StopAtBounds
                acceptedButtons: Qt.NoButton
                model: root.sections
                readonly property int currentSection: {
                    const i = indexAt(0, contentY + 8)
                    return i >= 0 ? i : 0
                }
                readonly property int columns: Math.max(1, Math.floor(width / root.cell))
                delegate: Column {
                    id: section
                    required property var modelData
                    width: list.width
                    visible: modelData.emoji.length > 0
                    height: visible ? implicitHeight : 0
                    Text {
                        x: 6
                        height: 32
                        verticalAlignment: Text.AlignVCenter
                        text: section.modelData.title
                        color: Theme.textMuted
                        font.pixelSize: 13
                        font.weight: Font.Medium
                    }
                    Grid {
                        columns: list.columns
                        Repeater {
                            model: section.modelData.emoji
                            delegate: EmojiCell {}
                        }
                    }
                }
                ScrollBar.vertical: ScrollLine {}
            }

            GridView {
                id: results
                WheelScroll { flickable: results }
                visible: search.text !== ""
                x: 8
                anchors.top: search.bottom
                anchors.topMargin: 8
                anchors.bottom: parent.bottom
                width: parent.width - 16
                clip: true
                boundsBehavior: Flickable.StopAtBounds
                acceptedButtons: Qt.NoButton
                cellWidth: root.cell
                cellHeight: root.cell
                model: visible ? EmojiSearch.search(search.text) : []
                delegate: EmojiCell {}

                Empty {
                    visible: results.count === 0
                    text: "No emoji found"
                }
            }
        }

        // Recent stickers, or the favourites; fetched afresh on each showing.
        FocusScope {
            id: stickers
            objectName: "stickerPage"
            visible: root.mode === "sticker"
            width: parent.width
            anchors.top: parent.top
            anchors.bottom: switcher.top
            anchors.bottomMargin: 8

            property var recentList: []
            property var favoriteList: []
            property bool favorites: false
            property bool loaded: false

            function reload() {
                Daemon.call("stickers.list", {}, (result, error) => {
                    loaded = true
                    if (error)
                        return
                    favoriteList = result.stickers.filter(s => s.favorite)
                    recentList = result.stickers.filter(s => !s.favorite)
                })
            }

            Row {
                id: stickerTabs
                x: 8
                y: 4
                Tab {
                    width: 56
                    icon: "schedule"
                    current: !stickers.favorites
                    onClicked: stickers.favorites = false
                }
                Tab {
                    objectName: "favoriteStickers"
                    width: 56
                    icon: "star"
                    current: stickers.favorites
                    onClicked: stickers.favorites = true
                }
            }

            Text {
                id: stickerTitle
                x: 14
                y: stickerTabs.y + stickerTabs.height + 4
                height: 32
                verticalAlignment: Text.AlignVCenter
                text: stickers.favorites ? "Favourites" : "Recent"
                color: Theme.textMuted
                font.pixelSize: 13
                font.weight: Font.Medium
            }

            GridView {
                id: stickerGrid
                WheelScroll { flickable: stickerGrid }
                objectName: "stickerGrid"
                x: 8
                anchors.top: stickerTitle.bottom
                anchors.bottom: parent.bottom
                width: parent.width - 16
                clip: true
                boundsBehavior: Flickable.StopAtBounds
                acceptedButtons: Qt.NoButton
                cellWidth: width / Math.max(1, Math.floor(width / 104))
                cellHeight: 104
                model: stickers.favorites ? stickers.favoriteList : stickers.recentList
                delegate: Rectangle {
                    id: stickerCell
                    required property var modelData
                    width: stickerGrid.cellWidth
                    height: stickerGrid.cellHeight
                    radius: 8
                    color: stickerMouse.containsMouse ? Theme.selected : "transparent"
                    Image {
                        anchors.centerIn: parent
                        width: 88
                        height: 88
                        sourceSize: Qt.size(88, 88)
                        fillMode: Image.PreserveAspectFit
                        asynchronous: true
                        source: "image://wisp/sticker/" + stickerCell.modelData.id
                    }
                    MouseArea {
                        id: stickerMouse
                        anchors.fill: parent
                        hoverEnabled: true
                        cursorShape: Qt.PointingHandCursor
                        onClicked: root.stickerPicked(stickerCell.modelData.id)
                    }
                }
                ScrollBar.vertical: ScrollLine {}

                Empty {
                    visible: stickers.loaded && stickerGrid.count === 0
                    text: stickers.favorites ? "Stickers you star on your phone appear here"
                        : "Stickers you send appear here"
                }
            }
        }

        // Emoji | Sticker, as WhatsApp's pill at the bottom.
        Rectangle {
            id: switcher
            visible: !root.emojiOnly
            anchors.horizontalCenter: parent.horizontalCenter
            anchors.bottom: parent.bottom
            anchors.bottomMargin: 10
            width: 2 * 72 + 2
            height: 36
            radius: 18
            color: "transparent"
            border.width: 1
            border.color: Theme.divider
            Row {
                anchors.fill: parent
                anchors.margins: 1
                Repeater {
                    model: [{mode: "emoji", icon: "mood"}, {mode: "sticker", icon: "sticker"}]
                    delegate: Rectangle {
                        id: segment
                        required property var modelData
                        required property int index
                        objectName: modelData.mode + "Tab"
                        width: 72
                        height: 34
                        topLeftRadius: index === 0 ? 17 : 0
                        bottomLeftRadius: index === 0 ? 17 : 0
                        topRightRadius: index === 1 ? 17 : 0
                        bottomRightRadius: index === 1 ? 17 : 0
                        color: root.mode === modelData.mode ? Theme.selected
                            : segmentMouse.containsMouse ? Theme.hover : "transparent"
                        Icon {
                            anchors.centerIn: parent
                            size: 20
                            name: segment.modelData.icon
                            color: root.mode === segment.modelData.mode ? Theme.text : Theme.icon
                        }
                        MouseArea {
                            id: segmentMouse
                            anchors.fill: parent
                            hoverEnabled: true
                            cursorShape: Qt.PointingHandCursor
                            onClicked: root.show(segment.modelData.mode)
                        }
                    }
                }
            }
        }
    }

    // A tab icon, underlined while current.
    component Tab: Item {
        id: tab
        property string icon
        property bool current
        signal clicked()
        height: 44
        Icon {
            anchors.centerIn: parent
            name: tab.icon
            color: tab.current ? Theme.text : Theme.icon
        }
        Rectangle {
            anchors.bottom: parent.bottom
            anchors.horizontalCenter: parent.horizontalCenter
            width: 28
            height: 3
            radius: 1.5
            color: Theme.accent
            visible: tab.current
        }
        MouseArea {
            anchors.fill: parent
            cursorShape: Qt.PointingHandCursor
            onClicked: tab.clicked()
        }
    }

    component Empty: Text {
        anchors.horizontalCenter: parent.horizontalCenter
        y: 24
        width: parent.width - 32
        horizontalAlignment: Text.AlignHCenter
        wrapMode: Text.Wrap
        color: Theme.textMuted
        font.pixelSize: 14
    }

    component EmojiCell: Rectangle {
        id: cellItem
        required property string modelData
        width: root.cell
        height: root.cell
        radius: 8
        color: mouse.containsMouse ? Theme.selected : "transparent"
        Text {
            anchors.centerIn: parent
            text: cellItem.modelData
            textFormat: Text.PlainText
            font.pixelSize: 28
        }
        MouseArea {
            id: mouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: root.pick(cellItem.modelData)
        }
    }
}
