package dictate

// A small RFC 6455 WebSocket client on the standard library (no new module):
// the opening handshake is an ordinary net/http request — so proxies, TLS and
// the HTTP status of a refused handshake (401 bad key, 402/429 quota …) come
// for free — and a "101 Switching Protocols" response body is the raw
// connection the frames travel on (net/http returns it as an
// io.ReadWriteCloser since Go 1.12). HTTP/2 is switched off for these
// requests: an upgrade only exists in HTTP/1.1.
//
// Client frames are masked (crypto/rand), server frames are read with
// fragmentation; pings are answered, a close frame ends the stream with its
// code and reason.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
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

// WebSocket opcodes.
const (
	opCont   = 0x0
	opText   = 0x1
	opBinary = 0x2
	opClose  = 0x8
	opPing   = 0x9
	opPong   = 0xA
)

const (
	wsMaxMessage = 16 << 20
	wsGUID       = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

// wsTransport makes the handshakes (a package var: tests allow loopback only).
var wsTransport http.RoundTripper = newWSTransport()

func newWSTransport() *http.Transport {
	var tr *http.Transport
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = dt.Clone()
	} else {
		tr = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	tr.ForceAttemptHTTP2 = false
	p := new(http.Protocols)
	p.SetHTTP1(true)
	tr.Protocols = p
	tr.TLSHandshakeTimeout = 15 * time.Second
	return tr
}

// HandshakeError is a refused WebSocket handshake: the HTTP status and the
// start of the body (services explain bad keys and quotas there).
type HandshakeError struct {
	Status int
	Body   string
	Header http.Header
}

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("websocket handshake: HTTP %d: %s", e.Status, clip(e.Body, 200))
}

// CloseError is the close frame that ended the stream.
type CloseError struct {
	Code   int
	Reason string
}

func (e *CloseError) Error() string {
	return fmt.Sprintf("websocket closed: %d %s", e.Code, e.Reason)
}

type wsConn struct {
	rwc io.ReadWriteCloser
	br  *bufio.Reader

	wmu    sync.Mutex
	sent   bool // close frame sent
	closed bool
	once   sync.Once
}

// wsDial opens a WebSocket at rawURL (ws:// or wss://, http(s) accepted)
// with the extra headers. ctx bounds the handshake only.
func wsDial(ctx context.Context, rawURL string, hdr http.Header) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(u.Scheme) {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return nil, fmt.Errorf("websocket: bad scheme %q", u.Scheme)
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	res, err := wsTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusSwitchingProtocols {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<10))
		res.Body.Close()
		return nil, &HandshakeError{Status: res.StatusCode, Body: string(b), Header: res.Header}
	}
	rwc, ok := res.Body.(io.ReadWriteCloser)
	if !ok {
		res.Body.Close()
		return nil, errors.New("websocket: the connection cannot be upgraded")
	}
	if !strings.EqualFold(res.Header.Get("Upgrade"), "websocket") || res.Header.Get("Sec-WebSocket-Accept") != acceptKey(key) {
		rwc.Close()
		return nil, errors.New("websocket: bad handshake reply")
	}
	return &wsConn{rwc: rwc, br: bufio.NewReaderSize(rwc, 32<<10)}, nil
}

func acceptKey(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// WriteText sends one text message.
func (c *wsConn) WriteText(b []byte) error { return c.write(opText, b) }

// WriteBinary sends one binary message.
func (c *wsConn) WriteBinary(b []byte) error { return c.write(opBinary, b) }

func (c *wsConn) write(op byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed || (c.sent && op != opClose) {
		return net.ErrClosed
	}
	if op == opClose {
		c.sent = true
	}
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
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	hdr = append(hdr, mask[:]...)
	buf := make([]byte, len(hdr)+n)
	copy(buf, hdr)
	body := buf[len(hdr):]
	for i := 0; i < n; i++ {
		body[i] = payload[i] ^ mask[i&3]
	}
	_, err := c.rwc.Write(buf)
	return err
}

// Read returns the next data message (text or binary). Pings are answered
// on the way; a close frame is answered and returned as *CloseError.
func (c *wsConn) Read() (op byte, data []byte, err error) {
	var msg []byte
	msgOp := byte(0)
	for {
		fin, fop, payload, err := c.frame()
		if err != nil {
			return 0, nil, err
		}
		switch fop {
		case opPing:
			_ = c.write(opPong, payload)
			continue
		case opPong:
			continue
		case opClose:
			ce := &CloseError{Code: 1005}
			if len(payload) >= 2 {
				ce.Code = int(binary.BigEndian.Uint16(payload))
				ce.Reason = string(payload[2:])
			}
			c.wmu.Lock()
			sent := c.sent
			c.wmu.Unlock()
			if !sent {
				_ = c.write(opClose, payload[:min(2, len(payload))])
			}
			c.shut()
			return 0, nil, ce
		case opText, opBinary:
			msgOp, msg = fop, append(msg[:0], payload...)
		case opCont:
			if msgOp == 0 {
				return 0, nil, errors.New("websocket: unexpected continuation frame")
			}
			msg = append(msg, payload...)
		default:
			return 0, nil, fmt.Errorf("websocket: unknown opcode %d", fop)
		}
		if len(msg) > wsMaxMessage {
			return 0, nil, errors.New("websocket: message too large")
		}
		if fin {
			return msgOp, msg, nil
		}
	}
}

// frame reads one frame.
func (c *wsConn) frame() (fin bool, op byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.br, h[:]); err != nil {
		return
	}
	fin, op = h[0]&0x80 != 0, h[0]&0x0F
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > wsMaxMessage {
		return false, 0, nil, errors.New("websocket: frame too large")
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return
}

// CloseNormal sends a normal close frame (the reader then sees the
// server's close or EOF). The connection is shut after grace.
func (c *wsConn) CloseNormal(grace time.Duration) {
	payload := []byte{0x03, 0xE8} // 1000
	_ = c.write(opClose, payload)
	time.AfterFunc(grace, c.shut)
}

// shut closes the underlying connection (unblocks a pending Read).
func (c *wsConn) shut() {
	c.once.Do(func() {
		c.rwc.Close() // first: unblocks a write stuck on the network
		c.wmu.Lock()
		c.closed = true
		c.wmu.Unlock()
	})
}
