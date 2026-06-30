package tools

import (
	"net"
	"testing"
)

func TestFreeReflectsBinding(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0") // OS picks a free port
	if err != nil {
		t.Skipf("cannot bind a test port: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	if Free(port) {
		t.Fatalf("port %s is bound but Free() reported it free", port)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !Free(port) {
		t.Errorf("port %s was released but Free() reported it busy", port)
	}
}
