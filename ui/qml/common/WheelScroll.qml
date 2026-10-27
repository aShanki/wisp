// Wheels and trackpads scroll a Flickable as a browser does; declare it inside
// the Flickable. A mouse notch eases 100 px, or on macOS 40 px a line with
// the system's acceleration (Qt there keeps that only in pixelDelta, at 20 px
// a line). A trackpad swipe follows its pixels and, where the system adds no
// momentum of its own (Wayland), coasts on after the fingers lift.
//
// A MouseArea over the Flickable, not a WheelHandler: a WheelHandler passes on
// a swipe's empty begin and end events, and the Flickable taking the begin
// alone is left mid-drag, holding off its own layout fixes.
import QtQuick

MouseArea {
    id: root
    required property Flickable flickable
    // The user scrolled; and their scroll came to rest.
    signal scrolled()
    signal settled()

    z: 1000
    acceptedButtons: Qt.NoButton
    Component.onCompleted: {
        parent = flickable
        anchors.fill = flickable
    }

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

    // The swipe's recent moves, as [ms, px] pairs, for its speed at lift-off.
    property var samples: []
    property real speed // px/ms, contentY's direction
    readonly property FrameAnimation coast: FrameAnimation {
        onTriggered: {
            const dt = Math.min(frameTime * 1000, 50)
            const was = root.flickable.contentY
            root.flickable.contentY = root.clamped(was + root.speed * dt)
            root.speed *= Math.exp(-dt / 325)
            if (Math.abs(root.speed) < 0.02 || root.flickable.contentY === was) {
                stop()
                root.settled()
            }
        }
    }

    function stop() {
        glide.stop()
        coast.stop()
    }
    function clamped(y) {
        const f = flickable
        const top = f.originY - f.topMargin
        return Math.max(top, Math.min(Math.max(top, f.originY + f.contentHeight + f.bottomMargin - f.height), y))
    }

    onWheel: event => {
        const now = Date.now()
        switch (event.phase) {
        case Qt.ScrollBegin:
            stop()
            samples = []
            return
        case Qt.ScrollEnd: {
            // Moves in the last 100 ms, unless the fingers rested before lifting.
            const recent = samples.filter(s => now - s[0] < 100)
            samples = []
            if (!linesInPixels && recent.length > 1 && now - recent[recent.length - 1][0] < 50) {
                const span = Math.max(now - recent[0][0], 16)
                speed = recent.reduce((sum, s) => sum + s[1], 0) / span
                if (Math.abs(speed) > 0.1) {
                    speed = Math.max(-8, Math.min(8, speed))
                    coast.start()
                    return
                }
            }
            settled()
            return
        }
        case Qt.NoScrollPhase: {
            if (event.angleDelta.y === 0 && event.pixelDelta.y === 0)
                return
            const notch = linesInPixels && event.pixelDelta.y !== 0 ? 2 * event.pixelDelta.y
                : event.angleDelta.y !== 0 ? event.angleDelta.y / 120 * 100 : event.pixelDelta.y
            coast.stop()
            goal = clamped((glide.running ? goal : flickable.contentY) - notch)
            scrolled()
            glide.restart()
            return
        }
        default: {
            // A swipe's update, or macOS's own momentum. Only pixels: Qt fills
            // angleDelta with a rounded-up guess while pixels are under one.
            const step = event.pixelDelta.y
            if (step === 0)
                return
            stop()
            scrolled()
            samples.push([now, -step])
            if (samples.length > 16)
                samples.shift()
            flickable.contentY = clamped(flickable.contentY - step)
        }
        }
    }
}
