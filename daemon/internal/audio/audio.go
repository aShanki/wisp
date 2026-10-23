// Package audio is a call's sound through the PulseAudio protocol (pipewire-pulse):
// the microphone, the speaker, and the ringing tones. Every stream is opened for one
// call and closed with it, so nothing runs between calls.
//
// Streams are 16 kHz mono float32, the rate meowcaller's codec uses, tagged
// media.role=phone. They prefer PipeWire's echo-cancel source and sink when that
// module is loaded, and also ask for a PulseAudio echo-cancel filter
// (filter.want), which pipewire-pulse ignores.
package audio

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

const (
	// Rate and FrameSamples match meowcaller.SampleRate and FrameSamples.
	Rate         = 16000
	FrameSamples = 960

	// queued bounds the speaker's buffer; older audio is dropped.
	queued = 5 * FrameSamples
)

// The devices tried first; tests point them at a null sink.
var (
	sourceName = "echo-cancel-source"
	sinkName   = "echo-cancel-sink"
)

// Available reports whether a PulseAudio-protocol server socket exists, without
// connecting to it.
func Available() bool {
	if s := os.Getenv("PULSE_SERVER"); s != "" {
		return true
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "pulse", "native"))
	return err == nil
}

func props(p proto.PropList, name string) {
	p["media.role"] = proto.PropListString("phone")
	p["filter.want"] = proto.PropListString("echo-cancel")
	p["media.name"] = proto.PropListString(name)
}

func connect() (*pulse.Client, error) {
	return pulse.NewClient(pulse.ClientApplicationName("Wisp"), pulse.ClientApplicationIconName("wisp"))
}

// Mic captures the microphone in 60 ms frames for meowcaller (an AudioSource).
// ReadFrame never blocks: it returns the oldest captured frame, or nil (silence).
type Mic struct {
	c      *pulse.Client
	s      *pulse.RecordStream
	frames chan []float32

	mu   sync.Mutex
	part []float32
	once sync.Once
}

func OpenMic() (*Mic, error) {
	c, err := connect()
	if err != nil {
		return nil, err
	}
	m := &Mic{c: c, frames: make(chan []float32, 4), part: make([]float32, 0, FrameSamples)}
	opts := []pulse.RecordOption{pulse.RecordMono, pulse.RecordSampleRate(Rate),
		pulse.RecordLatency(0.06), pulse.RecordRawOption(func(r *proto.CreateRecordStream) {
			r.ChannelVolumes = proto.ChannelVolumes{proto.VolumeNorm}
			props(r.Properties, "Call microphone")
		})}
	if src, err := c.SourceByID(sourceName); err == nil {
		opts = append(opts, pulse.RecordSource(src))
	}
	m.s, err = c.NewRecord(pulse.Float32Writer(m.write), opts...)
	if err != nil {
		c.Close()
		return nil, err
	}
	m.s.Start()
	return m, nil
}

func (m *Mic) write(p []float32) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range p {
		m.part = append(m.part, v)
		if len(m.part) < FrameSamples {
			continue
		}
		select {
		case m.frames <- m.part:
		default: // the call fell behind: drop the oldest frame
			select {
			case <-m.frames:
			default:
			}
			m.frames <- m.part
		}
		m.part = make([]float32, 0, FrameSamples)
	}
	return len(p), nil
}

func (m *Mic) ReadFrame() ([]float32, error) {
	select {
	case f := <-m.frames:
		return f, nil
	default:
		return nil, nil
	}
}

func (m *Mic) Close() error {
	m.once.Do(func() {
		m.s.Close()
		m.c.Close()
	})
	return nil
}

// playback is one output stream fed by fill, which must fill the whole buffer.
type playback struct {
	c    *pulse.Client
	s    *pulse.PlaybackStream
	once sync.Once
}

func openPlayback(name string, fill func([]float32)) (*playback, error) {
	c, err := connect()
	if err != nil {
		return nil, err
	}
	opts := []pulse.PlaybackOption{pulse.PlaybackMono, pulse.PlaybackSampleRate(Rate),
		pulse.PlaybackLatency(0.12), pulse.PlaybackRawOption(func(r *proto.CreatePlaybackStream) {
			r.ChannelVolumes = proto.ChannelVolumes{proto.VolumeNorm}
			props(r.Properties, name)
		})}
	if sink, err := c.SinkByID(sinkName); err == nil {
		opts = append(opts, pulse.PlaybackSink(sink))
	}
	s, err := c.NewPlayback(pulse.Float32Reader(func(p []float32) (int, error) {
		fill(p)
		return len(p), nil
	}), opts...)
	if err != nil {
		c.Close()
		return nil, err
	}
	s.Start()
	return &playback{c: c, s: s}, nil
}

func (p *playback) Close() error {
	p.once.Do(func() {
		p.s.Close()
		p.c.Close()
	})
	return nil
}

// Speaker plays the peer's decoded audio (a meowcaller AudioSink). It pads gaps
// with silence and keeps at most 300 ms queued.
type Speaker struct {
	*playback
	mu  sync.Mutex
	buf []float32
}

func OpenSpeaker() (*Speaker, error) {
	sp := &Speaker{buf: make([]float32, 0, queued)}
	p, err := openPlayback("Call", sp.fill)
	if err != nil {
		return nil, err
	}
	sp.playback = p
	return sp, nil
}

func (sp *Speaker) WriteFrame(f []float32) error {
	sp.mu.Lock()
	sp.buf = append(sp.buf, f...)
	if over := len(sp.buf) - queued; over > 0 {
		sp.buf = append(sp.buf[:0], sp.buf[over:]...)
	}
	sp.mu.Unlock()
	return nil
}

func (sp *Speaker) fill(p []float32) {
	sp.mu.Lock()
	n := copy(p, sp.buf)
	sp.buf = append(sp.buf[:0], sp.buf[n:]...)
	sp.mu.Unlock()
	clear(p[n:])
}
