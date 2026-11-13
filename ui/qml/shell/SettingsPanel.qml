// WhatsApp's settings, in place of the chat list: the profile, then
// Notifications, Privacy, Agent access and Log out. Each section opens its own page.
pragma ComponentBehavior: Bound
import QtQuick
import Wisp

Rectangle {
    id: panel

    property string selfJid
    property string selfName
    property string page: ""   // "", "notifications", "privacy" or "agent"
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

    // One choice of a setting with several values, as a radio row.
    component Choice: Rectangle {
        id: choice
        required property string key
        required property string value
        property bool fallback: false // shown as chosen while the setting is unset
        property alias title: choiceTitle.text
        property alias subtitle: choiceSubtitle.text
        readonly property bool checked: panel.values[key] === value || (fallback && panel.values[key] === undefined)

        width: parent.width
        height: Math.max(64, choiceText.implicitHeight + 28)
        color: choiceMouse.containsMouse ? Theme.hover : Theme.panel

        Rectangle {
            id: ring
            x: 24
            anchors.verticalCenter: parent.verticalCenter
            width: 20
            height: 20
            radius: 10
            color: "transparent"
            border.width: 2
            border.color: choice.checked ? Theme.accent : Theme.icon

            Rectangle {
                anchors.centerIn: parent
                visible: choice.checked
                width: 10
                height: 10
                radius: 5
                color: Theme.accent
            }
        }
        Column {
            id: choiceText
            x: 72
            width: parent.width - x - 24
            anchors.verticalCenter: parent.verticalCenter
            spacing: 2
            Text {
                id: choiceTitle
                width: parent.width
                wrapMode: Text.Wrap
                color: Theme.text
                font.pixelSize: 16
            }
            Text {
                id: choiceSubtitle
                width: parent.width
                visible: text !== ""
                wrapMode: Text.Wrap
                color: Theme.textMuted
                font.pixelSize: 14
            }
        }
        MouseArea {
            id: choiceMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: panel.set(choice.key, choice.value)
        }
    }

    readonly property string agentAccess: values["agent.access"] ?? "off"

    function copy(text) {
        copier.text = text
        copier.selectAll()
        copier.copy()
        copier.text = ""
        copied.restart()
    }

    TextEdit {
        id: copier
        visible: false
        textFormat: TextEdit.PlainText
    }
    Timer {
        id: copied
        interval: 2000
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
                : panel.page === "agent" ? "Agent access"
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
            objectName: "settingsAgent"
            icon: "smart_toy"
            title: "Agent access"
            subtitle: panel.agentAccess === "send" ? "Agents can read chats and send messages"
                : panel.agentAccess === "read" ? "Agents can read chats"
                : "Off"
            onClicked: panel.page = "agent"
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

    Flickable {
        anchors.top: header.bottom
        anchors.bottom: parent.bottom
        width: parent.width
        visible: panel.page === "agent"
        clip: true
        contentHeight: agentPage.height + 24
        boundsBehavior: Flickable.StopAtBounds

        Column {
            id: agentPage
            width: parent.width

            Text {
                x: 24
                width: parent.width - 48
                topPadding: 8
                bottomPadding: 16
                wrapMode: Text.Wrap
                text: "AI agents on this computer, such as Claude Code, can use your WhatsApp through Wisp. They act as you, so only allow what you trust them with."
                color: Theme.textMuted
                font.pixelSize: 14
            }
            Choice {
                objectName: "agentOff"
                key: "agent.access"
                value: "off"
                fallback: true
                title: "Off"
                subtitle: "Agents can't read or send anything"
            }
            Choice {
                objectName: "agentRead"
                key: "agent.access"
                value: "read"
                title: "Read chats"
                subtitle: "Agents can list chats, read and search messages, and open attachments. They can't send anything or mark chats as read."
            }
            Choice {
                objectName: "agentSend"
                key: "agent.access"
                value: "send"
                title: "Read and send"
                subtitle: "Agents can also send messages and files, react and mark chats as read, as you, to chats you already have. At most 20 a minute."
            }
            Rectangle {
                width: parent.width
                height: 1
                color: Theme.divider
            }
            Text {
                x: 24
                width: parent.width - 48
                topPadding: 20
                text: "Set up an agent"
                color: Theme.accentStrong
                font.pixelSize: 14
                font.weight: Font.Medium
                font.variableAxes: ({ wght: Font.Medium })
            }
            Text {
                x: 24
                width: parent.width - 48
                topPadding: 8
                bottomPadding: 12
                wrapMode: Text.Wrap
                text: "Add Wisp to the agent as a local (stdio) MCP server that runs this command. With Claude Code: claude mcp add wisp -- followed by the command."
                color: Theme.textMuted
                font.pixelSize: 14
            }
            Rectangle {
                x: 24
                width: parent.width - 48
                height: command.implicitHeight + 24
                radius: Theme.radius
                color: Theme.field

                TextEdit {
                    id: command
                    objectName: "agentCommand"
                    x: 12
                    y: 12
                    width: parent.width - 24
                    readOnly: true
                    selectByMouse: true
                    wrapMode: TextEdit.WrapAnywhere
                    text: panel.page === "agent" ? Daemon.agentCommand() : ""
                    color: Theme.text
                    selectionColor: Theme.highlight
                    selectedTextColor: Theme.text
                    font.pixelSize: 13
                    font.family: Qt.platform.os === "osx" ? "Menlo" : "monospace"
                }
            }
            Flow {
                x: 24
                width: parent.width - 48
                topPadding: 12
                spacing: 8
                PillButton {
                    text: "Copy command"
                    onClicked: panel.copy(command.text)
                }
                PillButton {
                    text: "Copy JSON config"
                    onClicked: panel.copy(Daemon.agentConfig())
                }
            }
            Text {
                x: 24
                width: parent.width - 48
                topPadding: 8
                visible: copied.running
                text: "Copied"
                color: Theme.accentStrong
                font.pixelSize: 14
            }
            Text {
                x: 24
                width: parent.width - 48
                topPadding: 16
                wrapMode: Text.Wrap
                text: "Only programs running as you on this computer can connect. Agents can't change this setting, link devices or log out, and reading never marks messages as read."
                color: Theme.textMuted
                font.pixelSize: 13
            }
        }
    }
}
