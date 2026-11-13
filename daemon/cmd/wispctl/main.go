// wispctl talks to wispd from the command line.
//
//	wispctl [--socket PATH] [--mock] call METHOD [JSON]
//	wispctl [--socket PATH] [--mock] events
//	wispctl [--socket PATH] [--mock] ping
//	wispctl [--socket PATH] [--mock] mcp
//
// mcp serves the Model Context Protocol on stdin and stdout, for a local
// agent host to start. What the agent may do is set in Wisp's settings.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wisp/daemon/internal/mcp"
	"wisp/daemon/internal/rpc"
	"wisp/daemon/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	socket := flag.String("socket", "", "socket path")
	mockMode := flag.Bool("mock", false, "use the mock daemon's default socket")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: wispctl [--socket PATH] [--mock] call METHOD [JSON] | events | ping | mcp")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("wispctl " + version.Version)
		return
	}
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	if args[0] == "mcp" {
		serveMCP(rpc.SocketPath(*socket, *mockMode))
		return
	}
	conn, err := net.Dial("unix", rpc.SocketPath(*socket, *mockMode))
	if err != nil {
		fail(err)
	}
	defer conn.Close()
	r := bufio.NewReaderSize(conn, 64<<10)

	switch args[0] {
	case "call":
		if len(args) < 2 {
			flag.Usage()
			os.Exit(2)
		}
		params := "{}"
		if len(args) > 2 {
			params = args[2]
		}
		res, err := call(conn, r, args[1], json.RawMessage(params))
		if err != nil {
			fail(err)
		}
		fmt.Println(string(res))
	case "ping":
		start := time.Now()
		res, err := call(conn, r, "daemon.hello", json.RawMessage(`{"client":"wispctl"}`))
		if err != nil {
			fail(err)
		}
		fmt.Printf("%s (%s)\n", res, time.Since(start).Round(time.Microsecond))
	case "events":
		if _, err := call(conn, r, "daemon.hello", json.RawMessage(`{"client":"wispctl"}`)); err != nil {
			fail(err)
		}
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				fail(err)
			}
			os.Stdout.Write(line)
		}
	default:
		flag.Usage()
		os.Exit(2)
	}
}

// call sends one request and returns its result, skipping events.
func call(conn net.Conn, r *bufio.Reader, method string, params json.RawMessage) (json.RawMessage, error) {
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, err
	}
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		var resp struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpc.Error      `json:"error"`
		}
		if err := json.Unmarshal(line, &resp); err != nil {
			return nil, err
		}
		if resp.ID == nil {
			continue // event
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// serveMCP runs until the agent host closes stdin or stops it. It starts
// even when wispd is not running, and connects once a tool needs it.
func serveMCP(socket string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if err := mcp.NewServer(os.Stdin, os.Stdout, socket, log).Serve(ctx); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "wispctl:", err)
	os.Exit(1)
}
