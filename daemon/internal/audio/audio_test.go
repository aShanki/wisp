//go:build !darwin

package audio

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// nullSink loads a null sink for this test only; its monitor is a source that
// hears whatever is played into it.
func nullSink(t *testing.T) {
	if !Available() {
		t.Skip("no PulseAudio-protocol server")
	}
	out, err := exec.Command("pactl", "load-module", "module-null-sink", "sink_name=wisp_test_null").Output()
	if err != nil {
		t.Skipf("pactl load-module: %v", err)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command("pactl", "unload-module", id).Run() })
	oldSrc, oldSink := sourceName, sinkName
	sourceName, sinkName = "wisp_test_null.monitor", "wisp_test_null"
	t.Cleanup(func() { sourceName, sinkName = oldSrc, oldSink })
}

// The null sink's monitor carries silence on this PipeWire (pacat into it, parec
// from it: all zeros), so these check that streams open on the chosen devices,
// run at the call's framing and are consumed, not what they sound like.

func TestSpeakerAndMicOnNullSink(t *testing.T) {
	nullSink(t)
	sp, err := OpenSpeaker()
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	mic, err := OpenMic()
	if err != nil {
		t.Fatal(err)
	}
	defer mic.Close()

	frame := make([]float32, FrameSamples)
	for range 10 {
		_ = sp.WriteFrame(frame)
	}
	frames := 0
	for deadline := time.Now().Add(3 * time.Second); frames < 3 && time.Now().Before(deadline); {
		time.Sleep(60 * time.Millisecond)
		for f, _ := mic.ReadFrame(); f != nil; f, _ = mic.ReadFrame() {
			if len(f) != FrameSamples {
				t.Fatalf("frame of %d samples", len(f))
			}
			frames++
		}
	}
	sp.mu.Lock()
	left := len(sp.buf)
	sp.mu.Unlock()
	if frames < 3 || left > queued-FrameSamples {
		t.Fatalf("mic frames %d, speaker still holds %d samples", frames, left)
	}
	if !sp.s.Running() || !mic.s.Running() {
		t.Fatal("streams not running")
	}
}

func TestSpeakerKeepsTheNewest300ms(t *testing.T) {
	sp := &Speaker{}
	for i := range 8 {
		f := make([]float32, FrameSamples)
		f[0] = float32(i)
		_ = sp.WriteFrame(f)
	}
	if len(sp.buf) != queued || sp.buf[0] != 3 {
		t.Fatalf("buffer %d samples starting at frame %v", len(sp.buf), sp.buf[0])
	}
	p := make([]float32, queued+10)
	p[queued] = 1
	sp.fill(p)
	if len(sp.buf) != 0 || p[queued] != 0 {
		t.Fatal("fill did not drain and pad with silence")
	}
}

func TestRingerOpensAndCloses(t *testing.T) {
	nullSink(t)
	r, err := Ring(Ringtone)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if !r.s.Running() {
		t.Fatal("the ringer is not playing")
	}
	_ = r.Close()
	_ = r.Close()
}
