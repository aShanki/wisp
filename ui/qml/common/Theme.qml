// WhatsApp Web's design tokens (its WDS light and dark palettes), following
// the system colour scheme.
pragma Singleton
import QtQuick
import QtCore

QtObject {
    readonly property SystemPalette pal: SystemPalette { colorGroup: SystemPalette.Active }
    readonly property bool dark: Application.styleHints.colorScheme === Qt.ColorScheme.Dark
        || pal.window.hslLightness < 0.5

    // Surfaces
    readonly property color rail: dark ? "#1d1f1f" : "#f7f5f3"          // nav bar
    readonly property color panel: dark ? "#161717" : "#ffffff"         // chat list, headers
    readonly property color conversation: dark ? "#161717" : "#f5f1eb" // chat wallpaper
    readonly property color composer: dark ? "#242626" : "#ffffff"
    readonly property color field: dark ? "#242626" : "#f7f5f3"         // search, inputs
    readonly property color elevated: dark ? "#12181c" : "#ffffff"      // menus, dialogs
    readonly property color hover: dark ? "#242626" : "#f7f5f3"
    readonly property color selected: dark ? "#2d2e2e" : "#f6f5f4"
    readonly property color divider: dark ? Qt.rgba(1, 1, 1, 0.1) : Qt.rgba(0, 0, 0, 0.1)

    // Text
    readonly property color text: dark ? "#fafafa" : "#0a0a0a"
    readonly property color textMuted: dark ? Qt.rgba(1, 1, 1, 0.6) : Qt.rgba(0, 0, 0, 0.6)
    readonly property color textFaint: dark ? "#3e474d" : "#bdbdbd"
    readonly property color icon: dark ? "#aebac1" : "#54656f"
    readonly property color highlight: dark ? "#103529" : "#d9fdd3"     // text selection

    // Accents
    readonly property color accent: dark ? "#21c063" : "#1daa61"        // badges, unread time, switches
    readonly property color accentStrong: dark ? "#21c063" : "#1b8755"  // links, action text
    readonly property color accentText: dark ? "#0a0a0a" : "#ffffff"    // on accent
    readonly property color chip: dark ? "#103529" : "#d9fdd3"          // selected filter chip
    readonly property color chipText: dark ? "#d9fdd3" : "#15603e"
    readonly property color badgeMuted: dark ? Qt.rgba(1, 1, 1, 0.1) : Qt.rgba(0, 0, 0, 0.05)
    readonly property color read: dark ? "#53bdeb" : "#007bfc"          // read ticks
    readonly property color link: dark ? "#53bdeb" : "#1b8755"
    readonly property color danger: dark ? "#fa99a4" : "#b80531"
    readonly property color warning: dark ? "#362c1f" : "#fff7e5"       // banner surface
    readonly property color warningIcon: dark ? "#ffd279" : "#ffb938"

    // Bubbles
    readonly property color bubbleIn: dark ? "#242626" : "#ffffff"
    readonly property color bubbleOut: dark ? "#144d37" : "#d9fdd3"
    readonly property color bubbleSystem: dark ? "#1d1f1f" : Qt.rgba(1, 1, 1, 0.9)
    readonly property color bubbleMeta: dark ? Qt.rgba(1, 1, 1, 0.6) : Qt.rgba(0, 0, 0, 0.6)
    readonly property color bubbleEdge: dark ? Qt.rgba(0, 0, 0, 0.3) : Qt.rgba(0, 0, 0, 0.08)
    readonly property color quote: dark ? Qt.rgba(0, 0, 0, 0.2) : Qt.rgba(0, 0, 0, 0.05)
    readonly property color quoteMine: "#06cf9c"
    // Group sender names, indexed by MessageModel's senderColor (WDS 300 shades on dark).
    readonly property var senderColors: dark
        ? ["#71eb85", "#53a6fd", "#fc9775", "#a791ff", "#fb5061", "#42c7b8", "#ffd279", "#ff72a1"]
        : ["#1b8755", "#007bfc", "#c4532d", "#5e47de", "#b80531", "#02a698", "#c58730", "#ff2e74"]
    // Voice notes: waveform bars, the plain track used without a waveform,
    // and the thumb and mic, green until played, then blue (grey on ours).
    readonly property color voiceBar: dark ? "#636b70" : "#ced0d1"
    readonly property color voiceBarMine: dark ? "#4d8d81" : "#b0ceae"
    readonly property color voiceTrack: dark ? "#343f46" : "#e7e8e9"
    readonly property color voiceTrackMine: dark ? "#176b5b" : "#c5e6c1"
    readonly property color voiceNew: "#09d261"
    readonly property color voiceNewTrack: dark ? "#63cb77" : "#4ada80"
    readonly property color voiceThumbMine: dark ? "#d1d7db" : "#6f8171"
    readonly property color voiceTrackThumbMine: dark ? "#599389" : "#8da78f"
    readonly property color voicePlayed: dark ? "#53bdeb" : "#4fc3f7"
    readonly property color voicePlayedTrack: dark ? "#49a1c8" : "#30b0e8"
    readonly property color voicePlayedMine: dark ? "#43abcd" : "#29afdf"
    readonly property color voiceMicMine: dark ? Qt.rgba(0.914, 0.929, 0.937, 0.4) : Qt.rgba(0.067, 0.106, 0.129, 0.7)
    function senderColor(i) { return i >= 0 ? senderColors[i % senderColors.length] : quoteMine }

    // WhatsApp's doodle tile, if installed (share/wisp/wallpaper-{light,dark}.png); "" draws `conversation`.
    readonly property url wallpaper: StandardPaths.locate(StandardPaths.GenericDataLocation,
        dark ? "wisp/wallpaper-dark.png" : "wisp/wallpaper-light.png")

    // Metrics (pixels)
    readonly property int radius: 8
    readonly property int railWidth: 64
    readonly property int listMinWidth: 340
    readonly property int avatarSize: 49
    readonly property int rowHeight: 72
    readonly property int headerHeight: 64

    // Fonts. WhatsApp Web uses 14.2 px body text, 17 px names, 12 px meta.
    readonly property int fontSize: 14
    readonly property int fontSizeSmall: 12
    readonly property int fontSizeName: 17
    readonly property int fontSizeLarge: 16
    readonly property int fontSizeTitle: 22
    // The system emoji font's advance per pixel of size: large emoji are
    // sized to fill WhatsApp's square emoji images.
    readonly property real emojiAdvance: emojiMetrics.advanceWidth / 100
    readonly property TextMetrics emojiMetrics: TextMetrics { font.pixelSize: 100; text: "\u{1F600}" }
}
