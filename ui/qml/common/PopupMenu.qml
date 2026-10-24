// WhatsApp's dropdown: a rounded card of plain rows, drawn in the window.
// openAt() opens it at a point, as a right-click does; openFrom() drops it
// from a button. Where the window ends it flips to the other side.
import QtQuick
import QtQuick.Templates as T
import Wisp

T.Menu {
    id: menu

    property real gap: 4 // between a button and its dropdown
    property rect anchorRect // what it opens beside, in window coordinates
    property bool above
    property bool alignRight

    // Rows that don't apply are left out, as WhatsApp does.
    component Entry: T.MenuItem {
        id: item
        property bool danger: false
        property string iconName // Material Symbol before the text, if any
        visible: enabled
        height: enabled ? implicitHeight : 0
        implicitWidth: 220
        implicitHeight: 40
        leftPadding: 16
        rightPadding: 16
        contentItem: Row {
            spacing: 16
            Icon {
                anchors.verticalCenter: parent.verticalCenter
                visible: item.iconName !== ""
                name: item.iconName
                size: 20
                color: item.danger ? Theme.danger : Theme.icon
            }
            Text {
                width: parent.width - (item.iconName !== "" ? 36 : 0)
                height: parent.height
                text: item.text
                verticalAlignment: Text.AlignVCenter
                elide: Text.ElideRight
                color: item.danger ? Theme.danger : Theme.text
                font.pixelSize: 14
            }
        }
        background: Rectangle {
            radius: 8
            color: item.highlighted ? Theme.hover : "transparent"
        }
    }

    // Opens with its top-left at (x, y) of item.
    function openAt(item, x, y) {
        const p = item.mapToItem(null, x, y)
        show(Qt.rect(p.x, p.y, 0, 0), false, false)
    }
    // Opens below button (above it when up), sharing its left edge (its
    // right edge when alignRight).
    function openFrom(button, up, alignRight) {
        const p = button.mapToItem(null, 0, -gap)
        show(Qt.rect(p.x, p.y, button.width, button.height + 2 * gap), up, alignRight)
    }
    function show(r, up, right) {
        anchorRect = r
        above = up
        alignRight = right
        currentIndex = -1
        open()
    }
    // The first position if the menu fits there, else the second if it fits
    // there, else the first moved as far into the window as it goes.
    function pick(first, second, size, room) {
        const fits = v => v >= 0 && v + size <= room
        return Math.max(0, Math.min(fits(first) || !fits(second) ? first : second, room - size))
    }
    function entriesHeight() {
        let h = 0
        for (let i = 0; i < count; ++i) {
            const item = itemAt(i)
            h += item ? item.height : 0
        }
        return h
    }

    parent: T.Overlay.overlay
    popupType: T.Popup.Item
    x: {
        const start = anchorRect.x, end = anchorRect.x + anchorRect.width - width
        return pick(alignRight ? end : start, alignRight ? start : end, width, parent ? parent.width : 0)
    }
    y: {
        const below = anchorRect.y + anchorRect.height, over = anchorRect.y - height
        return pick(above ? over : below, above ? below : over, height, parent ? parent.height : 0)
    }
    padding: 8
    implicitWidth: Math.max(implicitBackgroundWidth, implicitContentWidth + leftPadding + rightPadding)
    implicitHeight: entriesHeight() + topPadding + bottomPadding

    contentItem: ListView {
        implicitHeight: contentHeight
        model: menu.contentModel
        interactive: false
        currentIndex: menu.currentIndex
    }
    background: Rectangle {
        implicitWidth: 236
        radius: 16
        color: Theme.elevated
        border.width: 1
        border.color: Theme.divider
    }
}
