// WhatsApp's composer: a rounded field on the wallpaper with the attach menu
// on the left and a send button once there is text. Enter sends, Shift+Enter
// adds a line; a pasted image is sent; a reply shows its quote above the text.
// In groups "@" picks a member to mention.
pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Dialogs
import Wisp

Item {
    id: root
    objectName: "composer"
    property alias text: input.text
    property bool restoring: false
    property real spaceAbove: 0 // for the emoji suggestions and the mention picker
    // The members chosen in the mention picker, per chat: {chat: {name: jid}}.
    property var mentions: ({})
    readonly property var chatMentions: mentions[Conversation.jid] || ({})
    signal sent()

    function setText(t) {
        restoring = true
        input.text = t
        input.cursorPosition = input.length
        restoring = false
    }
    function focusInput() { input.forceActiveFocus() }
    // Replaces the selection, if any, as typing would.
    function insertText(t) {
        if (input.selectedText !== "")
            input.remove(input.selectionStart, input.selectionEnd)
        input.insert(input.cursorPosition, t)
    }
    function clearDraft() {
        setChatMentions({})
        input.clear()
    }
    function setChatMentions(m) {
        const all = Object.assign({}, mentions)
        all[Conversation.jid] = m
        mentions = all
    }
    function send() {
        const t = input.text.trim()
        if (t === "")
            return
        Conversation.send(t, chatMentions)
        setChatMentions({})
        input.clear()
        sent()
    }
    function attach(media) {
        dialog.active = true
        const d = dialog.item as FileDialog
        d.nameFilters = media ? ["Photos and videos (*.jpg *.jpeg *.png *.webp *.gif *.mp4 *.mov *.webm *.3gp)"] : []
        d.open()
    }

    implicitHeight: field.height + 24

    // Grows upwards: a reply's quote sits above the input row, which keeps
    // one line's height and grows to six lines before it scrolls.
    Rectangle {
        id: field
        objectName: "composerField"
        x: 16
        y: 8
        width: root.width - 32
        readonly property bool replying: !!Conversation.replyTo && !Conversation.readOnly
        readonly property real pad: 6
        readonly property real oneLine: fm.height + input.topPadding + input.bottomPadding + 4
        readonly property real rowH: Math.min(Math.max(input.implicitHeight + 4, oneLine), oneLine + 5 * fm.height)
        height: (replying ? quote.y + quote.height : 0) + rowH
        radius: 26
        color: Theme.composer
        border.width: 1
        border.color: Theme.bubbleEdge

        QuoteBlock {
            id: quote
            objectName: "composerQuote"
            visible: field.replying
            x: field.pad
            y: field.pad
            width: cancel.x - x - field.pad
            quote: visible ? Conversation.replyTo : null
            radius: 12
        }
        IconButton {
            id: cancel
            objectName: "cancelReply"
            visible: field.replying
            x: field.width - width - field.pad
            anchors.verticalCenter: quote.verticalCenter
            name: "close"
            onClicked: Conversation.cancelReply()
        }

        // The buttons stay centred on the last line.
        IconButton {
            id: attachButton
            objectName: "attachButton"
            x: field.pad
            y: field.height - Math.round((field.oneLine + height) / 2)
            enabled: !Conversation.readOnly
            name: "add"
            checked: attachMenu.visible
            onClicked: attachMenu.openFrom(attachButton, true, false)
        }
        IconButton {
            id: emojiButton
            objectName: "emojiButton"
            anchors.left: attachButton.right
            y: attachButton.y
            enabled: !Conversation.readOnly
            name: "mood"
            checked: emojiPanel.item?.visible ?? false
            onClicked: {
                emojiPanel.active = true
                const p = emojiPanel.item as EmojiPanel
                if (p.visible)
                    p.close()
                else
                    p.open()
            }
        }

        ScrollView {
            anchors.left: emojiButton.right
            anchors.right: sendButton.left
            anchors.leftMargin: 4
            y: field.height - field.rowH + 2
            height: field.rowH - 4
            ScrollBar.horizontal.policy: ScrollBar.AlwaysOff

            TextArea {
                id: input
                objectName: "composerInput"
                enabled: !Conversation.readOnly
                placeholderText: Conversation.readOnly ? "Only admins can send messages" : "Type a message"
                placeholderTextColor: Theme.textMuted
                color: Theme.text
                wrapMode: TextEdit.Wrap
                textFormat: TextEdit.PlainText
                font.pixelSize: 15
                selectByMouse: true
                selectionColor: Theme.highlight
                selectedTextColor: Theme.text
                leftPadding: 4
                rightPadding: 8
                topPadding: 13
                bottomPadding: 13
                background: null

                onTextChanged: if (!root.restoring && text !== "") Conversation.userTyping()

                Keys.forwardTo: [suggest, mentionPicker]
                Keys.onPressed: event => {
                    if ((event.key === Qt.Key_Return || event.key === Qt.Key_Enter)
                            && !(event.modifiers & Qt.ShiftModifier) && !input.inputMethodComposing) {
                        root.send()
                        event.accepted = true
                    } else if (event.matches(StandardKey.Paste)) {
                        // An image on the clipboard is sent; text pastes as usual.
                        event.accepted = Conversation.stageClipboardImage(input.text.trim())
                    } else if (event.key === Qt.Key_Escape && Conversation.replyTo) {
                        Conversation.cancelReply()
                        event.accepted = true
                    }
                }
            }
        }

        Rectangle {
            id: sendButton
            x: field.width - width - field.pad
            y: attachButton.y
            width: input.text.trim() !== "" ? 40 : 0
            height: 40
            radius: 20
            visible: width > 0
            color: Theme.accent
            Icon {
                anchors.centerIn: parent
                name: "send"
                size: 22
                color: Theme.accentText
            }
            MouseArea {
                anchors.fill: parent
                cursorShape: Qt.PointingHandCursor
                onClicked: root.send()
            }
        }
    }

    EmojiSuggest {
        id: suggest
        input: input
        x: field.x
        anchors.bottom: parent.top
        maxHeight: root.spaceAbove - 8
    }

    MentionPicker {
        id: mentionPicker
        objectName: "mentionPicker"
        input: input
        x: field.x
        width: field.width
        anchors.bottom: parent.top
        maxHeight: root.spaceAbove - 8
        onChosen: (jid, name) => root.setChatMentions(Object.assign({}, root.chatMentions, {[name]: jid}))
    }

    MentionHighlighter {
        textDocument: input.textDocument
        names: Object.keys(root.chatMentions)
        color: Theme.link
    }

    Connections {
        target: Daemon
        function onAuthStateChanged() {
            if (Daemon.authState !== "paired")
                root.mentions = ({})
        }
    }

    FontMetrics {
        id: fm
        font: input.font
    }

    PopupMenu {
        id: attachMenu
        objectName: "attachMenu"
        gap: 8
        PopupMenu.Entry {
            text: "Photos & videos"
            onTriggered: root.attach(true)
        }
        PopupMenu.Entry {
            text: "Document"
            onTriggered: root.attach(false)
        }
    }

    // Made on first use: it reads the whole emoji table.
    Loader {
        id: emojiPanel
        active: false
        sourceComponent: EmojiPanel {
            parent: field
            x: 0
            y: -height - 8
            width: Math.min(field.width, 480)
            height: Math.min(440, root.spaceAbove - 8)
            onPicked: emoji => root.insertText(emoji)
            onStickerPicked: id => Conversation.sendSticker(id)
            onClosed: root.focusInput()
        }
    }

    Loader {
        id: dialog
        active: false
        sourceComponent: FileDialog {
            title: "Send files"
            fileMode: FileDialog.OpenFiles
            onAccepted: {
                Conversation.stageFiles(selectedFiles, input.text.trim())
            }
        }
    }
}
