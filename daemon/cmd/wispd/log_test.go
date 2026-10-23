package main

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaskNumbers(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{ReplaceAttr: maskNumbers}))
	log.With("jid", "61400000001@s.whatsapp.net").Warn("send to 61400000002:12@s.whatsapp.net failed",
		"err", errors.New("no session for 120363000000000001@g.us"), "n", 1234567)
	out := buf.String()
	for _, leak := range []string{"61400000001", "61400000002", "120363000000000001"} {
		if strings.Contains(out, leak) {
			t.Errorf("%s leaked: %s", leak, out)
		}
	}
	if !strings.Contains(out, "…001@s.whatsapp.net") || !strings.Contains(out, "n=1234567") {
		t.Errorf("masked log = %s", out)
	}
}

func TestCappedLog(t *testing.T) {
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "wispd.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := &capped{f: f}
	line := []byte(strings.Repeat("x", 1000) + "\n")
	for range 3 * maxLogSize / len(line) {
		if _, err := w.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	if st, _ := f.Stat(); st.Size() > maxLogSize || st.Size() == 0 {
		t.Fatalf("log size %d", st.Size())
	}
}
