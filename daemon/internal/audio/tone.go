package audio

import "math"

// Tone is a looped call tone: segments of sound and silence, in milliseconds.
type Tone struct {
	freqs   []float64
	cadence []int // on, off, on, off, ...
}

var (
	// Ringtone: an incoming call is ringing here.
	Ringtone = Tone{freqs: []float64{440, 480}, cadence: []int{400, 200, 400, 2000}}
	// Ringback: an outgoing call is ringing the other side.
	Ringback = Tone{freqs: []float64{425}, cadence: []int{1000, 4000}}
)

const toneGain = 0.15

// Ringer plays a Tone until Close.
type Ringer struct {
	*playback
	t Tone
	n int // samples played
}

func Ring(t Tone) (*Ringer, error) {
	r := &Ringer{t: t}
	p, err := openPlayback("Ringing", r.fill)
	if err != nil {
		return nil, err
	}
	r.playback = p
	return r, nil
}

// fill runs on the stream's goroutine only.
func (r *Ringer) fill(p []float32) {
	for i := range p {
		p[i] = r.t.sample(r.n)
		r.n++
	}
}

func (t Tone) period() int {
	ms := 0
	for _, c := range t.cadence {
		ms += c
	}
	return ms * Rate / 1000
}

func (t Tone) sample(n int) float32 {
	at := n % t.period()
	for i, c := range t.cadence {
		seg := c * Rate / 1000
		if at < seg {
			if i%2 == 1 {
				return 0
			}
			// A 10 ms fade at each edge avoids clicks.
			g := math.Min(1, float64(min(at, seg-at))/(Rate/100))
			v := 0.0
			for _, f := range t.freqs {
				v += math.Sin(2 * math.Pi * f * float64(n) / Rate)
			}
			return float32(toneGain * g * v / float64(len(t.freqs)))
		}
		at -= seg
	}
	return 0
}
