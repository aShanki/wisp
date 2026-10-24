// The open chat, as in WhatsApp: header, messages on the wallpaper (newest at
// the bottom, paged upwards) and the composer.
pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Templates as T
import Wisp

Rectangle {
    id: view
    color: Theme.conversation

    property var drafts: ({})
    property string draftJid: ""
    property int unseen: 0
    readonly property real nbsp: space.advanceWidth("\u00a0")

    function scrollToBottom() {
        wheel.stop()
        list.positionViewAtBeginning()
        list.pinned = true
    }
    function maybeLoadOlder() {
        if (Conversation.hasMore && !Conversation.loading && list.count > 0
                && list.contentY - list.originY < list.height)
            Conversation.loadOlder()
    }
    function copyRich(rich) {
        copier.text = rich
        copier.selectAll()
        copier.copy()
        copier.text = ""
    }

    Component.onCompleted: {
        draftJid = Conversation.jid
        composer.focusInput()
    }

    Connections {
        target: Conversation
        function onJidChanged() {
            if (view.draftJid !== "")
                view.drafts[view.draftJid] = composer.text
            view.draftJid = Conversation.jid
            composer.setText(view.drafts[Conversation.jid] || "")
            view.unseen = 0
            list.pinned = true
        }
        function onOpened() {
            view.scrollToBottom()
            composer.focusInput()
            Qt.callLater(view.maybeLoadOlder)
        }
        function onNewMessage(fromMe) {
            if (fromMe || list.pinned)
                Qt.callLater(view.scrollToBottom)
            else
                view.unseen++
        }
        function onLoadingChanged() {
            if (!Conversation.loading)
                Qt.callLater(view.maybeLoadOlder)
        }
    }

    // WhatsApp's doodle, when installed: one opaque tiled image.
    Image {
        anchors.top: header.bottom
        anchors.bottom: parent.bottom
        width: parent.width
        visible: status === Image.Ready
        source: Theme.wallpaper
        fillMode: Image.Tile
        asynchronous: true
    }

    ConversationHeader {
        id: header
        width: parent.width
    }

    ListView {
        id: list
        anchors.top: header.bottom
        anchors.bottom: composer.top
        width: parent.width
        clip: true
        model: Conversation.messages
        verticalLayoutDirection: ListView.BottomToTop
        reuseItems: true
        // Rows, and their images, are built well before they scroll in and
        // kept a while after, so images don't blank and reload mid-scroll.
        cacheBuffer: 2000
        boundsBehavior: Flickable.StopAtBounds
        // A mouse drag doesn't pull the list as a finger would; WheelScroll
        // scrolls it for the wheel and trackpad. Whole pixels keep text from shimmering.
        acceptedButtons: Qt.NoButton
        pixelAligned: true
        topMargin: 8
        bottomMargin: 8

        // Follows the newest message unless the user scrolled up. A resize,
        // such as the composer growing, moves contentY before onHeightChanged
        // runs; that move is not the user's, so it keeps the view pinned.
        property bool pinned: true
        property real laidOutHeight: 0
        function updatePinned() {
            const newest = itemAtIndex(0)
            pinned = count === 0 || newest !== null && newest.y + newest.height - contentY <= height + 1
        }
        onPinnedChanged: if (pinned) view.unseen = 0
        onMovementStarted: {
            wheel.stop()
            pinned = false
        }
        onMovementEnded: updatePinned()
        onContentYChanged: {
            if (!moving && height === laidOutHeight)
                updatePinned()
            view.maybeLoadOlder()
            hover.retarget()
        }
        onHeightChanged: {
            if (pinned)
                positionViewAtBeginning()
            laidOutHeight = height
        }
        onContentHeightChanged: if (pinned && !moving) positionViewAtBeginning()

        delegate: MessageDelegate {
            width: list.width
            isGroup: Conversation.isGroup
            spaceWidth: view.nbsp
            windowWidth: view.Window.width
            onImageClicked: (id, source, w, h) => {
                viewer.active = true
                const v = viewer.item as ImageViewer
                v.show(id, source, w, h)
            }
        }

        ScrollBar.vertical: ScrollLine {}

        // Handlers declared in a ListView sit on its contentItem, so points
        // are mapped from the scene.
        TapHandler {
            acceptedButtons: Qt.RightButton
            onTapped: eventPoint => {
                const p = list.contentItem.mapFromItem(null, eventPoint.scenePosition)
                const item = list.itemAt(p.x, p.y) as MessageDelegate
                if (!menu.offers(item))
                    return
                menu.target = item
                menu.openAt(list.contentItem, p.x, p.y)
            }
        }
        WheelScroll {
            id: wheel
            flickable: list
            onScrolled: list.pinned = false
            onSettled: list.updatePinned()
        }
        // One shared hover chevron instead of one per row.
        HoverHandler {
            id: hover
            property MessageDelegate target: null
            // The bubble under the pointer, also as the list scrolls under it.
            function retarget() {
                if (chevronHover.hovered)
                    return
                if (!hovered) {
                    target = null
                    return
                }
                const p = list.contentItem.mapFromItem(null, point.scenePosition)
                const item = list.itemAt(p.x, p.y) as MessageDelegate
                const b = menu.offers(item) && !item.bare ? item.bubbleItem : null
                target = b && b.contains(b.mapFromItem(null, point.scenePosition)) ? item : null
            }
            onPointChanged: retarget()
            onHoveredChanged: if (!hovered && !chevronHover.hovered) target = null
        }
    }

    // The header's shadow (WhatsApp Web's 0 1px 4px at 12% black) falls on the messages.
    Rectangle {
        anchors.top: header.bottom
        width: parent.width
        height: 5
        gradient: Gradient {
            GradientStop { position: 0; color: Qt.rgba(0, 0, 0, 0.1) }
            GradientStop { position: 0.4; color: Qt.rgba(0, 0, 0, 0.06) }
            GradientStop { position: 1; color: "transparent" }
        }
    }

    // At the top right of the part of the hovered bubble that shows, and only
    // while that part has room for it.
    Item {
        id: chevron
        objectName: "chevron"
        readonly property MessageDelegate row: hover.target
        readonly property Item bubble: row ? row.bubbleItem : null
        readonly property real bubbleY: bubble ? row.y + bubble.y + list.contentItem.y : 0
        readonly property real partTop: Math.max(bubbleY, 0)
        readonly property real partBottom: bubble ? Math.min(bubbleY + bubble.height, list.height) : 0
        visible: bubble !== null && partBottom - partTop >= height + 6
        x: list.x + (bubble ? row.x + bubble.x + bubble.width + list.contentItem.x : 0) - width - 3
        y: list.y + partTop + 3
        width: 26
        height: 22
        Rectangle {
            anchors.fill: parent
            radius: 11
            color: chevron.bubble ? chevron.bubble.color : "transparent"
        }
        Icon {
            anchors.centerIn: parent
            name: "keyboard_arrow_down"
            size: 22
            color: Theme.bubbleMeta
        }
        HoverHandler {
            id: chevronHover
            cursorShape: Qt.PointingHandCursor
        }
        TapHandler {
            onTapped: {
                menu.target = hover.target
                menu.openFrom(chevron, false, hover.target.fromMe)
            }
        }
    }

    FontMetrics {
        id: space
        font.pixelSize: 14
    }

    // Jump to the newest message, with a count of what arrived meanwhile.
    Rectangle {
        anchors.right: list.right
        anchors.bottom: list.bottom
        anchors.rightMargin: 20
        anchors.bottomMargin: 16
        visible: !list.pinned
        width: 42
        height: 42
        radius: 21
        color: Theme.composer
        border.width: 1
        border.color: Theme.bubbleEdge
        Icon {
            anchors.centerIn: parent
            name: "keyboard_arrow_down"
            size: 26
        }
        Rectangle {
            visible: view.unseen > 0
            x: -6
            y: -6
            width: Math.max(20, count.implicitWidth + 10)
            height: 20
            radius: 10
            color: Theme.accent
            Text {
                id: count
                anchors.centerIn: parent
                text: view.unseen > 99 ? "99+" : view.unseen
                color: Theme.accentText
                font.pixelSize: 11
                font.weight: Font.DemiBold
                font.variableAxes: ({ wght: Font.DemiBold })
            }
        }
        MouseArea {
            anchors.fill: parent
            cursorShape: Qt.PointingHandCursor
            onClicked: view.scrollToBottom()
        }
    }

    DropArea {
        id: drop
        anchors.fill: list
        enabled: !Conversation.readOnly
        keys: ["text/uri-list"]
        onDropped: event => {
            Conversation.stageFiles(event.urls, composer.text.trim())
            event.acceptProposedAction()
        }
        Rectangle {
            anchors.fill: parent
            anchors.margins: 16
            visible: drop.containsDrag
            radius: 16
            color: Qt.rgba(Theme.panel.r, Theme.panel.g, Theme.panel.b, 0.9)
            border.width: 2
            border.color: Theme.accent
            Text {
                anchors.centerIn: parent
                text: "Drop to send"
                color: Theme.textMuted
                font.pixelSize: Theme.fontSizeLarge
            }
        }
    }

    Composer {
        id: composer
        anchors.bottom: parent.bottom
        width: parent.width
        spaceAbove: list.height
        onSent: view.scrollToBottom()
    }

    Loader {
        anchors.fill: parent
        active: Conversation.attachments.count > 0
        sourceComponent: MediaPreview {
            onSent: {
                composer.clearDraft()
                view.scrollToBottom()
            }
            onAddRequested: composer.attach(true)
        }
        onActiveChanged: if (!active) composer.focusInput()
    }

    MessageMenu {
        id: menu
        objectName: "messageMenu"
        onReplyRequested: id => {
            Conversation.startReply(id)
            composer.focusInput()
        }
        onCopyRequested: rich => view.copyRich(rich)
        onReactRequested: (id, mine) => reactions.show(id, mine, menu.x, menu.y)
        onDeleteRequested: (id, forEveryone) => {
            deleteConfirm.msgId = id
            deleteConfirm.forEveryone = forEveryone
            deleteConfirm.open()
        }
    }

    // WhatsApp's quick reactions; picking ours again takes it back. + opens
    // every emoji.
    T.Popup {
        id: reactions
        objectName: "reactionPicker"
        property string msgId
        property string mine
        function show(id, current, atX, atY) {
            msgId = id
            mine = current
            x = Math.max(0, Math.min(atX, parent.width - width))
            y = Math.max(0, Math.min(atY, parent.height - height))
            open()
        }
        function pick(emoji) {
            Conversation.react(msgId, emoji === mine ? "" : emoji)
            close()
        }
        function more() {
            reactPanel.msgId = msgId
            reactPanel.mine = mine
            reactPanel.x = Math.max(0, Math.min(x + width - reactPanel.width, parent.width - reactPanel.width))
            reactPanel.y = Math.max(0, Math.min(y, parent.height - reactPanel.height))
            close()
            reactPanel.open()
        }
        parent: T.Overlay.overlay
        popupType: T.Popup.Item
        padding: 6
        background: Rectangle {
            radius: 26
            color: Theme.elevated
            border.width: 1
            border.color: Theme.divider
        }
        // A template Popup has no size of its own: without these it is 0x0
        // and its card doesn't show.
        implicitWidth: implicitContentWidth + leftPadding + rightPadding
        implicitHeight: implicitContentHeight + topPadding + bottomPadding
        contentItem: Item {
            implicitWidth: quick.width
            implicitHeight: quick.height
            Row {
                id: quick
                spacing: 2
                Repeater {
                    model: ["👍", "❤️", "😂", "😮", "😢", "🙏"]
                    delegate: Rectangle {
                        id: cell
                        required property string modelData
                        width: 40
                        height: 40
                        radius: 20
                        color: cell.modelData === reactions.mine ? Theme.selected
                             : mouse.containsMouse ? Theme.hover : "transparent"
                        Text {
                            anchors.centerIn: parent
                            text: cell.modelData
                            textFormat: Text.PlainText
                            font.pixelSize: 24
                        }
                        MouseArea {
                            id: mouse
                            anchors.fill: parent
                            hoverEnabled: true
                            cursorShape: Qt.PointingHandCursor
                            onClicked: reactions.pick(cell.modelData)
                        }
                    }
                }
                Rectangle {
                    objectName: "moreReactions"
                    width: 40
                    height: 40
                    radius: 20
                    color: moreMouse.containsMouse ? Theme.hover : Theme.selected
                    Icon {
                        anchors.centerIn: parent
                        name: "add"
                        size: 22
                    }
                    MouseArea {
                        id: moreMouse
                        anchors.fill: parent
                        hoverEnabled: true
                        cursorShape: Qt.PointingHandCursor
                        onClicked: reactions.more()
                    }
                }
            }
        }
        onClosed: if (!reactPanel.visible) composer.focusInput()
    }

    // Every emoji, to react with any; closes on the first pick.
    EmojiPanel {
        id: reactPanel
        objectName: "reactPanel"
        property string msgId
        property string mine
        parent: T.Overlay.overlay
        popupType: T.Popup.Item
        emojiOnly: true
        width: Math.min(400, view.width - 16)
        height: Math.min(440, view.height - 16)
        closePolicy: T.Popup.CloseOnEscape | T.Popup.CloseOnPressOutside
        onPicked: emoji => {
            Conversation.react(msgId, emoji === mine ? "" : emoji)
            close()
        }
        onClosed: composer.focusInput()
    }

    Sheet {
        id: deleteConfirm
        objectName: "deleteConfirm"
        property string msgId
        property bool forEveryone
        text: forEveryone ? "Delete this message for everyone in the chat?" : "Delete this message for you? Others will still see it."
        acceptText: forEveryone ? "Delete for everyone" : "Delete for me"
        onAccepted: forEveryone ? Conversation.deleteForEveryone(msgId) : Conversation.deleteForMe(msgId)
        onClosed: composer.focusInput()
    }

    // Copies a message's text to the clipboard (rich and plain).
    TextEdit {
        id: copier
        visible: false
        textFormat: TextEdit.RichText
    }

    Loader {
        id: viewer
        active: false
        sourceComponent: ImageViewer {}
    }

    Loader {
        id: player
        active: false
        sourceComponent: VideoViewer {}
    }
    Connections {
        target: Conversation
        function onVideoReady(id: string, url: url, gif: bool) {
            player.active = true
            const v = player.item as VideoViewer
            v.show(id, url, gif)
        }
    }
}
