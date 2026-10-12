package codeanim

// A minimal Chrome DevTools Protocol client: one WebSocket to the browser
// (ws://127.0.0.1 only, so the handshake is written by hand on a plain TCP
// connection), flat sessions for the pages (Target.attachToTarget with
// flatten), request/response matching by id and per-session event handlers.
// Only what the renderer needs; no third-party module.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const wsMaxMessage = 256 << 20 // CDP replies can carry big strings

// ---- WebSocket (RFC 6455, client side) ---------------------------------------------------------

type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex
}

func wsDial(ctx context.Context, rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "ws" {
		return nil, fmt.Errorf("devtools: unexpected url %q", rawURL)
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("devtools: refusing non-loopback endpoint %q", u.Host)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	key := base64.StdEncoding.EncodeToString(nonce)
	path := u.RequestURI()
	req := "GET " + path + " HTTP/1.1\r\nHost: " + u.Host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := io.WriteString(conn, req); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(conn, 1<<20)
	res, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if res.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, fmt.Errorf("devtools handshake: HTTP %d", res.StatusCode)
	}
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if res.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(h[:]) {
		conn.Close()
		return nil, errors.New("devtools handshake: bad accept key")
	}
	_ = conn.SetDeadline(time.Time{})
	return &wsConn{conn: conn, br: br}, nil
}

func (c *wsConn) write(op byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	n := len(payload)
	hdr := make([]byte, 0, 14)
	hdr = append(hdr, 0x80|op)
	switch {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 0x80|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	hdr = append(hdr, mask[:]...)
	buf := make([]byte, len(hdr)+n)
	copy(buf, hdr)
	body := buf[len(hdr):]
	for i := 0; i < n; i++ {
		body[i] = payload[i] ^ mask[i&3]
	}
	_, err := c.conn.Write(buf)
	return err
}

// read returns the next text/binary message; pings are answered.
func (c *wsConn) read() ([]byte, error) {
	var msg []byte
	started := false
	for {
		var h [2]byte
		if _, err := io.ReadFull(c.br, h[:]); err != nil {
			return nil, err
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0F
		masked := h[1]&0x80 != 0
		n := uint64(h[1] & 0x7F)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(c.br, b[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(c.br, b[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n > wsMaxMessage || uint64(len(msg))+n > wsMaxMessage {
			return nil, errors.New("devtools: message too large")
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(c.br, mask[:]); err != nil {
				return nil, err
			}
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.br, payload); err != nil {
			return nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i&3]
			}
		}
		switch op {
		case 0x9: // ping
			_ = c.write(0xA, payload)
			continue
		case 0xA:
			continue
		case 0x8:
			return nil, io.EOF
		case 0x1, 0x2:
			msg, started = payload, true
		case 0x0:
			if !started {
				return nil, errors.New("devtools: unexpected continuation frame")
			}
			msg = append(msg, payload...)
		default:
			return nil, fmt.Errorf("devtools: unknown opcode %d", op)
		}
		if fin {
			return msg, nil
		}
	}
}

func (c *wsConn) close() { _ = c.conn.Close() }

// ---- CDP ----------------------------------------------------------------------------------------

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

func (e *cdpError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("devtools: %s (%s)", e.Message, e.Data)
	}
	return "devtools: " + e.Message
}

type cdpMsg struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

type cdpConn struct {
	ws *wsConn

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan cdpMsg
	handlers map[string]func(method string, params json.RawMessage) // by session id
	err      error

	done chan struct{}
}

func newCDP(ws *wsConn) *cdpConn {
	c := &cdpConn{ws: ws, pending: map[int64]chan cdpMsg{}, handlers: map[string]func(string, json.RawMessage){}, done: make(chan struct{})}
	go c.loop()
	return c
}

func (c *cdpConn) loop() {
	var err error
	for {
		var b []byte
		b, err = c.ws.read()
		if err != nil {
			break
		}
		var m cdpMsg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		c.mu.Lock()
		if m.ID != 0 {
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		h := c.handlers[m.SessionID]
		c.mu.Unlock()
		if h != nil && m.Method != "" {
			h(m.Method, m.Params)
		}
	}
	c.mu.Lock()
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		err = errors.New("браузер закрився")
	}
	c.err = err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.done)
}

// call sends method (in session sid, "" = the browser) and decodes the
// result into out (may be nil).
func (c *cdpConn) call(ctx context.Context, sid, method string, params any, out any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	ch := make(chan cdpMsg, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()
	b, err := json.Marshal(cdpMsg{ID: id, Method: method, Params: raw, SessionID: sid})
	if err != nil {
		return err
	}
	if err := c.ws.write(0x1, b); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			c.mu.Lock()
			err := c.err
			c.mu.Unlock()
			return err
		}
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

func (c *cdpConn) setHandler(sid string, h func(method string, params json.RawMessage)) {
	c.mu.Lock()
	if h == nil {
		delete(c.handlers, sid)
	} else {
		c.handlers[sid] = h
	}
	c.mu.Unlock()
}

func (c *cdpConn) close() { c.ws.close() }

// ---- Runtime helpers ----------------------------------------------------------------------------

type remoteObject struct {
	Type        string          `json:"type"`
	Value       json.RawMessage `json:"value"`
	Description string          `json:"description"`
}

type exceptionDetails struct {
	Text       string        `json:"text"`
	LineNumber int           `json:"lineNumber"`
	URL        string        `json:"url"`
	Exception  *remoteObject `json:"exception"`
}

func (e *exceptionDetails) String() string {
	if e.Exception != nil && e.Exception.Description != "" {
		return e.Exception.Description
	}
	if e.Exception != nil && len(e.Exception.Value) > 0 {
		return string(e.Exception.Value)
	}
	s := e.Text
	if e.URL != "" {
		s += fmt.Sprintf(" (%s:%d)", e.URL, e.LineNumber+1)
	}
	return s
}

// jsError is an exception thrown by page code.
type jsError struct{ msg string }

func (e *jsError) Error() string { return e.msg }

// eval runs expr in session sid (awaiting a promise) and decodes the
// returned value into out (may be nil).
func (c *cdpConn) eval(ctx context.Context, sid, expr string, out any) error {
	var r struct {
		Result           remoteObject      `json:"result"`
		ExceptionDetails *exceptionDetails `json:"exceptionDetails"`
	}
	err := c.call(ctx, sid, "Runtime.evaluate", map[string]any{
		"expression": expr, "awaitPromise": true, "returnByValue": true,
	}, &r)
	if err != nil {
		return err
	}
	if r.ExceptionDetails != nil {
		return &jsError{strings.TrimSpace(r.ExceptionDetails.String())}
	}
	if out != nil && len(r.Result.Value) > 0 {
		return json.Unmarshal(r.Result.Value, out)
	}
	return nil
}
