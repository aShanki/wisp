# Wisp

A native WhatsApp client for Linux, built for low memory use. A small Go daemon
(`wispd`, built on [whatsmeow](https://github.com/tulir/whatsmeow)) holds the
session, sends notifications and lives in the tray. A Qt 6 / QML window
(`wisp`) talks to it over a unix socket and can be closed at any time.

Wisp is an unofficial client, not affiliated with WhatsApp or Meta. Using it
may break WhatsApp's terms of service.

## Build (Linux)

Requirements: Go 1.26, Qt 6.9+ (Quick, QuickControls2, Svg, Multimedia), CMake, Ninja, and
a desktop with a notification server, a StatusNotifierItem tray and PipeWire.
`make install` also needs curl, jq and ffmpeg. On Ubuntu 26.04 / Debian:

    sudo apt install golang-go cmake ninja-build g++ \
        qt6-base-dev qt6-declarative-dev qt6-svg-plugins qt6-multimedia-dev \
        qml6-module-qtquick-controls qml6-module-qtquick-dialogs qml6-module-qtmultimedia \
        qml6-module-qtquick-layouts qml6-module-qtquick-templates \
        qml6-module-qtquick-window qml6-module-qtcore \
        qml6-module-qtqml-workerscript curl jq ffmpeg fonts-liberation

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

## Search

Use Ctrl+K to search chats and message text across your synced history. Chat
matches appear immediately; message matches follow after a short typing pause.
Choose a result to jump to the message, or use More results to continue through
older matches. Search ignores case and accents and matches the last word as a
prefix. Ctrl+F (or the search icon in a chat header) searches that conversation.
Back to latest messages returns from an old result to the live conversation.

## Agent access (MCP)

AI agents on the same computer, such as Claude Code, can read your chats and
send messages for you through `wispctl mcp`, a local Model Context Protocol
server on stdio. It is off until you choose a level in Settings → Agent access:

- **Read chats**: list chats, read and search messages, list group members and
  download attachments. Reading never marks anything as read.
- **Read and send**: also send messages and files, react, and mark chats as
  read, as you, to chats you already have, at most 20 a minute.

The same page shows the command for this installation and copies it, or a
JSON `mcpServers` entry. With Claude Code on Linux, for example:

    claude mcp add wisp -- ~/.local/bin/wispctl --socket "$XDG_RUNTIME_DIR/wisp.sock" mcp

On macOS the program is `Wisp.app/Contents/MacOS/wispctl`. The tools are
`get_status`, `list_chats`, `read_messages`, `search_messages`,
`list_group_members`, `get_media`, `send_message`, `send_file`,
`react_to_message` and `mark_chat_read`.

`wispd` enforces the setting on every call (`daemon/internal/api/agent.go`):
an agent connection gets no events and only those methods, and cannot change
settings, link devices, log out, or edit or delete messages. The socket is
private to your user. While Wisp is not running, agents get nothing. The
setting governs the MCP tools; it is not a sandbox against programs that
already run as you, so an agent with unrestricted shell access could still
use `wispctl call` directly. Give such agents shell access with that in mind.

## License

MIT. Vendored and third-party material keeps its own licence:
`ui/src/3rdparty` (qrcodegen, MIT),
`ui/assets/icons` (Material Symbols, Apache-2.0) and `ui/assets/emoji` (gemoji, MIT).

## Updates and manual releases

`VERSION` is the shared version for the app, bundle and Go binaries. `wispd
--version` and `wispctl --version` print the built version. Settings shows the
app version and lets you turn automatic update checks off. Production builds
check GitHub's latest stable release on startup and every six hours, with a
persisted six-hour minimum between requests. Offline checks are silent. Mock
runs and the UI test harness never check; `WISP_UPDATES=0` also disables checks.

An available update offers Install and Later. Install verifies the release's
`SHA256SUMS`, stages the download beside the installation, then replaces the
app after the UI exits. macOS replaces the whole signed bundle and restarts its
LaunchAgent, or opens the bundle directly. Linux replaces the three binaries
in their existing directory and restarts the user daemon service when its
binary changed. Updates need a writable installation directory. They do not
modify the session database or any application data. Failed staging leaves the
running installation intact; a failed replacement keeps its recovery files and
`install.log` in the `.wisp-update-*` directory beside the installation.

Releases are built and uploaded by hand, never by CI. Requirements are the
normal build tools, `gh` authenticated with repository write access, `shasum`,
and on macOS `macdeployqt`, `ditto`, and `codesign`. Linux releases use the host
Qt libraries, so build on the oldest distribution you intend to support.

1. Update `VERSION` to a stable `X.Y.Z`, commit it with the release changes,
   and check out that commit on the build machine.
2. Run `scripts/release.sh --dry-run X.Y.Z`. This builds and packages locally
   under `build/release/`, writes editable notes from commits since the previous
   tag, and makes no tag, push or GitHub release changes.
3. Review `build/release/X.Y.Z/PLATFORM/notes.md`. Run
   `scripts/release.sh X.Y.Z` to build again and publish after confirmation.
   If `EDITOR` is set, the script opens the notes before asking to publish.
4. On the other platform, fetch tags and check out `vX.Y.Z`, then run the same
   script with `X.Y.Z`. It uploads that platform's asset and merges its checksum
   into the existing `SHA256SUMS`. Run the two uploads sequentially so neither
   overwrites the other's checksum manifest.

The assets are `wisp-X.Y.Z-macos-arm64.zip` and
`wisp-X.Y.Z-linux-x86_64.tar.gz`. macOS bundles Qt and uses an ad-hoc signature;
it is not notarized. The script copies optional icon and notification files
from `build/whatsapp/` when present. It does not install the app locally.
On macOS it searches `/opt/homebrew/lib` for split Qt frameworks; set
`QT_LIBDIR` if your Qt libraries are elsewhere. Deployment errors stop the
script before it signs or uploads an incomplete bundle.
Use `RELEASE_BUILD_WRAPPER=/path/to/scripts/lim` to wrap the build on a machine
with a memory budget. On this Mac, put
`/Users/ashank/projects/wisp/build/tools/{cmake,ninja,go}/bin` on `PATH`, and set
`RELEASE_BUILD_WRAPPER=/Users/ashank/projects/wisp/scripts/lim`.

Existing installations need the first updater-enabled release installed by
hand. Later releases can use the in-app update prompt.
