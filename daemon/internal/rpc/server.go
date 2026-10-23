// Package rpc serves JSON-RPC 2.0 over a unix socket:
// one request per line, each handled in its own goroutine, and events sent
// to every client that has said daemon.hello.
package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"wisp/daemon/internal/api"
)

const (
	maxLine      = 16 << 20 // largest request line
	outQueue     = 2048     // queued lines per client; a client that fills it is dropped
	writeTimeout = 10 * time.Second
	drainTimeout = 3 * time.Second // in-flight requests at disconnect or shutdown
)

// Server is the unix-socket JSON-RPC server. It implements api.Emitter.
type Server struct {
	b   api.Backend
	log *slog.Logger

	mu      sync.RWMutex
	clients map[int64]*client
	nextID  atomic.Int64
	closing atomic.Bool
	wg      sync.WaitGroup // client read loops
}

var _ api.Emitter = (*Server)(nil)

// NewServer makes a server for b and sets itself as b's emitter.
func NewServer(b api.Backend, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{b: b, log: log, clients: map[int64]*client{}}
	b.SetEmitter(s)
	return s
}

var ErrAlreadyRunning = errors.New("another wispd is already running on this socket")

// Listen listens on socketPath with mode 0600. It fails if a daemon already
// answers there and removes a stale socket file.
func Listen(socketPath string) (*net.UnixListener, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, err
	}
	if fi, err := os.Lstat(socketPath); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket; refusing to replace it", socketPath)
		}
		c, err := net.DialTimeout("unix", socketPath, time.Second)
		if err == nil {
			c.Close()
			return nil, fmt.Errorf("%w (%s)", ErrAlreadyRunning, socketPath)
		}
		if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	}
	old := syscall.Umask(0o177) // created 0600, no window for other users
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	ln.SetUnlinkOnClose(true)
	return ln, nil
}

// ServeListener serves ln (from Listen) until ctx is cancelled, then closes
// it. On shutdown in-flight requests finish (bounded), output is flushed and
// the socket file is removed.
func (s *Server) ServeListener(ctx context.Context, ln net.Listener) error {
	s.log.Info("rpc: listening", "socket", ln.Addr().String())
	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()

	var acceptErr error
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if errors.Is(err, net.ErrClosed) {
				acceptErr = err
				break
			}
			s.log.Warn("rpc: accept", "err", err) // e.g. out of file descriptors
			time.Sleep(100 * time.Millisecond)
			continue
		}
		s.startClient(conn)
	}
	ln.Close()
	s.closing.Store(true)

	s.mu.RLock()
	for _, c := range s.clients {
		c.conn.SetReadDeadline(time.Now())
	}
	s.mu.RUnlock()
	s.wg.Wait()
	s.log.Info("rpc: stopped")
	return acceptErr
}

// Emit queues an event for every client past daemon.hello. It never blocks;
// a client whose queue is full is disconnected.
func (s *Server) Emit(event string, params any) {
	line, err := json.Marshal(notification{JSONRPC: "2.0", Method: event, Params: params})
	if err != nil {
		s.log.Error("rpc: marshal event", "event", event, "err", err)
		return
	}
	line = append(line, '\n')
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.clients {
		if !c.hello.Load() {
			continue
		}
		select {
		case c.out <- line:
		default:
			s.log.Warn("rpc: client too slow, disconnecting", "client", c.id)
			c.kill()
		}
	}
}

type client struct {
	id     int64
	conn   net.Conn
	out    chan []byte
	quit   chan struct{} // writer: flush the queue and stop
	dead   chan struct{} // connection unusable; drop output
	wdone  chan struct{} // writer exited
	once   sync.Once
	hello  atomic.Bool
	ctx    context.Context
	cancel context.CancelFunc
	reqs   sync.WaitGroup
}

func (c *client) kill() {
	c.once.Do(func() {
		close(c.dead)
		c.conn.Close()
	})
}

// send queues a response, blocking only the request's goroutine.
func (c *client) send(line []byte) {
	select {
	case c.out <- line:
	case <-c.dead:
	}
}

func (c *client) writeLoop() {
	defer close(c.wdone)
	w := bufio.NewWriterSize(c.conn, 32<<10)
	// drain writes b and whatever else is queued, then flushes once.
	drain := func(b []byte) bool {
		c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		for {
			if _, err := w.Write(b); err != nil {
				return false
			}
			select {
			case b = <-c.out:
				continue
			default:
			}
			return w.Flush() == nil
		}
	}
	for {
		select {
		case b := <-c.out:
			if !drain(b) {
				c.kill()
				return
			}
		case <-c.quit:
			select {
			case b := <-c.out:
				drain(b)
			default:
			}
			return
		case <-c.dead:
			return
		}
	}
}

