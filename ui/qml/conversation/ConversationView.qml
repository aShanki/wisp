// The open chat, as in WhatsApp: header, messages on the wallpaper (newest at
// the bottom, paged upwards) and the composer.
pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import Wisp

Rectangle {
    id: view
    color: Theme.conversation

    property var drafts: ({})
    property string draftJid: ""
    property int unseen: 0
    readonly property real nbsp: space.advanceWidth("\u00a0")

    function scrollToBottom() {
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
        cacheBuffer: 600
        boundsBehavior: Flickable.StopAtBounds
        topMargin: 8
        bottomMargin: 8

        // Follows the newest message unless the user scrolled up.
        property bool pinned: true
        function updatePinned() {
            const newest = itemAtIndex(0)
            pinned = count === 0 || newest !== null && newest.y + newest.height - contentY <= height + 1
        }
        onPinnedChanged: if (pinned) view.unseen = 0
        onMovementStarted: pinned = false
        onMovementEnded: updatePinned()
        onContentYChanged: {
            if (!moving)
                updatePinned()
            view.maybeLoadOlder()
            hover.target = null
        }
        onHeightChanged: if (pinned) positionViewAtBeginning()
        onContentHeightChanged: if (pinned && !moving) positionViewAtBeginning()

        delegate: MessageDelegate {
            width: list.width
            isGroup: Conversation.isGroup
            spaceWidth: view.nbsp
            onImageClicked: (source, w, h) => {
                viewer.active = true
                const v = viewer.item as ImageViewer
                v.show(source, w, h)
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
        // One shared hover chevron instead of one per row.
        HoverHandler {
            id: hover
            property MessageDelegate target: null
            onPointChanged: {
                if (chevronHover.hovered)
                    return
                const p = list.contentItem.mapFromItem(null, point.scenePosition)
                const item = list.itemAt(p.x, p.y) as MessageDelegate
                const b = menu.offers(item) && !item.bare ? item.bubbleItem : null
                target = b && b.contains(b.mapFromItem(null, point.scenePosition)) ? item : null
            }
            onHoveredChanged: if (!hovered && !chevronHover.hovered) target = null
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
}
