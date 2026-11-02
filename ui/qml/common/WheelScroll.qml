// Wheels and trackpads scroll a Flickable as a browser does; declare it inside
// the Flickable. A mouse notch eases 120 px, or on macOS 40 px a line with
// the system's acceleration (Qt there keeps that only in pixelDelta, at 20 px
// a line). A trackpad swipe follows its fingers and then coasts: macOS sends
// the momentum as events, elsewhere (libinput) the Flickable flicks at the
// swipe's last speed. Off macOS a swipe moves as Chromium's does, 12 px per
// compositor pixel, which Qt Wayland already reports as angleDelta.
import QtQuick

WheelHandler {
    id: root
    required property Flickable flickable
    // Qt Wayland sends trackpad swipes from a TouchPad device, which a
    // WheelHandler ignores by default; the Flickable then scrolled them itself.
    acceptedDevices: PointerDevice.Mouse | PointerDevice.TouchPad
    // The user scrolled; and their scroll came to rest.
    signal scrolled()
    signal settled()

    property real goal
    property var swipe: [] // the last 100 ms of [time, pixels] steps
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

    // Qt caps flicks at 2500 px/s, far below a Chromium-speed swipe's coast.
    Component.onCompleted: flickable.maximumFlickVelocity = Math.max(flickable.maximumFlickVelocity, 20000)

    function stop() { glide.stop() }
    function clamped(y) {
        const f = flickable
        const top = f.originY - f.topMargin
        return Math.max(top, Math.min(Math.max(top, f.originY + f.contentHeight + f.bottomMargin - f.height), y))
    }
    // Pixels a second over the swipe's last steps; none if the fingers paused.
    function swipeSpeed(now) {
        const s = swipe.filter(p => now - p[0] <= 100)
        if (s.length < 2 || now - s[s.length - 1][0] > 50)
            return 0
        let d = 0
        for (let i = 1; i < s.length; ++i)
            d += s[i][1]
        return d * 1000 / Math.max(16, s[s.length - 1][0] - s[0][0])
    }

    onWheel: event => {
        const step = linesInPixels && event.pixelDelta.y !== 0 ? event.pixelDelta.y
            : event.angleDelta.y !== 0 ? event.angleDelta.y : 12 * event.pixelDelta.y
        const now = Date.now()
        if (event.phase !== Qt.ScrollEnd && event.phase !== Qt.ScrollMomentum)
            flickable.cancelFlick()
        if (event.phase === Qt.ScrollBegin)
            swipe = []
        if (event.phase === Qt.ScrollEnd) {
            const v = linesInPixels ? 0 : swipeSpeed(now)
            swipe = []
            if (v !== 0)
                flickable.flick(0, v)
            settled()
        } else if (event.phase === Qt.NoScrollPhase) {
            if (step === 0)
                return
            goal = clamped((glide.running ? goal : flickable.contentY) - (linesInPixels && event.pixelDelta.y !== 0 ? 2 * step : step))
            scrolled()
            glide.restart()
        } else if (step !== 0) {
            glide.stop()
            scrolled()
            if (event.phase !== Qt.ScrollMomentum)
                swipe = swipe.filter(p => now - p[0] <= 100).concat([[now, step]])
            flickable.contentY = clamped(flickable.contentY - step)
        }
    }
}