func (s *Server) startClient(conn net.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &client{
		id:     s.nextID.Add(1),
		conn:   conn,
		out:    make(chan []byte, outQueue),
		quit:   make(chan struct{}),
		dead:   make(chan struct{}),
		wdone:  make(chan struct{}),
		cancel: cancel,
	}
	c.ctx = api.WithClientID(ctx, c.id)
	s.mu.Lock()
	s.clients[c.id] = c
	s.mu.Unlock()
	if s.closing.Load() {
		conn.SetReadDeadline(time.Now())
	}
	s.log.Debug("rpc: client connected", "client", c.id)
	s.wg.Add(1)
	go c.writeLoop()
	go s.readLoop(c)
}

func (s *Server) readLoop(c *client) {
	defer s.wg.Done()
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 0, 16<<10), maxLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		s.handleLine(c, line)
	}
	if err := sc.Err(); err != nil && !s.closing.Load() {
		if errors.Is(err, bufio.ErrTooLong) {
			s.log.Warn("rpc: request line too long, disconnecting", "client", c.id)
		} else if !errors.Is(err, net.ErrClosed) {
			s.log.Debug("rpc: read", "client", c.id, "err", err)
		}
	}

	s.mu.Lock()
	delete(s.clients, c.id)
	s.mu.Unlock()

	// On shutdown, in-flight requests still get their answers.
	if !s.closing.Load() {
		c.cancel()
	}
	waitTimeout(&c.reqs, drainTimeout)
	c.cancel()
	close(c.quit)
	select {
	case <-c.wdone:
	case <-time.After(writeTimeout):
	}
	c.kill()
	s.b.ClientGone(c.id)
	s.log.Debug("rpc: client gone", "client", c.id)
}

func waitTimeout(wg *sync.WaitGroup, d time.Duration) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

var nullID = json.RawMessage("null")

func (s *Server) handleLine(c *client, line []byte) {
	if line[0] == '[' {
		c.send(encodeError(nullID, api.CodeInvalidRequest, "batch requests are not supported"))
		return
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		c.send(encodeError(nullID, api.CodeParseError, "parse error: "+err.Error()))
		return
	}
	id := req.ID
	if id == nil {
		id = nullID
	}
	if req.Method == "" {
		c.send(encodeError(id, api.CodeInvalidRequest, "invalid request: missing method"))
		return
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		c.send(encodeError(id, api.CodeInvalidRequest, "invalid request: jsonrpc must be \"2.0\""))
		return
	}
	c.reqs.Add(1)
	go func() {
		defer c.reqs.Done()
		s.call(c, &req)
	}()
}

func (s *Server) call(c *client, req *request) {
	start := time.Now()
	result, err := s.dispatch(c, req)
	if req.ID == nil { // notification: no response
		return
	}
	var line []byte
	if err != nil {
		code := api.ErrorCode(err)
		if errors.Is(err, api.ErrMethodNotFound) {
			code = api.CodeMethodNotFound
		}
		line = encodeError(req.ID, code, err.Error())
		s.log.Debug("rpc: error", "client", c.id, "method", req.Method, "code", code, "err", err)
	} else {
		if result == nil {
			result = api.OK{OK: true}
		}
		b, merr := json.Marshal(response{JSONRPC: "2.0", ID: req.ID, Result: result})
		if merr != nil {
			s.log.Error("rpc: marshal result", "method", req.Method, "err", merr)
			line = encodeError(req.ID, api.CodeInternal, "internal error: "+merr.Error())
		} else {
			line = append(b, '\n')
		}
		if d := time.Since(start); d > 50*time.Millisecond && req.Method != api.MMediaFetch {
			s.log.Debug("rpc: slow call", "method", req.Method, "took", d)
		}
	}
	c.send(line)
	if err == nil && req.Method == api.MDaemonHello {
		c.hello.Store(true) // events follow the hello response
	}
}

func (s *Server) dispatch(c *client, req *request) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("rpc: handler panic", "method", req.Method, "panic", r)
			result, err = nil, fmt.Errorf("internal error: %v", r)
		}
	}()
	return api.Dispatch(c.ctx, s.b, req.Method, req.Params)
}

func encodeError(id json.RawMessage, code int, msg string) []byte {
	b, _ := json.Marshal(response{JSONRPC: "2.0", ID: id, Error: &Error{Code: code, Message: msg}})
	return append(b, '\n')
}
