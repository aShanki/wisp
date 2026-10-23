// A message's dropdown, from its hover chevron or a right click.
import QtQuick
import Wisp

PopupMenu {
    id: menu
    property MessageDelegate target: null
    signal replyRequested(string id)
    signal copyRequested(string richText)

    function canReply(m) { return !m.isDeleted && !Conversation.readOnly }
    function canCopy(m) { return !m.isDeleted && m.richText !== "" }
    function canOpen(m) { return !m.isDeleted && m.hasMedia }
    // Whether m gets a menu at all: deleted and system messages don't.
    function offers(m) { return !!m && !m.isSystem && (canReply(m) || canCopy(m) || canOpen(m)) }

    onClosed: target = null

    PopupMenu.Entry {
        text: "Reply"
        enabled: !!menu.target && menu.canReply(menu.target)
        onTriggered: menu.replyRequested(menu.target.msgId)
    }
    PopupMenu.Entry {
        text: "Copy"
        enabled: !!menu.target && menu.canCopy(menu.target)
        onTriggered: menu.copyRequested(menu.target.richText)
    }
    PopupMenu.Entry {
        text: menu.target && menu.target.mediaSource !== "" ? "Open" : "Download"
        enabled: !!menu.target && menu.canOpen(menu.target)
        onTriggered: Conversation.openMedia(menu.target.msgId)
    }
}
