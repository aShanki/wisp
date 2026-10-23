// An editable field's right-click menu, as the browser gives WhatsApp Web:
// whichever of undo, redo, cut, copy, paste and select all apply. Built on
// first use. The field turns off Qt's own menu and opens this one:
//     ContextMenu.menu: null
//     ContextMenu.onRequested: position => textMenu.openAt(position.x, position.y)
import QtQuick
import Wisp

Loader {
    id: root

    required property Item editor
    // Paste as the field's Ctrl+V does.
    property var paste: () => editor.paste()

    // Opens at (x, y) of the editor.
    function openAt(x, y) {
        active = true
        item.openAt(editor, x, y)
    }

    active: false
    sourceComponent: PopupMenu {
        PopupMenu.Entry {
            text: "Undo"
            enabled: root.editor.canUndo
            onTriggered: root.editor.undo()
        }
        PopupMenu.Entry {
            text: "Redo"
            enabled: root.editor.canRedo
            onTriggered: root.editor.redo()
        }
        PopupMenu.Entry {
            text: "Cut"
            enabled: root.editor.selectedText !== "" && !root.editor.readOnly
            onTriggered: root.editor.cut()
        }
        PopupMenu.Entry {
            text: "Copy"
            enabled: root.editor.selectedText !== ""
            onTriggered: root.editor.copy()
        }
        PopupMenu.Entry {
            text: "Paste"
            enabled: root.editor.canPaste
            onTriggered: root.paste()
        }
        PopupMenu.Entry {
            text: "Select all"
            enabled: root.editor.length > 0 && root.editor.selectedText.length < root.editor.length
            onTriggered: root.editor.selectAll()
        }
    }
}
