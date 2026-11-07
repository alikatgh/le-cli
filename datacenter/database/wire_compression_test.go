package database

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

type fixtureFrameConn struct{ *bytes.Reader }

func (fixtureFrameConn) Write([]byte) (int, error)        { return 0, io.ErrClosedPipe }
func (fixtureFrameConn) Close() error                     { return nil }
func (fixtureFrameConn) LocalAddr() net.Addr              { return nil }
func (fixtureFrameConn) RemoteAddr() net.Addr             { return nil }
func (fixtureFrameConn) SetDeadline(time.Time) error      { return nil }
func (fixtureFrameConn) SetReadDeadline(time.Time) error  { return nil }
func (fixtureFrameConn) SetWriteDeadline(time.Time) error { return nil }
func TestMongoRejectsUnnegotiatedCompressedFrameBeforeBody(t *testing.T) {
	header := make([]byte, 16)
	binary.LittleEndian.PutUint32(header, 25)
	binary.LittleEndian.PutUint32(header[12:], 2012)
	// No body is supplied: rejection must precede reading a malicious inner
	// uncompressed length or invoking the native driver's decompressor.
	conn := guardedConn(fixtureFrameConn{bytes.NewReader(header)}, true)
	if _, err := conn.Read(make([]byte, 32)); !errors.Is(err, ErrLimit) {
		t.Fatalf("compressed message reached driver: %v", err)
	}
}
