package audio

import (
	"math"
	"testing"
)

func TestToneCadence(t *testing.T) {
	if got := Ringback.sample(Rate / 2); got == 0 {
		t.Fatal("ringback silent half a second in")
	}
	if got := Ringback.sample(2 * Rate); got != 0 {
		t.Fatalf("ringback = %v two seconds in, want silence", got)
	}
	if got := Ringback.sample(Ringback.period() + Rate/2); got == 0 {
		t.Fatal("ringback does not loop")
	}
	for n := range Ringtone.period() {
		if v := Ringtone.sample(n); math.Abs(float64(v)) > toneGain {
			t.Fatalf("sample %d = %v, above the gain", n, v)
		}
	}
}
