package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// Handler serves one method. conn identifies the caller connection (for
// subscriptions); params are raw JSON.
type Handler func(ctx context.Context, conn *Conn, params json.RawMessage) (any, error)

// Server serves JSON-RPC on a Unix socket.
type Server struct {
	mu       sync.RWMutex
	handlers map[string]Handler
	ln       net.Listener
	conns    map[*Conn]bool
	wg       sync.WaitGroup
}

// NewServer returns an empty server.
func NewServer() *Server { return &Server{handlers: map[string]Handler{}, conns: map[*Conn]bool{}} }

// Handle registers a method.
func (s *Server) Handle(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// Methods lists registered methods.
func (s *Server) Methods() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.handlers))
	for m := range s.handlers {
		out = append(out, m)
	}
	return out
}

// Listen creates the socket at path with mode 0600 inside a 0700
// directory. A stale socket file is replaced only if nothing answers on it.
func (s *Server) Listen(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		if c, err := net.Dial("unix", path); err == nil {
			_ = c.Close()
			return fmt.Errorf("another daemon is already listening on %s", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	s.ln = ln
	return nil
}

// Serve accepts connections until Close.
func (s *Server) Serve() error {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if err := samePeerUser(c); err != nil {
			_ = c.Close()
			continue
		}
		conn := newConn(c)
		s.mu.Lock()
		s.conns[conn] = true
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serveConn(conn)
			s.mu.Lock()
			delete(s.conns, conn)
			s.mu.Unlock()
		}()
	}
}

// Close stops accepting, closes every connection and waits.
func (s *Server) Close() error {
	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}
	s.mu.Lock()
	for c := range s.conns {
		c.close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return err
}

// Conn is one client connection.
type Conn struct {
	c      net.Conn
	wmu    sync.Mutex
	enc    *json.Encoder
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	onEnd  []func()
}

func newConn(c net.Conn) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &Conn{c: c, enc: json.NewEncoder(c), ctx: ctx, cancel: cancel}
}

// Context ends when the connection closes.
func (c *Conn) Context() context.Context { return c.ctx }

// OnClose registers cleanup (e.g. unsubscribe) for when the client leaves.
func (c *Conn) OnClose(f func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onEnd = append(c.onEnd, f)
}

func (c *Conn) close() {
	c.cancel()
	_ = c.c.Close()
}

func (c *Conn) write(v any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.enc.Encode(v)
}

// Push sends an event notification to this client.
func (c *Conn) Push(ev Event) error {
	return c.write(Notification{JSONRPC: "2.0", Method: "event", Params: ev})
}

func (s *Server) serveConn(c *Conn) {
	defer func() {
		c.close()
		c.mu.Lock()
		ends := c.onEnd
		c.mu.Unlock()
		for _, f := range ends {
			f()
		}
	}()
	sc := bufio.NewScanner(c.c)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var reqs sync.WaitGroup
	defer reqs.Wait()
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = c.write(Response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &Error{Code: CodeParse, Message: err.Error()}})
			continue
		}
		if req.JSONRPC != "2.0" || req.Method == "" {
			_ = c.write(Response{JSONRPC: "2.0", ID: req.ID, Error: &Error{Code: CodeInvalidRequest, Message: "not a JSON-RPC 2.0 request"}})
			continue
		}
		s.mu.RLock()
		h := s.handlers[req.Method]
		s.mu.RUnlock()
		reqs.Add(1)
		go func() {
			defer reqs.Done()
			resp := Response{JSONRPC: "2.0", ID: req.ID}
			if h == nil {
				resp.Error = &Error{Code: CodeMethodNotFound, Message: "unknown method " + req.Method}
			} else {
				res, err := h(c.ctx, c, req.Params)
				if err != nil {
					var ie *Error
					if errors.As(err, &ie) {
						resp.Error = ie
					} else {
						resp.Error = &Error{Code: CodeFailed, Message: err.Error()}
					}
				} else {
					b, err := json.Marshal(res)
					if err != nil {
						resp.Error = &Error{Code: CodeInternal, Message: err.Error()}
					} else {
						resp.Result = b
					}
				}
			}
			if len(req.ID) == 0 {
				return // a notification: no reply
			}
			_ = c.write(resp)
		}()
	}
}

// Params decodes params strictly into v.
func Params(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytesReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &Error{Code: CodeInvalidParams, Message: err.Error()}
	}
	return nil
}
