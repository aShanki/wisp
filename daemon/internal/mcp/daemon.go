package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"wisp/daemon/internal/api"
	"wisp/daemon/internal/rpc"
)

var (
	// errNotRunning: nothing answers on the socket.
	errNotRunning = errors.New("wispd is not running")
	// errOutdated: a wispd from before agent access answers there.
	errOutdated = errors.New("wispd is older than this wispctl")
	// errLost: the connection broke after the request went out, so wispd may
	// or may not have acted on it.
	errLost = errors.New("lost the connection to wispd")
	// errUnsent: the connection was already gone; the request never went out.
	errUnsent = errors.New("the connection to wispd was closed")
)

const dialTimeout = 2 * time.Second

// daemon is the agent connection to wispd. It connects on the first call
// and again after wispd restarts; calls may overlap.
type daemon struct {
	socket string

	mu      sync.Mutex
	conn    *daemonConn
	dialing chan struct{} // closed when the dial in progress ends
}

type daemonConn struct {
	net.Conn
	wmu     sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan reply
	next    int64
	dead    bool
}

type reply struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpc.Error      `json:"error"`
}

func (d *daemon) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		d.conn.Close()
	}
}

// call runs method and decodes its result into out (nil to ignore it). It
// reconnects and repeats a request that never went out (wispd restarted
// since the last call), and one that only reads when the connection breaks
// under it. A send whose answer was lost fails with errLost instead: wispd
// may have sent it.
func (d *daemon) call(ctx context.Context, method string, params, out any) error {
	raw, err := d.callOnce(ctx, method, params)
	if (errors.Is(err, errUnsent) || errors.Is(err, errLost) && !api.AgentSends(method)) && ctx.Err() == nil {
		raw, err = d.callOnce(ctx, method, params)
	}
	if errors.Is(err, errUnsent) {
		err = fmt.Errorf("%w: %v", errNotRunning, err)
	}
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (d *daemon) callOnce(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c, err := d.connect(ctx)
	if err != nil {
		return nil, err
	}
	return c.roundTrip(ctx, method, params)
}

// connect returns the live connection, dialing and saying agent.hello when
// there is none.
func (d *daemon) connect(ctx context.Context) (*daemonConn, error) {
	for {
		d.mu.Lock()
		if d.conn != nil && !d.conn.isDead() {
			c := d.conn
			d.mu.Unlock()
			return c, nil
		}
		if d.dialing == nil {
			break
		}
		wait := d.dialing
		d.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan struct{})
	d.dialing = done
	d.mu.Unlock()

	c, err := d.dial(ctx)

	d.mu.Lock()
	d.dialing = nil
	if err == nil {
		d.conn = c
	}
	d.mu.Unlock()
	close(done)
	return c, err
}

func (d *daemon) dial(ctx context.Context) (*daemonConn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	var dialer net.Dialer
	nc, err := dialer.DialContext(ctx, "unix", d.socket)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNotRunning, err)
	}
	c := &daemonConn{Conn: nc, pending: map[int64]chan reply{}}
	go c.read()
	_, err = c.roundTrip(ctx, api.MAgentHello, map[string]string{"client": "wispctl-mcp"})
	if err != nil {
		c.Close()
		var re *rpc.Error
		if errors.As(err, &re) && re.Code == api.CodeMethodNotFound {
			return nil, errOutdated
		}
		if errors.Is(err, errLost) || errors.Is(err, errUnsent) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: no answer to %s", errNotRunning, api.MAgentHello)
		}
		return nil, err
	}
	return c, nil
}

func (c *daemonConn) isDead() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dead
}

// read hands each response to its caller until the connection ends.
func (c *daemonConn) read() {
	r := bufio.NewReaderSize(c.Conn, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			break
		}
		var rep reply
		if json.Unmarshal(line, &rep) != nil || rep.ID == nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[*rep.ID]
		delete(c.pending, *rep.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- rep
		}
	}
	c.Close()
	c.mu.Lock()
	c.dead = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

// roundTrip sends one request and waits for its answer.
func (c *daemonConn) roundTrip(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.dead {
		c.mu.Unlock()
		return nil, errUnsent
	}
	c.next++
	id := c.next
	c.pending[id] = ch
	c.mu.Unlock()

	line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		c.forget(id)
		return nil, err
	}
	c.wmu.Lock()
	c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = c.Write(append(line, '\n'))
	c.wmu.Unlock()
	if err != nil {
		c.forget(id)
		c.Close()
		return nil, fmt.Errorf("%w: %v", errUnsent, err)
	}
	select {
	case rep, ok := <-ch:
		if !ok {
			return nil, errLost
		}
		if rep.Error != nil {
			return nil, rep.Error
		}
		return rep.Result, nil
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	}
}

func (c *daemonConn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}
