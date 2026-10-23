# Wisp

A native WhatsApp client for Linux, built for low memory use. A small Go daemon
(`wispd`, built on [whatsmeow](https://github.com/tulir/whatsmeow)) holds the
session, sends notifications and lives in the tray. A Qt 6 / QML window
(`wisp`) talks to it over a unix socket and can be closed at any time.

Wisp is an unofficial client, not affiliated with WhatsApp or Meta. Using it
may break WhatsApp's terms of service.

## Build (Linux)

Requirements: Go 1.26, Qt 6.9+ (Quick, QuickControls2, Svg), CMake, Ninja, and
a desktop with a notification server, a StatusNotifierItem tray and PipeWire.
`make install` also needs curl, jq and ffmpeg. On Ubuntu 26.04 / Debian:

    sudo apt install golang-go cmake ninja-build g++ \
        qt6-base-dev qt6-declarative-dev qt6-svg-plugins \
        qml6-module-qtquick-controls qml6-module-qtquick-dialogs \
        qml6-module-qtquick-layouts qml6-module-qtquick-templates \
        qml6-module-qtquick-window qml6-module-qtcore \
        qml6-module-qtqml-workerscript pipewire-pulse curl jq ffmpeg fonts-liberation

Then:

    make build        # bin/wispd, bin/wispctl, bin/wisp
    make check        # vet, tests, smoke test and UI test against the mock daemon
    make install      # installs to ~/.local (PREFIX=... for elsewhere)
    make autostart    # optional: start wispd at login, in the tray

`make install` downloads WhatsApp's app icon, notification sound and chat
wallpaper from WhatsApp Web for your local install. They are never committed.

## Run

    wisp              # starts wispd if it isn't running
    make run-mock     # generated chats, no network

Wisp links as a new companion device. On your phone open WhatsApp → Linked
devices → Link a device and scan the QR code, or choose "Log in with phone
number" and type the code Wisp shows.

Data lives in `~/.local/share/wisp`. The log is `~/.local/state/wisp/wispd.log`.
Set `WISP_GPU=1` to render the window on the GPU instead of in software.

## License

MIT. Vendored and third-party material keeps its own licence:
`daemon/third_party/meowcaller` (MIT), `ui/src/3rdparty` (qrcodegen, MIT),
`ui/assets/icons` (Material Symbols, Apache-2.0) and `ui/assets/emoji` (gemoji, MIT).
