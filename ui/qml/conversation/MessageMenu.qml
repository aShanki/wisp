// A message's dropdown, from its hover chevron or a right click.
import QtQuick
import Wisp

PopupMenu {
    id: menu
    property MessageDelegate target: null
    signal replyRequested(string id)
    signal copyRequested(string richText)
    signal reactRequested(string id, string mine)
    signal deleteRequested(string id, bool forEveryone)

    // Our own messages exist on WhatsApp once sent.
    function sent(m) { return !m.fromMe || (m.status !== "pending" && m.status !== "error") }
    function canReply(m) { return !m.isDeleted && !Conversation.readOnly }
    function canCopy(m) { return !m.isDeleted && m.richText !== "" }
    function canOpen(m) { return !m.isDeleted && m.hasMedia }
    function canCopyImage(m) { return canOpen(m) && (m.type === "image" || m.isSticker) }
    function canReact(m) { return !m.isDeleted && sent(m) }
    function canDeleteForEveryone(m) { return m.fromMe && !m.isDeleted && sent(m) }
    function myReaction(m) {
        const r = m.reactions.find(r => r.mine)
        return r ? r.emoji : ""
    }
    // Whether m gets a menu at all: system messages don't. Any other can be
    // deleted for us.
    function offers(m) { return !!m && !m.isSystem }

    onClosed: target = null

    PopupMenu.Entry {
        text: "Reply"
        iconName: "reply"
        enabled: !!menu.target && menu.canReply(menu.target)
        onTriggered: menu.replyRequested(menu.target.msgId)
    }
    PopupMenu.Entry {
        text: "Copy"
        iconName: "content_copy"
        enabled: !!menu.target && menu.canCopy(menu.target)
        onTriggered: menu.copyRequested(menu.target.richText)
    }
    PopupMenu.Entry {
        text: "React"
        iconName: "add_reaction"
        enabled: !!menu.target && menu.canReact(menu.target)
        onTriggered: menu.reactRequested(menu.target.msgId, menu.myReaction(menu.target))
    }
    PopupMenu.Entry {
        text: "Copy image"
        iconName: "image"
        enabled: !!menu.target && menu.canCopyImage(menu.target)
        onTriggered: Conversation.copyImage(menu.target.msgId)
    }
    PopupMenu.Entry {
        text: menu.target && menu.target.mediaSource !== "" ? "Open" : "Download"
        iconName: text === "Open" ? "open_in_new" : "download"
        enabled: !!menu.target && menu.canOpen(menu.target)
        onTriggered: Conversation.openMedia(menu.target.msgId)
    }
    PopupMenu.Entry {
        text: "Delete for me"
        iconName: "delete"
        enabled: !!menu.target && (!menu.target.fromMe || menu.target.status !== "pending")
        onTriggered: menu.deleteRequested(menu.target.msgId, false)
    }
    PopupMenu.Entry {
        text: "Delete for everyone"
        iconName: "delete"
        enabled: !!menu.target && menu.canDeleteForEveryone(menu.target)
        onTriggered: menu.deleteRequested(menu.target.msgId, true)
    }
}
