package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"os"
	"path/filepath"
	"testing"
)

func jpegSeg(marker byte, payload string) []byte {
	b := []byte{0xFF, marker, 0, 0}
	binary.BigEndian.PutUint16(b[2:], uint16(len(payload)+2))
	return append(b, payload...)
}

// exifWithGPS is an Exif payload: orientation 6 plus a GPS pointer and a serial.
func exifWithGPS() string {
	var t bytes.Buffer
	t.WriteString("MM\x00*\x00\x00\x00\x08")
	binary.Write(&t, binary.BigEndian, uint16(2))
	binary.Write(&t, binary.BigEndian, []uint16{0x0112, 3, 0, 1, 6, 0})
	binary.Write(&t, binary.BigEndian, []uint16{0x8825, 4, 0, 1, 0, 38})
	t.WriteString("\x00\x00\x00\x00GPS-SECRET SERIAL-1234")
	return "Exif\x00\x00" + t.String()
}

func pngChunk(typ, data string) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	b = append(b, typ...)
	b = append(b, data...)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(append([]byte(typ), data...)))
}

func TestPrepareStripsMetadata(t *testing.T) {
	c := newCache(t, "", Config{})
	dir := t.TempDir()
	secrets := []string{"GPS-SECRET", "SERIAL", "xmpmeta", "Photoshop", "a comment", "MOTION"}

	raw := genJPEG(t, 40, 20)
	var jp []byte
	jp = append(jp, raw[:2]...)
	jp = append(jp, jpegSeg(0xE1, exifWithGPS())...)
	jp = append(jp, jpegSeg(0xE1, "http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta>")...)
	jp = append(jp, jpegSeg(0xED, "Photoshop 3.0\x00IPTC")...)
	jp = append(jp, jpegSeg(0xFE, "a comment")...)
	jp = append(jp, raw[2:]...)
	jp = append(jp, "MOTION-MP4"...)

	pp := genPNG(t, 30, 10)
	iend := len(pp) - 12
	var pn []byte
	pn = append(pn, pp[:33]...) // signature + IHDR
	pn = append(pn, pngChunk("tEXt", "Comment\x00GPS-SECRET")...)
	pn = append(pn, pngChunk("eXIf", "MM\x00*SERIAL")...)
	pn = append(pn, pngChunk("gAMA", "\x00\x00\xb1\x8f")...)
	pn = append(pn, pp[33:iend]...)
	pn = append(pn, pp[iend:]...)
	pn = append(pn, "MOTION"...)

	for name, data := range map[string][]byte{"a.jpg": jp, "b.png": pn} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		om, err := c.Prepare(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range secrets {
			if bytes.Contains(om.Data, []byte(s)) {
				t.Errorf("%s: %q survived", name, s)
			}
		}
		img, _, err := image.Decode(bytes.NewReader(om.Data))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if b := img.Bounds(); b.Dx() != om.Width || b.Dy() != om.Height || om.Thumb == nil {
			t.Fatalf("%s: %v vs %dx%d", name, b, om.Width, om.Height)
		}
	}

	// The JPEG keeps only its orientation, the PNG its gamma.
	om, _ := c.Prepare(context.Background(), filepath.Join(dir, "a.jpg"))
	if o := exifOrientation(om.Data[6:38]); o != 6 {
		t.Fatalf("orientation %d", o)
	}
	om, _ = c.Prepare(context.Background(), filepath.Join(dir, "b.png"))
	if !bytes.Contains(om.Data, []byte("gAMA")) {
		t.Fatal("png lost gAMA")
	}
}
