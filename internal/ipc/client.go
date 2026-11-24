package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// Client is a JSON-RPC client over the daemon socket. It multiplexes
// concurrent calls and delivers pushed events.
type Client struct {
	c       net.Conn
	enc     *json.Encoder
	wmu     sync.Mutex
	next    atomic.Int64
	mu      sync.Mutex
	pending map[string]chan Response
	events  chan Event
	done    chan struct{}
	err     error
}

// ErrClosed is returned for calls on a closed client.
var ErrClosed = errors.New("ipc connection closed")

// Dial connects to the daemon socket.
func Dial(ctx context.Context, path string) (*Client, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	cl := &Client{c: c, enc: json.NewEncoder(c), pending: map[string]chan Response{}, events: make(chan Event, 1024), done: make(chan struct{})}
	go cl.read()
	return cl, nil
}

func (cl *Client) read() {
	defer close(cl.done)
	defer close(cl.events)
	sc := bufio.NewScanner(cl.c)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		var probe struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		line := sc.Bytes()
		if json.Unmarshal(line, &probe) != nil {
			continue
		}
		if probe.Method == "event" {
			var n Notification
			if json.Unmarshal(line, &n) == nil {
				select {
				case cl.events <- n.Params:
				default: // a slow consumer drops events, never blocks replies
				}
			}
			continue
		}
		var resp Response
		if json.Unmarshal(line, &resp) != nil {
			continue
		}
		cl.mu.Lock()
		ch := cl.pending[string(resp.ID)]
		delete(cl.pending, string(resp.ID))
		cl.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
	cl.mu.Lock()
	cl.err = ErrClosed
	for id, ch := range cl.pending {
		close(ch)
		delete(cl.pending, id)
	}
	cl.mu.Unlock()
}

// Events delivers pushed notifications after events.subscribe.
func (cl *Client) Events() <-chan Event { return cl.events }

// Close closes the connection.
func (cl *Client) Close() error {
	err := cl.c.Close()
	<-cl.done
	return err
}

// Call invokes method with params and decodes the result into out.
func (cl *Client) Call(ctx context.Context, method string, params, out any) error {
	id := strconv.FormatInt(cl.next.Add(1), 10)
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan Response, 1)
	cl.mu.Lock()
	if cl.err != nil {
		cl.mu.Unlock()
		return cl.err
	}
	cl.pending[id] = ch
	cl.mu.Unlock()
	cl.wmu.Lock()
	err = cl.enc.Encode(Request{JSONRPC: "2.0", ID: json.RawMessage(id), Method: method, Params: raw})
	cl.wmu.Unlock()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		cl.mu.Lock()
		delete(cl.pending, id)
		cl.mu.Unlock()
		return ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	}
}
