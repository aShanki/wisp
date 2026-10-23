// wispctl talks to wispd from the command line.
//
//	wispctl [--socket PATH] [--mock] call METHOD [JSON]
//	wispctl [--socket PATH] [--mock] events
//	wispctl [--socket PATH] [--mock] ping
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"wisp/daemon/internal/rpc"
)

func main() {
	socket := flag.String("socket", "", "socket path")
	mockMode := flag.Bool("mock", false, "use the mock daemon's default socket")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: wispctl [--socket PATH] [--mock] call METHOD [JSON] | events | ping")
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, "wispctl:", err)
	os.Exit(1)
}
