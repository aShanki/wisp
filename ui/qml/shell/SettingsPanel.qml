// WhatsApp's settings, in place of the chat list: the profile, then
// Notifications, Privacy and Log out. Each section opens its own page.
pragma ComponentBehavior: Bound
import QtQuick
import Wisp

Rectangle {
    id: panel

    property string selfJid
    property string selfName
    property string page: ""   // "", "notifications" or "privacy"
    property var values: ({})

    signal logoutRequested()

    function set(key, value) {
        values = Object.assign({}, values, { [key]: value })
        Daemon.call("settings.set", { key: key, value: value })
    }

    component Entry: Rectangle {
        id: entry
        property alias icon: entryIcon.name
        property alias title: entryTitle.text
        property alias subtitle: entrySubtitle.text
        property bool danger: false
        signal clicked()

        width: parent.width
        height: 64
        color: entryMouse.containsMouse ? Theme.hover : Theme.panel

        Icon {
            id: entryIcon
            x: 24
            anchors.verticalCenter: parent.verticalCenter
            color: entry.danger ? Theme.danger : Theme.icon
        }
        Column {
            x: 72
            width: parent.width - x - 24
            anchors.verticalCenter: parent.verticalCenter
            spacing: 2
            Text {
                id: entryTitle
                width: parent.width
                elide: Text.ElideRight
                color: entry.danger ? Theme.danger : Theme.text
                font.pixelSize: 16
            }
            Text {
                id: entrySubtitle
                width: parent.width
                visible: text !== ""
                elide: Text.ElideRight
                color: Theme.textMuted
                font.pixelSize: 14
            }
        }
        MouseArea {
            id: entryMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: entry.clicked()
        }
    }

    component Toggle: Rectangle {
        id: toggle
        required property string key
        property alias title: toggleTitle.text
        property alias subtitle: toggleSubtitle.text
        readonly property bool checked: panel.values[key] !== false

        width: parent.width
        height: Math.max(64, toggleText.implicitHeight + 28)
        opacity: enabled ? 1 : 0.5
        color: toggleMouse.containsMouse ? Theme.hover : Theme.panel

        Column {
            id: toggleText
            x: 24
            width: track.x - x - 16
            anchors.verticalCenter: parent.verticalCenter
            spacing: 2
            Text {
                id: toggleTitle
                width: parent.width
                wrapMode: Text.Wrap
                color: Theme.text
                font.pixelSize: 16
            }
            Text {
                id: toggleSubtitle
                width: parent.width
                visible: text !== ""
                wrapMode: Text.Wrap
                color: Theme.textMuted
                font.pixelSize: 14
            }
        }
        Rectangle {
            id: track
            anchors.right: parent.right
            anchors.rightMargin: 24
            anchors.verticalCenter: parent.verticalCenter
            width: 36
            height: 20
            radius: 10
            color: toggle.checked ? Theme.accent : Theme.textFaint

            Rectangle {
                x: toggle.checked ? parent.width - width - 3 : 3
                anchors.verticalCenter: parent.verticalCenter
                width: 14
                height: 14
                radius: 7
                color: Theme.dark && toggle.checked ? Theme.accentText : "#ffffff"
            }
        }
        MouseArea {
            id: toggleMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: panel.set(toggle.key, !toggle.checked)
        }
    }

    color: Theme.panel
    // The panel may have closed by the time the answer comes.
    Component.onCompleted: Daemon.call("settings.get", {}, (result, error) => {
        if (!error && panel)
            panel.values = result.settings
    })

    Item {
        id: header
        width: parent.width
        height: Theme.headerHeight

        IconButton {
            id: back
            x: 12
            anchors.verticalCenter: parent.verticalCenter
            visible: panel.page !== ""
            name: "arrow_back"
            onClicked: panel.page = ""
        }
        Text {
            x: panel.page !== "" ? back.x + back.width + 12 : 20
            anchors.verticalCenter: parent.verticalCenter
            text: panel.page === "notifications" ? "Notifications"
                : panel.page === "privacy" ? "Privacy"
                : "Settings"
            color: Theme.text
            font.pixelSize: Theme.fontSizeTitle
            font.weight: Font.Bold
            font.variableAxes: ({ wght: Font.Bold })
        }
    }

    Column {
        anchors.top: header.bottom
        width: parent.width
        visible: panel.page === ""

        Item {
            width: parent.width
            height: 96

            Avatar {
                x: 20
                anchors.verticalCenter: parent.verticalCenter
                size: 64
                name: panel.selfName
                source: panel.selfJid !== "" ? "image://wisp/avatar/" + panel.selfJid : ""
            }
            Column {
                x: 104
                width: parent.width - x - 20
                anchors.verticalCenter: parent.verticalCenter
                spacing: 4
                Text {
                    width: parent.width
                    text: panel.selfName !== "" ? panel.selfName : "WhatsApp"
                    elide: Text.ElideRight
                    color: Theme.text
                    font.pixelSize: 17
                }
                Text {
                    width: parent.width
                    text: panel.selfJid !== "" ? "+" + panel.selfJid.split(/[@:.]/)[0] : ""
                    elide: Text.ElideRight
                    color: Theme.textMuted
                    font.pixelSize: 14
                }
            }
        }
        Rectangle {
            width: parent.width
            height: 1
            color: Theme.divider
        }
        Entry {
            objectName: "settingsNotifications"
            icon: "notifications"
            title: "Notifications"
            subtitle: "Message notifications"
            onClicked: panel.page = "notifications"
        }
        Entry {
            objectName: "settingsPrivacy"
            icon: "lock"
            title: "Privacy"
            subtitle: "Read receipts"
            onClicked: panel.page = "privacy"
        }
        Entry {
            icon: "settings"
            title: "Wisp " + Updater.version
            subtitle: Updater.enabled ? "Automatic update checks are on" : "Automatic update checks are off"
            onClicked: Updater.enabled = !Updater.enabled
        }
        Entry {
            icon: "logout"
            title: "Log out"
            danger: true
            onClicked: panel.logoutRequested()
        }
    }

    Column {
        anchors.top: header.bottom
        width: parent.width
        visible: panel.page === "notifications"

        Toggle {
            key: "notifications.enabled"
            title: "Message notifications"
            subtitle: "Show notifications for new messages"
        }
        Toggle {
            key: "notifications.preview"
            title: "Show previews"
            subtitle: "Show message text in notifications"
            enabled: panel.values["notifications.enabled"] !== false
        }
    }

    Column {
        anchors.top: header.bottom
        width: parent.width
        visible: panel.page === "privacy"

        Toggle {
            key: "privacy.readReceipts"
            title: "Read receipts"
            subtitle: "If turned off, you won't send or receive read receipts. Read receipts are always sent for group chats."
        }
    }
}
