package mock

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/image/draw"

	"wisp/daemon/internal/api"
)

const (
	fetchLatency = 300 * time.Millisecond
	thumbWidth   = 96
)

// media returns a copy of message id in chat, if it has media.
func (b *Backend) media(chat, id string) (*chat, api.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.chat(chat)
	if err != nil {
		return nil, api.Message{}, err
	}
	i, ok := c.pos(id)
	if !ok {
		return nil, api.Message{}, fmt.Errorf("message %s: %w", id, api.ErrNotFound)
	}
	m := c.at(i)
	if m.Media == nil {
		return nil, api.Message{}, fmt.Errorf("message %s has no media: %w", id, api.ErrNotFound)
	}
	return c, m, nil
}

func (b *Backend) MediaThumb(ctx context.Context, p api.MessageRefParams) (*api.PathResult, error) {
	_, m, err := b.media(p.Chat, p.ID)
	if err != nil || !m.Media.HasThumb {
		return &api.PathResult{}, err
	}
	path := filepath.Join(b.dir, "thumb-"+m.ID+".jpg")
	if _, err := os.Stat(path); err == nil {
		return &api.PathResult{Path: path, Mime: "image/jpeg"}, nil
	}
	w, h := thumbWidth, max(1, thumbWidth*m.Media.Height/max(1, m.Media.Width))
	var img image.Image
	if m.Media.Path != "" { // our own sent image
		src, err := decodeFile(m.Media.Path)
		if err != nil {
			return &api.PathResult{}, nil
		}
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
		img = dst
	} else {
		img = picture(hash(m.ID), w, h)
	}
	if err := writeJPEG(path, img, 60); err != nil {
		return nil, err
	}
	return &api.PathResult{Path: path, Mime: "image/jpeg"}, nil
}

// MediaFetch "downloads" the file after a short delay and emits message.update.
func (b *Backend) MediaFetch(ctx context.Context, p api.MessageRefParams) (*api.PathResult, error) {
	c, m, err := b.media(p.Chat, p.ID)
	if err != nil {
		return nil, err
	}
	if m.Media.Path != "" {
		return &api.PathResult{Path: m.Media.Path, Mime: m.Media.Mime}, nil
	}
	select {
	case <-time.After(fetchLatency):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	path := filepath.Join(b.dir, "media-"+m.ID+filepath.Ext(m.Media.FileName))
	switch m.Type {
	case api.MsgImage:
		path += ".jpg"
		err = writeJPEG(path, picture(hash(m.ID), m.Media.Width, m.Media.Height), 85)
	case api.MsgVoice, api.MsgAudio:
		path += ".wav"
		err = writeFile(path, voice(hash(m.ID), m.Media.Duration))
	default:
		err = writeFile(path, []byte("Wisp mock file: "+m.Media.FileName+"\n"))
	}
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	c.paths[m.ID] = path
	if i, ok := c.pos(m.ID); ok {
		b.emit(api.EMessageUpdate, api.MessageEvent{Message: c.at(i)})
	}
	b.mu.Unlock()
	return &api.PathResult{Path: path, Mime: m.Media.Mime}, nil
}

func (b *Backend) AvatarsGet(ctx context.Context, p api.JIDParams) (*api.PathResult, error) {
	b.mu.Lock()
	c, ok := b.chats[p.JID]
	b.mu.Unlock()
	if !ok || !c.spec.avatar {
		return &api.PathResult{}, nil
	}
	path := filepath.Join(b.dir, fmt.Sprintf("avatar-%d.jpg", c.spec.idx))
	if _, err := os.Stat(path); err != nil {
		if err := writeJPEG(path, picture(c.spec.seed, 128, 128), 80); err != nil {
			return nil, err
		}
	}
	return &api.PathResult{Path: path, Mime: "image/jpeg"}, nil
}

func hash(s string) uint64 {
	h := uint64(1469598103934665603)
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * 1099511628211
	}
	return h
}

// picture draws a deterministic sky-and-sun scene. Coordinates are
// relative, so a thumbnail looks like its full-size image.
func picture(seed uint64, w, h int) *image.RGBA {
	r := rng(seed, 1)
	top, bottom, sun := hsv(r.Float64(), 0.45, 0.95), hsv(r.Float64(), 0.6, 0.45), hsv(r.Float64(), 0.3, 1)
	cx, cy, rad := r.Float64(), 0.2+0.5*r.Float64(), 0.08+0.15*r.Float64()
	horizon := 0.55 + 0.3*r.Float64()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	aspect := float64(w) / float64(h)
	for y := range h {
		fy := float64(y) / float64(h)
		row := lerp(top, bottom, fy)
		if fy > horizon {
			row = lerp(bottom, color.RGBA{20, 30, 40, 255}, (fy-horizon)/(1-horizon))
		}
		for x := range w {
			c := row
			dx, dy := (float64(x)/float64(w)-cx)*aspect, fy-cy
			if fy < horizon && dx*dx+dy*dy < rad*rad {
				c = sun
			}
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = c.R, c.G, c.B, 255
		}
	}
	return img
}

