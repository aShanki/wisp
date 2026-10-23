package audio

/*
#cgo LDFLAGS: -framework AudioToolbox -framework CoreAudio
#include "audio_darwin.h"
*/
import "C"

import (
	"fmt"
	"sync"
	"time"
	"unsafe"
)

const (
	Rate         = 16000
	FrameSamples = 960
)

func Available() bool { return C.wispAudioAvailable() != 0 }

type Mic struct {
	mu     sync.Mutex
	stream *C.WispAudio
}

func OpenMic() (*Mic, error) {
	var status C.int
	stream := C.wispAudioOpen(1, &status)
	if stream == nil {
		return nil, fmt.Errorf("microphone: CoreAudio status %d", status)
	}
	return &Mic{stream: stream}, nil
}
func (m *Mic) ReadFrame() ([]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stream == nil {
		return nil, nil
	}
	f := make([]float32, FrameSamples)
	if C.wispAudioRead(m.stream, (*C.float)(unsafe.Pointer(&f[0])), FrameSamples) == 0 {
		return nil, nil
	}
	return f, nil
}
func (m *Mic) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stream != nil {
		C.wispAudioClose(m.stream)
		m.stream = nil
	}
	return nil
}

type playback struct {
	stream *C.WispAudio
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

func openPlayback(_ string, fill func([]float32)) (*playback, error) {
	var status C.int
	stream := C.wispAudioOpen(0, &status)
	if stream == nil {
		return nil, fmt.Errorf("speaker: CoreAudio status %d", status)
	}
	p := &playback{stream: stream, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(60 * time.Millisecond)
		defer ticker.Stop()
		frame := make([]float32, FrameSamples)
		for {
			fill(frame)
			C.wispAudioWrite(stream, (*C.float)(unsafe.Pointer(&frame[0])), FrameSamples)
			select {
			case <-p.stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return p, nil
}
func (p *playback) Close() error {
	p.once.Do(func() { close(p.stop); <-p.done; C.wispAudioClose(p.stream) })
	return nil
}

type Speaker struct {
	*playback
	mu  sync.Mutex
	buf []float32
}

func OpenSpeaker() (*Speaker, error) {
	s := &Speaker{}
	p, err := openPlayback("Call", s.fill)
	s.playback = p
	return s, err
}
func (s *Speaker) WriteFrame(f []float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, f...)
	if len(s.buf) > 5*FrameSamples {
		s.buf = append(s.buf[:0], s.buf[len(s.buf)-5*FrameSamples:]...)
	}
	return nil
}
func (s *Speaker) fill(f []float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := copy(f, s.buf)
	s.buf = append(s.buf[:0], s.buf[n:]...)
	clear(f[n:])
}
