// Wheels and trackpads scroll a Flickable as a browser does; declare it inside
// the Flickable. A mouse notch eases 40 px a line with the system's
// acceleration (Qt on macOS keeps that only in pixelDelta, at 20 px a line),
// else 100 px. A trackpad swipe, momentum included, follows its pixels.
import QtQuick

WheelHandler {
    id: root
    required property Flickable flickable
    // The user scrolled; and their scroll came to rest.
    signal scrolled()
    signal settled()

    property real goal
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
        } else if (step === 0) {
            return
        } else if (event.phase === Qt.NoScrollPhase) {
            goal = clamped((glide.running ? goal : flickable.contentY) - (event.pixelDelta.y !== 0 ? 2 * step : step))
            scrolled()
            glide.restart()
        } else {
            glide.stop()
            scrolled()
            flickable.contentY = clamped(flickable.contentY - step)
        }
    }
}
