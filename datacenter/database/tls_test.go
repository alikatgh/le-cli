package database

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Every key stays in memory. The only certificate file is this test's public
// synthetic CA, beneath its owned temporary directory.
func fixtureTLSCertificate(t *testing.T, address net.IP) (tls.Certificate, []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Owned TLS fixture CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Owned TLS fixture server"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IPAddresses: []net.IP{address}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, server, ca, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{serverDER, caDER}, PrivateKey: serverKey}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}

func TestVerifiedTLSWireHandshake(t *testing.T) {
	for _, mode := range []string{"trusted", "untrusted-ca", "hostname-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			certIP := net.ParseIP("127.0.0.1")
			if mode == "hostname-mismatch" {
				certIP = net.ParseIP("127.0.0.2")
			}
			certificate, publicCA := fixtureTLSCertificate(t, certIP)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			serverDone := make(chan struct{})
			clientDone := make(chan struct{})
			go func() {
				defer close(serverDone)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				server := tls.Server(conn, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}})
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				// The server observes client rejection for the two failure cases.
				if server.HandshakeContext(ctx) == nil {
					// Keep the peer open until client cleanup, avoiding a close-
					// notify race unrelated to certificate verification.
					select {
					case <-clientDone:
					case <-ctx.Done():
					}
				}
			}()
			config := Config{Engine: "redis", Address: listener.Addr().String(), Database: "0", TLS: true}
			if mode != "untrusted-ca" {
				config.CAFile = filepath.Join(t.TempDir(), "owned-public-ca.crt")
				if err := os.WriteFile(config.CAFile, publicCA, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			transport, err := TLSConfig(config)
			if err != nil {
				t.Fatal(err)
			}
			if transport == nil || transport.InsecureSkipVerify || transport.ServerName != "127.0.0.1" {
				t.Fatal("TLS verification was weakened")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			started := time.Now()
			conn, err := encryptedDial(ctx, config.Address, transport)
			if time.Since(started) > 3*time.Second {
				t.Fatal("TLS operation exceeded its bound")
			}
			if mode == "trusted" {
				if err != nil {
					t.Fatalf("trusted fixture TLS failed: %v", err)
				}
				secure, ok := conn.(*tls.Conn)
				if !ok || !secure.ConnectionState().HandshakeComplete || len(secure.ConnectionState().VerifiedChains) == 0 {
					t.Fatal("handshake did not verify a certificate chain")
				}
				if err := conn.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if conn != nil {
					_ = conn.Close()
				}
				if err == nil || conn != nil {
					t.Fatal("invalid TLS identity fell back to a usable connection")
				}
				if mode == "untrusted-ca" {
					var untrusted x509.UnknownAuthorityError
					if !errors.As(err, &untrusted) {
						t.Fatalf("expected certificate trust rejection: %v", err)
					}
				} else {
					var mismatch x509.HostnameError
					if !errors.As(err, &mismatch) {
						t.Fatalf("expected certificate hostname rejection: %v", err)
					}
				}
			}
			close(clientDone)
			select {
			case <-serverDone:
			case <-time.After(3 * time.Second):
				t.Fatal("owned TLS server did not finish")
			}
		})
	}
}
