package rpc

import (
	"fmt"
	"os"
	"path/filepath"
)

// SocketPath resolves the socket path per PROTOCOL.md: explicit flag value,
// then $WISP_SOCKET, then $XDG_RUNTIME_DIR/wisp.sock (wisp-mock.sock when
// mock). Without XDG_RUNTIME_DIR it falls back to /tmp/wisp-<uid>/.
func SocketPath(flagValue string, mock bool) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("WISP_SOCKET"); env != "" {
		return env
	}
	name := "wisp.sock"
	if mock {
		name = "wisp-mock.sock"
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("wisp-%d", os.Getuid()))
	}
	return filepath.Join(dir, name)
}
