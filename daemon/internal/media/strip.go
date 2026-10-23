package media

import (
	"bytes"
	"encoding/binary"
)

// stripMeta drops metadata (Exif with GPS and serial numbers, XMP, IPTC,
// comments, text chunks, anything after the image) from a JPEG or PNG
// without decoding it. Colour data (ICC, Adobe, gamma) stays, and a JPEG's
// Exif orientation is kept in a minimal Exif block. ok is false if the
// stream doesn't parse.
func stripMeta(mime string, data []byte) ([]byte, bool) {
	if mime == "image/png" {
		return stripPNG(data)
	}
	return stripJPEG(data)
}

func stripJPEG(d []byte) ([]byte, bool) {
	if len(d) < 4 || d[0] != 0xFF || d[1] != 0xD8 {
		return nil, false
	}
	out := make([]byte, 0, len(d))
	out = append(out, 0xFF, 0xD8)
	for i := 2; i < len(d); {
		if d[i] != 0xFF {
			return nil, false
		}
		for i < len(d) && d[i] == 0xFF { // fill bytes
			i++
		}
		if i >= len(d) {
			return nil, false
		}
		m := d[i]
		i++
		if m == 0xD9 { // EOI: anything after it (MPF images, motion photos) goes
			return append(out, 0xFF, 0xD9), true
		}
		if m == 0x01 || m >= 0xD0 && m <= 0xD7 { // no length
			out = append(out, 0xFF, m)
			continue
		}
		if i+2 > len(d) {
			return nil, false
		}
		n := int(binary.BigEndian.Uint16(d[i:]))
		if n < 2 || i+n > len(d) {
			return nil, false
		}
		seg, payload := d[i:i+n], d[i+2:i+n]
		i += n
		keep := true
		switch {
		case m == 0xE1:
			keep = false
			if o := exifOrientation(payload); o > 1 {
				out = append(out, orientationExif(o)...)
			}
		case m >= 0xE0 && m <= 0xEF:
			keep = m == 0xE0 && bytes.HasPrefix(payload, []byte("JFIF\x00")) ||
				m == 0xE2 && bytes.HasPrefix(payload, []byte("ICC_PROFILE\x00")) ||
				m == 0xEE && bytes.HasPrefix(payload, []byte("Adobe"))
		case m == 0xFE: // comment
			keep = false
		}
		if keep {
			out = append(out, 0xFF, m)
			out = append(out, seg...)
		}
		if m == 0xDA { // start of scan: entropy-coded data runs to the next marker
			j := i
			for j+1 < len(d) && (d[j] != 0xFF || d[j+1] == 0x00 || d[j+1] >= 0xD0 && d[j+1] <= 0xD7) {
				j++
			}
			if j+1 >= len(d) { // truncated: no EOI
				return append(out, d[i:]...), true
			}
			out = append(out, d[i:j]...)
			i = j
		}
	}
	return out, true
}

// exifOrientation reads the Orientation tag from an APP1 Exif payload (0 if none).
func exifOrientation(p []byte) uint16 {
	if !bytes.HasPrefix(p, []byte("Exif\x00\x00")) || len(p) < 14 {
		return 0
	}
	t := p[6:]
	var bo binary.ByteOrder
	switch string(t[:4]) {
	case "II*\x00":
		bo = binary.LittleEndian
	case "MM\x00*":
		bo = binary.BigEndian
	default:
		return 0
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off+2 > len(t) {
		return 0
	}
	n := int(bo.Uint16(t[off:]))
	for k := range n {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 0
		}
		if bo.Uint16(t[e:]) == 0x0112 && bo.Uint16(t[e+2:]) == 3 {
			if o := bo.Uint16(t[e+8:]); o <= 8 {
				return o
			}
			return 0
		}
	}
	return 0
}

// orientationExif is an APP1 segment holding only the Orientation tag.
func orientationExif(o uint16) []byte {
	b := []byte{0xFF, 0xE1, 0, 34}
	b = append(b, "Exif\x00\x00MM\x00*\x00\x00\x00\x08"...)
	b = append(b, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, byte(o>>8), byte(o), 0, 0)
	return append(b, 0, 0, 0, 0)
}

var pngKeep = map[string]bool{
	"tRNS": true, "gAMA": true, "cHRM": true, "sRGB": true, "iCCP": true, "sBIT": true, "pHYs": true,
}

func stripPNG(d []byte) ([]byte, bool) {
	const sig = "\x89PNG\r\n\x1a\n"
	if !bytes.HasPrefix(d, []byte(sig)) {
		return nil, false
	}
	out := make([]byte, 0, len(d))
	out = append(out, sig...)
	for i := len(sig); i < len(d); {
		if i+12 > len(d) {
			return nil, false
		}
		n := int(binary.BigEndian.Uint32(d[i:]))
		end := i + 12 + n
		if end > len(d) {
			return nil, false
		}
		typ := string(d[i+4 : i+8])
		critical := typ[0]&0x20 == 0
		if critical || pngKeep[typ] {
			out = append(out, d[i:end]...)
		}
		if typ == "IEND" {
			break
		}
		i = end
	}
	return out, true
}
