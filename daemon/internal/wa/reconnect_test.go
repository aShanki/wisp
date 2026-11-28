package wa

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRedialDelay(t *testing.T) {
	var total time.Duration
	for n := 0; n <= 10; n++ {
		total += redialDelay(n)
	}
	if redialDelay(0) != 0 || total != 10*time.Second {
		t.Fatalf("first 11 attempts take %v, want immediate then 1s apart (10s)", total)
	}
	prev := redialDelay(10)
	for n := 11; n < 40; n++ {
		d := redialDelay(n)
		if d < prev || d > 30*time.Second {
			t.Fatalf("redialDelay(%d) = %v after %v", n, d, prev)
		}
		prev = d
	}
	if prev != 30*time.Second {
		t.Fatalf("backoff settles at %v, want 30s", prev)
	}
}

// Without a session there's nothing to reconnect: a resume or a stray
// redial returns at once and never dials.
func TestRedialNeedsSession(t *testing.T) {
	c, err := New(context.Background(), Config{DBPath: filepath.Join(t.TempDir(), "wa.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.resumed()
	c.startRedial()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		running := c.redialKick != nil
		c.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("redial still running without a session")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c.client().IsConnected() {
		t.Fatal("dialled without a session")
	}
}
