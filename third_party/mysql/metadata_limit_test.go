// SPDX-License-Identifier: MPL-2.0
// Local regression tests for localhost-explorer's metadata allocation bounds.
package mysql

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"testing"
)

type metadataFixtureConn struct {
	net.Conn
	reader *bytes.Reader
}

func (c *metadataFixtureConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *metadataFixtureConn) Close() error               { return nil }

func metadataConnection(t *testing.T, payload []byte) *mysqlConn {
	t.Helper()
	if len(payload) > 255 {
		t.Fatal("fixture payload too large")
	}
	packet := append([]byte{byte(len(payload)), 0, 0, 0}, payload...)
	return &mysqlConn{buf: newBuffer(), netConn: &metadataFixtureConn{reader: bytes.NewReader(packet)}, cfg: NewConfig(), closech: make(chan struct{})}
}

func TestBoundedResultHeaderBeforeIntegerConversion(t *testing.T) {
	for _, payload := range [][]byte{{0xfc, 1, 1}, {0xfd, 1, 1, 0}, {0xfe, 255, 255, 255, 255, 255, 255, 255, 255}} {
		mc := metadataConnection(t, payload)
		if _, _, err := mc.resultUnchanged().readResultSetHeaderPacket(); !errors.Is(err, ErrMetadataLimit) {
			t.Fatalf("metadata count accepted: %v", err)
		}
	}
	mc := metadataConnection(t, []byte{0xfc, 0, 1})
	count, _, err := mc.resultUnchanged().readResultSetHeaderPacket()
	if err != nil || count != 256 {
		t.Fatalf("bounded metadata rejected: %d %v", count, err)
	}
}

func TestMalformedMetadataHeadersRemainErrors(t *testing.T) {
	for _, payload := range [][]byte{{0xfc}, {0xfd, 1, 1}, {0xfe, 1, 1, 1, 1, 1, 1, 1}} {
		mc := metadataConnection(t, payload)
		if _, _, err := mc.resultUnchanged().readResultSetHeaderPacket(); !errors.Is(err, ErrMalformPkt) {
			t.Fatalf("truncated metadata: %v", err)
		}
	}
	mc := metadataConnection(t, []byte{1})
	mc.extCapabilities = clientCacheMetadata
	if _, _, err := mc.resultUnchanged().readResultSetHeaderPacket(); !errors.Is(err, ErrMalformPkt) {
		t.Fatalf("missing cache-metadata flag: %v", err)
	}
	mc = metadataConnection(t, []byte{1, 2})
	mc.extCapabilities = clientCacheMetadata
	if _, _, err := mc.resultUnchanged().readResultSetHeaderPacket(); !errors.Is(err, ErrMalformPkt) {
		t.Fatalf("invalid cache-metadata flag: %v", err)
	}
}

func TestBoundedPreparedMetadataBeforeAllocation(t *testing.T) {
	for _, counts := range [][2]uint16{{257, 0}, {0, 257}, {65535, 65535}, {256, 256}} {
		payload := make([]byte, 12)
		binary.LittleEndian.PutUint32(payload[1:5], 1)
		binary.LittleEndian.PutUint16(payload[5:7], counts[0])
		binary.LittleEndian.PutUint16(payload[7:9], counts[1])
		stmt := mysqlStmt{mc: metadataConnection(t, payload)}
		_, err := stmt.readPrepareResultPacket()
		if counts[0] > 256 || counts[1] > 256 {
			if !errors.Is(err, ErrMetadataLimit) || stmt.paramCount != 0 {
				t.Fatalf("prepared count accepted: %v", err)
			}
		} else if err != nil || stmt.paramCount != 256 {
			t.Fatalf("bounded prepare rejected: %v", err)
		}
	}
	stmt := mysqlStmt{mc: metadataConnection(t, make([]byte, 9))}
	if _, err := stmt.readPrepareResultPacket(); !errors.Is(err, ErrMalformPkt) {
		t.Fatalf("truncated prepare: %v", err)
	}
	for _, count := range []int{-1, 257, int(^uint(0) >> 1)} {
		_, err := (&mysqlConn{}).readColumns(count, nil)
		if err == nil {
			t.Fatal("readColumns allocated an invalid count")
		}
	}
}
