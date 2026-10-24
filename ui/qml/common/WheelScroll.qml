// Wheels and trackpads scroll a Flickable as a browser does; declare it inside
// the Flickable. A mouse notch eases 100 px, or on macOS 40 px a line with
// the system's acceleration (Qt there keeps that only in pixelDelta, at 20 px
// a line). A trackpad swipe, momentum included, follows its pixels.
import QtQuick

WheelHandler {
    id: root
    required property Flickable flickable
    // The user scrolled; and their scroll came to rest.
    signal scrolled()
    signal settled()

    property real goal
    readonly property bool linesInPixels: Qt.platform.os === "osx" || Qt.platform.os === "macos"
    readonly property NumberAnimation glide: NumberAnimation {
        target: root.flickable
        property: "contentY"
        to: root.goal
        duration: 160
        easing.type: Easing.OutCubic
        onFinished: {
            root.flickable.returnToBounds()
            root.settled()
        }
    }

    function stop() { glide.stop() }
    function clamped(y) {
        const f = flickable
        const top = f.originY - f.topMargin
        return Math.max(top, Math.min(Math.max(top, f.originY + f.contentHeight + f.bottomMargin - f.height), y))
    }

    onWheel: event => {
        const step = event.pixelDelta.y !== 0 ? event.pixelDelta.y : event.angleDelta.y / 120 * 100
        if (event.phase === Qt.ScrollEnd) {
            settled()
        } else if (event.phase === Qt.NoScrollPhase) {
            if (event.angleDelta.y === 0 && event.pixelDelta.y === 0)
                return
            const notch = linesInPixels && event.pixelDelta.y !== 0 ? 2 * event.pixelDelta.y
                : event.angleDelta.y !== 0 ? event.angleDelta.y / 120 * 100 : event.pixelDelta.y
            goal = clamped((glide.running ? goal : flickable.contentY) - notch)
            scrolled()
            glide.restart()
        } else if (step !== 0) {
            glide.stop()
            scrolled()
            flickable.contentY = clamped(flickable.contentY - step)
        }
    }
}
