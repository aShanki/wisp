package rpc

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// SocketPath resolves the socket path: explicit flag value,
// then $WISP_SOCKET, then $XDG_RUNTIME_DIR/wisp.sock (wisp-mock.sock when
// mock). The fallback is Wisp's application support directory on macOS,
// or a private directory under the system temporary directory elsewhere.
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
	if dir == "" && runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Library", "Application Support", "Wisp", "run")
	}
	if dir == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("wisp-%d", os.Getuid()))
	}
	return filepath.Join(dir, name)
}
