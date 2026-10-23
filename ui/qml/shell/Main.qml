import QtQuick
import QtQuick.Controls
import QtCore
import Wisp

ApplicationWindow {
    id: window

    // Stays true across daemon restarts, so the window doesn't flash.
    property bool started: false
    readonly property bool ready: Daemon.connected && Daemon.authState !== "unknown"
    readonly property bool linked: Daemon.authState === "paired"
    property string selfJid
    property string selfName
    property bool showSettings: false
    property int restoreVisibility: Window.Windowed

    onVisibilityChanged: function(visibility) {
        if (visibility !== Window.Hidden && visibility !== Window.Minimized)
            restoreVisibility = visibility
    }

    function reveal() {
        if (!visible || visibility === Window.Minimized)
            visibility = restoreVisibility
        raise()
        requestActivate()
    }

    width: geometry.width
    height: geometry.height
    minimumWidth: 760
    minimumHeight: 480
    visibility: Window.Windowed
    title: "Wisp"
    color: Theme.panel

    onReadyChanged: if (ready) started = true
    // Logged out here or unlinked from the phone: the chats are gone.
    onLinkedChanged: {
        if (linked) {
            Daemon.call("auth.status", {}, (result, error) => {
                if (!error) {
                    window.selfJid = result.selfJid
                    window.selfName = result.selfName
                }
            })
        } else {
            Conversation.close()
            chat.active = false
            showSettings = false
        }
    }
    Component.onCompleted: if (geometry.maximized) showMaximized()
    onClosing: {
        geometry.maximized = visibility === Window.Maximized
        if (!geometry.maximized) {
            geometry.width = width
            geometry.height = height
        }
    }

    Settings {
        id: geometry
        category: "window"
        property int width: 1200
        property int height: 760
        property bool maximized: false
    }

    Binding {
        target: Daemon
        property: "windowActive"
        value: window.active
    }

    Connections {
        target: Daemon
        function onOpenChatRequested(jid) {
            window.showSettings = false
            if (jid) { // "" (the tray): just raise the window
                Conversation.open(jid)
                chat.forceActiveFocus()
            }
            window.reveal()
        }
        function onErrorOccurred(message) { toast.show(message) }
    }

    Shortcut {
        sequence: "Ctrl+K"
        enabled: window.linked
        onActivated: {
            window.showSettings = false
            sidebar.focusSearch()
        }
    }

    Text {
        anchors.centerIn: parent
        visible: !window.started
        text: "Starting…"
        color: Theme.textMuted
        font.pixelSize: Theme.fontSize
    }

    Loader {
        anchors.fill: parent
        active: window.started && !window.linked
        sourceComponent: Login {}
    }

    Item {
        anchors.fill: parent
        visible: window.started && window.linked

        NavRail {
            id: rail
            height: parent.height
            selfJid: window.selfJid
            selfName: window.selfName
            settings: window.showSettings
            onChatsClicked: {
                window.showSettings = false
                sidebar.forceActiveFocus()
            }
            onSettingsClicked: window.showSettings = true
        }

        Item {
            id: left
            anchors.left: rail.right
            width: Math.max(Theme.listMinWidth, Math.min(Math.round(window.width * (window.width <= 1024 ? 0.4 : window.width <= 1300 ? 0.35 : 0.3)), 520))
            height: parent.height

            Sidebar {
                id: sidebar
                anchors.fill: parent
                visible: !window.showSettings
                focus: true
                onSettingsRequested: window.showSettings = true
                onLogoutRequested: logoutConfirm.open()
                onChatChosen: chat.forceActiveFocus()
            }

            Loader {
                anchors.fill: parent
                active: window.showSettings
                sourceComponent: SettingsPanel {
                    selfJid: window.selfJid
                    selfName: window.selfName
                    onLogoutRequested: logoutConfirm.open()
                }
            }
        }

        Rectangle {
            id: divider
            anchors.left: left.right
            width: 1
            height: parent.height
            color: Theme.divider
        }

        Rectangle {
            anchors.left: divider.right
            anchors.right: parent.right
            height: parent.height
            color: Theme.conversation

            Item {
                anchors.fill: parent
                visible: Conversation.jid === ""

                Image {
                    anchors.centerIn: parent
                    source: "qrc:/assets/mark.svg"
                    sourceSize: Qt.size(80, 80)
                    opacity: Theme.dark ? 0.3 : 0.25
                }
                Row {
                    anchors.horizontalCenter: parent.horizontalCenter
                    anchors.bottom: parent.bottom
                    anchors.bottomMargin: 40
                    spacing: 6

                    Icon {
                        anchors.verticalCenter: parent.verticalCenter
                        size: 14
                        name: "lock"
                        color: Theme.textMuted
                    }
                    Text {
                        text: "Your personal messages are end-to-end encrypted"
                        color: Theme.textMuted
                        font.pixelSize: 14
                    }
                }
            }

            // Created on first use and kept, so drafts survive closing a chat.
            Loader {
                id: chat
                anchors.fill: parent
                active: false
                visible: Conversation.jid !== ""
                onVisibleChanged: if (visible) active = true
                sourceComponent: ConversationView {}
                Keys.onEscapePressed: sidebar.closeChat()
            }
        }
    }

    CallBar { // the call in progress, centred over the conversation
        id: callBar
        x: divider.x + 1 + Math.round((window.width - divider.x - 1 - width) / 2)
        y: 12
        visible: callId !== "" && window.linked
    }

    CallBanner { // a call ringing, under the call in progress if there is one
        x: divider.x + 1 + Math.round((window.width - divider.x - 1 - width) / 2)
        y: callBar.visible ? callBar.y + callBar.height + 8 : 12
        visible: callId !== "" && window.linked
    }

    Rectangle {
        id: toast

        function show(message) {
            toastText.text = message
            visible = true
            toastTimer.restart()
        }

        visible: false
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.bottom: parent.bottom
        anchors.bottomMargin: 24
        width: Math.min(toastText.implicitWidth + 48, parent.width - 64)
        height: 48
        radius: 8
        color: Theme.dark ? "#eaedee" : "#242626"

        Text {
            id: toastText
            anchors.fill: parent
            anchors.leftMargin: 24
            anchors.rightMargin: 24
            verticalAlignment: Text.AlignVCenter
            elide: Text.ElideRight
            color: Theme.dark ? "#0a1014" : "#ffffff"
            font.pixelSize: Theme.fontSize
        }
        Timer {
            id: toastTimer
            interval: 4000
            onTriggered: toast.visible = false
        }
    }

    Sheet {
        id: logoutConfirm
        text: "Are you sure you want to log out?"
        acceptText: "Log out"
        onAccepted: Daemon.logout()
    }
}
