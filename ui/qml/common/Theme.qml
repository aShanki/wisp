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
    readonly property color accentText: dark ? "#0a1014" : "#ffffff"    // on accent
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
}
