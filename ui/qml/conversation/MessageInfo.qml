// Per-person receipts observed by this installation.
pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Templates as T
import Wisp

T.Popup {
    id: root
    objectName: "messageInfo"
    property string msgId: ""
    property string chat: ""
    property string preview: ""
    property var receipts: []
    property bool loading: false
    property bool failed: false
    function show(id, text) {
        chat = Conversation.jid; msgId = id; preview = text; receipts = []; failed = false; loading = true
        open(); Conversation.watchMessageInfo(id)
    }
    function time(ts) {
        return ts > 0 ? Qt.formatDateTime(new Date(ts * 1000), Qt.DefaultLocaleShortDate) : "Time unavailable"
    }
    readonly property var readPeople: receipts.filter(r => r.read)
    readonly property var deliveredPeople: receipts.filter(r => r.delivered && !r.read)
    Connections {
        target: Conversation
        function onJidChanged() { if (root.visible && Conversation.jid !== root.chat) root.close() }
        function onMessageInfoLoaded(id, list, error) {
            if (id !== root.msgId) return
            root.loading = false; root.failed = error; root.receipts = list
        }
    }
    onClosed: { Conversation.watchMessageInfo(""); msgId = "" }
    parent: T.Overlay.overlay
    popupType: T.Popup.Item
    x: parent ? parent.width - width : 0
    y: 0
    width: Math.min(400, parent ? parent.width : 400)
    height: parent ? parent.height : 600
    padding: 0
    focus: true
    modal: false
    closePolicy: T.Popup.CloseOnEscape
    background: Rectangle { color: Theme.elevated; border.color: Theme.divider; border.width: 1 }
    contentItem: Column {
        spacing: 0
        Row {
            width: parent.width; height: 64; spacing: 16
            Item { width: 4; height: 1 }
            Button { anchors.verticalCenter: parent.verticalCenter; text: "×"; flat: true; onClicked: root.close(); Accessible.name: "Close message info" }
            Text { anchors.verticalCenter: parent.verticalCenter; text: "Message info"; color: Theme.text; font.pixelSize: 18; font.weight: Font.Medium }
        }
        Rectangle { width: parent.width; height: 1; color: Theme.divider }
        Text {
            width: parent.width - 48; x: 24; topPadding: 24; bottomPadding: 24
            text: root.preview || "Message"; textFormat: Text.RichText; wrapMode: Text.Wrap
            color: Theme.text; font.pixelSize: 14; maximumLineCount: 4; elide: Text.ElideRight
        }
        Rectangle { width: parent.width; height: 1; color: Theme.divider }
        ScrollView {
            width: parent.width; height: Math.max(0, parent.height - y); clip: true
            Column {
                width: root.width
                Text { x: 24; width: parent.width - 48; topPadding: 20; bottomPadding: 12; text: root.loading ? "Loading…" : root.failed ? "Couldn't load message info" : "Read by"; color: Theme.textMuted; font.pixelSize: 14 }
                Repeater {
                    model: root.readPeople
                    delegate: ReceiptRow { required property var modelData; person: modelData; detail: "Read: " + root.time(modelData.readAt) + (modelData.delivered ? "\nDelivered: " + root.time(modelData.deliveredAt) : "") }
                }
                Text { x: 24; width: parent.width - 48; visible: !root.loading && !root.failed && root.readPeople.length === 0; text: "No read receipts observed"; color: Theme.textMuted; font.pixelSize: 14; bottomPadding: 12 }
                Rectangle { width: parent.width; height: 1; color: Theme.divider }
                Text { x: 24; width: parent.width - 48; topPadding: 20; bottomPadding: 12; text: "Delivered to"; color: Theme.textMuted; font.pixelSize: 14 }
                Repeater {
                    model: root.deliveredPeople
                    delegate: ReceiptRow { required property var modelData; person: modelData; detail: root.time(modelData.deliveredAt) }
                }
                Text { x: 24; width: parent.width - 48; visible: !root.loading && !root.failed && root.deliveredPeople.length === 0; text: "No delivery receipts observed"; color: Theme.textMuted; font.pixelSize: 14; bottomPadding: 12 }
                Text { x: 24; width: parent.width - 48; topPadding: 20; bottomPadding: 20; wrapMode: Text.Wrap; text: "Only receipts available to Wisp are shown. Some earlier receipts may be unavailable."; color: Theme.textMuted; font.pixelSize: 12 }
            }
        }
    }
    component ReceiptRow: Item {
        id: row
        property var person
        property string detail
        width: root.width; height: Math.max(64, details.implicitHeight + 20)
        Accessible.role: Accessible.StaticText
        Accessible.name: person.name + ", " + detail
        Avatar { x: 24; anchors.verticalCenter: parent.verticalCenter; size: 40; name: row.person.name; source: "image://wisp/avatar/" + row.person.jid; maskColor: Theme.elevated }
        Column {
            id: details
            x: 80; width: parent.width - 104; anchors.verticalCenter: parent.verticalCenter; spacing: 3
            Text { width: parent.width; text: row.person.name; textFormat: Text.PlainText; elide: Text.ElideRight; color: Theme.text; font.pixelSize: 15 }
            Text { width: parent.width; text: row.detail; wrapMode: Text.Wrap; color: Theme.textMuted; font.pixelSize: 12 }
        }
    }
}
