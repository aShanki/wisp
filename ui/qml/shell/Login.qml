// WhatsApp Web's linking page: steps on the left, the QR code on the right,
// or the phone-number code flow.
pragma ComponentBehavior: Bound
import QtQuick
import Wisp

Rectangle {
    id: login

    property bool usePhone: false
    property bool expired: false
    property bool requested: false   // pairPhone sent, waiting for a code
    property string lastState

    function start() {
        expired = false
        requested = false
        Daemon.startQR()
    }

    component Step: Row {
        id: step
        required property int number
        property alias text: stepText.text

        width: parent.width
        spacing: 14

        Rectangle {
            width: 28
            height: 28
            radius: 14
            color: "transparent"
            border.width: 1
            border.color: Theme.textMuted

            Text {
                anchors.centerIn: parent
                text: step.number
                color: Theme.text
                font.pixelSize: 14
            }
        }
        Text {
            id: stepText
            width: parent.width - 42
            anchors.verticalCenter: parent.verticalCenter
            wrapMode: Text.Wrap
            textFormat: Text.StyledText
            color: Theme.text
            font.pixelSize: 17
        }
    }

    component Link: Text {
        id: link
        signal clicked()
        color: Theme.accentStrong
        font.pixelSize: 15
        font.underline: linkMouse.containsMouse

        MouseArea {
            id: linkMouse
            anchors.fill: parent
            anchors.margins: -6
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: link.clicked()
        }
    }

    color: Theme.conversation

    // The daemon re-sends the current code if pairing is already running.
    Component.onCompleted: {
        lastState = Daemon.authState
        start()
    }

    Connections {
        target: Daemon
        function onAuthStateChanged() {
            if (login.lastState === "pairing" && Daemon.authState === "unpaired")
                login.expired = true
            login.lastState = Daemon.authState
        }
        function onPairCodeChanged() { login.requested = false }
        function onErrorOccurred() { login.requested = false }
    }

    Row {
        x: 32
        y: 24
        spacing: 8
        Image {
            anchors.verticalCenter: parent.verticalCenter
            source: "qrc:/assets/mark.svg"
            sourceSize: Qt.size(28, 28)
        }
        Text {
            anchors.verticalCenter: parent.verticalCenter
            text: "Wisp"
            color: Theme.accentStrong
            font.pixelSize: 18
            font.weight: Font.Bold
        }
    }

    Rectangle {
        id: card
        anchors.centerIn: parent
        width: Math.min(parent.width - 64, 920)
        height: content.implicitHeight + 112
        radius: 24
        color: Theme.dark ? Theme.rail : Theme.panel
        border.width: 1
        border.color: Theme.divider

        Item {
            id: content
            anchors.fill: parent
            anchors.margins: 56
            implicitHeight: Math.max(steps.implicitHeight, qr.height)

            Column {
                id: steps
                width: parent.width - qr.width - 56
                spacing: 28

                Text {
                    width: parent.width
                    wrapMode: Text.Wrap
                    text: login.usePhone && Daemon.pairCode !== "" ? "Enter code on phone"
                        : login.usePhone ? "Enter phone number"
                        : "Log into WhatsApp"
                    color: Theme.text
                    font.pixelSize: 32
                    font.weight: Font.Light
                }
                Column {
                    width: parent.width
                    spacing: 20
                    visible: !login.usePhone || Daemon.pairCode !== ""

                    Step { number: 1; text: "Open WhatsApp on your phone" }
                    Step { number: 2; text: "Tap <b>Menu</b> on Android, or <b>Settings</b> on iPhone, and select <b>Linked devices</b>" }
                    Step {
                        number: 3
                        text: login.usePhone ? "Tap <b>Link a device</b>, then <b>Link with phone number instead</b>"
                                             : "Tap <b>Link a device</b> and point your phone at this screen to scan the QR code"
                    }
                    Step {
                        number: 4
                        visible: login.usePhone
                        text: "Enter this code on your phone"
                    }
                }
                Text {
                    width: parent.width
                    visible: login.usePhone && Daemon.pairCode === ""
                    wrapMode: Text.Wrap
                    text: "Enter your phone number with its country code."
                    color: Theme.textMuted
                    font.pixelSize: 15
                }
                Link {
                    text: login.usePhone ? "Log in with QR code" : "Log in with phone number"
                    onClicked: {
                        login.usePhone = !login.usePhone
                        if (login.usePhone)
                            phone.forceActiveFocus()
                    }
                }
            }

            // The QR code, or the phone number and then its code.
            Item {
                id: qr
                anchors.right: parent.right
                width: 264
                height: 264

                Rectangle {
                    anchors.fill: parent
                    visible: !login.usePhone
                    radius: 8
                    color: "#ffffff"
                    border.width: 1
                    border.color: Theme.divider

                    QrCode {
                        anchors.fill: parent
                        anchors.margins: 8
                        visible: !login.expired && text !== ""
                        text: Daemon.qrCode
                    }
                }

                Column {
                    width: parent.width
                    visible: login.usePhone && Daemon.pairCode === "" && !login.expired
                    spacing: 20

                    Field {
                        id: phone
                        width: parent.width
                        implicitHeight: 48
                        horizontalAlignment: TextInput.AlignHCenter
                        placeholderText: "+61 400 000 000"
                        inputMethodHints: Qt.ImhDialableCharactersOnly
                        validator: RegularExpressionValidator { regularExpression: /[+\d\s()-]*/ }
                        font.pixelSize: 18
                        onAccepted: if (next.enabled) next.clicked()
                    }
                    PillButton {
                        id: next
                        anchors.horizontalCenter: parent.horizontalCenter
                        primary: true
                        text: login.requested ? "Requesting…" : "Next"
                        enabled: !login.requested && phone.text.replace(/\D/g, "").length >= 7
                        onClicked: {
                            login.requested = true
                            Daemon.pairPhone(phone.text.replace(/\D/g, ""))
                        }
                    }
                }

                Rectangle {
                    anchors.centerIn: parent
                    visible: login.usePhone && Daemon.pairCode !== "" && !login.expired
                    width: parent.width
                    height: 72
                    radius: 12
                    color: Theme.field

                    Text {
                        anchors.centerIn: parent
                        text: Daemon.pairCode
                        color: Theme.text
                        font.pixelSize: 30
                        font.letterSpacing: 5
                        font.family: Qt.platform.os === "osx" ? "Menlo" : "monospace"
                    }
                }

                PillButton {
                    anchors.centerIn: parent
                    visible: login.expired
                    primary: true
                    text: "Get a new code"
                    onClicked: login.start()
                }
            }
        }
    }
}
