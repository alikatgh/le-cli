package database

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"time"
)

// Deadline and aggregate byte caps survive drivers extending socket deadlines.
// MySQL additionally has its protocol's 24-bit packet bound; pgx has an explicit
// maximum message size. Redis/Mongo guards reject oversized announced frames.
type deadlineConn struct {
	net.Conn
	until     time.Time
	remaining int64
}

func (c *deadlineConn) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, ErrLimit
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.Conn.Read(p)
	c.remaining -= int64(n)
	return n, err
}
func (c *deadlineConn) SetReadDeadline(t time.Time) error {
	if t.IsZero() || t.After(c.until) {
		t = c.until
	}
	return c.Conn.SetReadDeadline(t)
}
func (c *deadlineConn) SetWriteDeadline(t time.Time) error {
	if t.IsZero() || t.After(c.until) {
		t = c.until
	}
	return c.Conn.SetWriteDeadline(t)
}
func (c *deadlineConn) SetDeadline(t time.Time) error {
	if t.IsZero() || t.After(c.until) {
		t = c.until
	}
	return c.Conn.SetDeadline(t)
}
func boundedDial(ctx context.Context, address string) (net.Conn, error) {
	conn, err := (&net.Dialer{Timeout: queryTimeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	until := time.Now().Add(queryTimeout)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(until) {
		until = deadline
	}
	c := &deadlineConn{Conn: conn, until: until, remaining: 4 * maxResult}
	if err := c.SetDeadline(until); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func encryptedDial(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
	conn, err := boundedDial(ctx, address)
	if err != nil {
		return nil, err
	}
	if config == nil {
		return conn, nil
	}
	t := tls.Client(conn, config)
	if err := t.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return t, nil
}

// frameConn exposes a response only after validating its declared lengths. The
// mature driver still handles its protocol, authentication, and decoded types.
type frameConn struct {
	net.Conn
	reader  *bufio.Reader
	pending []byte
	mongo   bool
}

func guardedConn(c net.Conn, mongo bool) net.Conn {
	return &frameConn{Conn: c, reader: bufio.NewReaderSize(c, 4096), mongo: mongo}
}
func (c *frameConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.pending) == 0 {
		if c.mongo {
			header := make([]byte, 16)
			if _, err := io.ReadFull(c.reader, header); err != nil {
				return 0, err
			}
			size := int64(binary.LittleEndian.Uint32(header))
			if size < 16 || size > maxResult {
				return 0, ErrLimit
			}
			// Compression is never negotiated. The driver's decompressor trusts
			// the inner uncompressedSize before decoding, so an outer byte cap
			// alone cannot bound allocation for an unsolicited OP_COMPRESSED.
			if binary.LittleEndian.Uint32(header[12:16]) == 2012 {
				return 0, ErrLimit
			}
			c.pending = make([]byte, int(size))
			copy(c.pending, header)
			if _, err := io.ReadFull(c.reader, c.pending[16:]); err != nil {
				c.pending = nil
				return 0, err
			}
		} else {
			var err error
			c.pending, err = respFrame(c.reader, 0, maxResult)
			if err != nil {
				return 0, err
			}
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func respFrame(r *bufio.Reader, depth, budget int) ([]byte, error) {
	if depth > 32 || budget < 3 {
		return nil, ErrLimit
	}
	line, err := r.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		return nil, ErrLimit
	}
	if err != nil {
		return nil, err
	}
	if len(line) < 3 || line[len(line)-2] != '\r' || len(line) > budget {
		return nil, ErrQuery
	}
	out := append([]byte(nil), line...)
	switch line[0] {
	case '+', '-', ':':
		return out, nil
	case '$', '*':
		n, err := strconv.ParseInt(string(line[1:len(line)-2]), 10, 64)
		if err != nil || n < -1 {
			return nil, ErrQuery
		}
		if n == -1 {
			return out, nil
		}
		if line[0] == '$' {
			if n > int64(budget-len(out)-2) {
				return nil, ErrLimit
			}
			start := len(out)
			out = append(out, make([]byte, int(n)+2)...)
			if _, err := io.ReadFull(r, out[start:]); err != nil {
				return nil, err
			}
			if out[len(out)-2] != '\r' || out[len(out)-1] != '\n' {
				return nil, ErrQuery
			}
			return out, nil
		}
		if n > 2*maxRows+32 {
			return nil, ErrLimit
		}
		for i := int64(0); i < n; i++ {
			part, err := respFrame(r, depth+1, budget-len(out))
			if err != nil {
				return nil, err
			}
			out = append(out, part...)
		}
		return out, nil
	default:
		return nil, ErrQuery
	}
}