func lerp(a, b color.RGBA, t float64) color.RGBA {
	f := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return color.RGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), 255}
}

func hsv(h, s, v float64) color.RGBA {
	i := math.Floor(h * 6)
	f := h*6 - i
	p, q, t := v*(1-s), v*(1-f*s), v*(1-(1-f)*s)
	var r, g, b float64
	switch int(i) % 6 {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	return color.RGBA{uint8(r * 255), uint8(g * 255), uint8(b * 255), 255}
}

func writeJPEG(path string, img image.Image, quality int) error {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return err
	}
	return writeFile(path, buf.Bytes())
}

// writeFile writes atomically, so concurrent fetches never see a partial file.
func writeFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

func decodeFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// waveform is a voice note's 64 levels (0-100), rising and falling like speech.
func waveform(seed uint64) []byte {
	w := make([]byte, 64)
	for i := range w {
		seed = seed*6364136223846793005 + 1442695040888963407
		w[i] = byte(10 + 40*(1+math.Sin(float64(i)/5+float64(seed%7)))/2 + float64(seed>>59))
	}
	return w
}

// voice renders seconds of 8 kHz 8-bit mono WAV that sounds vaguely like speech.
func voice(seed uint64, seconds int) []byte {
	const rate = 8000
	n := rate * max(1, seconds)
	out := make([]byte, 44+n)
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+n))
	copy(out[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1) // PCM
	binary.LittleEndian.PutUint16(out[22:], 1) // mono
	binary.LittleEndian.PutUint32(out[24:], rate)
	binary.LittleEndian.PutUint32(out[28:], rate)
	binary.LittleEndian.PutUint16(out[32:], 1)
	binary.LittleEndian.PutUint16(out[34:], 8)
	copy(out[36:], "data")
	binary.LittleEndian.PutUint32(out[40:], uint32(n))
	pitch := 110 + float64(seed%90)
	for i := range n {
		t := float64(i) / rate
		env := math.Max(0, math.Sin(2*math.Pi*2.7*t)) * (0.6 + 0.4*math.Sin(2*math.Pi*0.3*t))
		s := math.Sin(2*math.Pi*pitch*t) + 0.4*math.Sin(2*math.Pi*2*pitch*t)
		out[44+i] = uint8(128 + 50*env*s)
	}
	return out
}

// mockStickers is the picker's content: two favourites, then recent ones.
const mockStickers = 8

func stickerID(i int) string { return fmt.Sprintf("%064x", i+1) }

func (b *Backend) StickersList(ctx context.Context) (*api.StickersListResult, error) {
	out := &api.StickersListResult{Stickers: []api.Sticker{}}
	for i := range mockStickers {
		out.Stickers = append(out.Stickers, api.Sticker{ID: stickerID(i), Favorite: i < 2, Width: 512, Height: 512})
	}
	return out, nil
}

func (b *Backend) StickersFetch(ctx context.Context, p api.StickerParams) (*api.PathResult, error) {
	path, err := b.stickerFile(p.ID)
	if err != nil {
		return nil, err
	}
	return &api.PathResult{Path: path, Mime: "image/jpeg"}, nil
}

func (b *Backend) stickerFile(id string) (string, error) {
	for i := range mockStickers {
		if stickerID(i) != id {
			continue
		}
		path := filepath.Join(b.dir, "sticker-"+id[56:]+".jpg")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		return path, writeJPEG(path, picture(hash(id), 256, 256), 80)
	}
	return "", fmt.Errorf("sticker %s: %w", id, api.ErrNotFound)
}

func (b *Backend) MessagesSendSticker(ctx context.Context, p api.SendStickerParams) (*api.Message, error) {
	path, err := b.stickerFile(p.Sticker)
	if err != nil {
		return nil, err
	}
	m := &api.Message{Type: api.MsgSticker,
		Media: &api.MediaInfo{Mime: "image/jpeg", Width: 512, Height: 512, Path: path}}
	return b.send(p.Chat, p.ReplyTo, m)
}
