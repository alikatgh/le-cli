// Package database supplies one-shot, bounded database reads. Read-only roles
// remain necessary: a query adapter cannot sandbox privileged server functions.
package database

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrConfig = errors.New("invalid database configuration or read request")
	ErrLimit  = errors.New("database read limit exceeded")
	ErrQuery  = errors.New("database read failed")
)

// Config is never serialized. Callers load credentials privately from the
// configured environment/secret wrapper; the package never reads credentials.
type Config struct {
	Engine     string `json:"-"`
	Address    string `json:"-"`
	Database   string `json:"-"`
	Collection string `json:"-"`
	Username   string `json:"-"`
	Password   string `json:"-"`
	CAFile     string `json:"-"`
	TLS        bool   `json:"-"`
}

// ValidateConfig requires an explicit single TCP endpoint and database. Remote
// endpoints use verified TLS automatically; TLS/CAFile also enable it locally.
func ValidateConfig(c Config) error {
	switch c.Engine {
	case "postgres", "mysql", "mariadb", "mongodb", "redis":
	default:
		return ErrConfig
	}
	if len(c.Address) > 320 || strings.ContainsAny(c.Address, "/@?#,\\") || !plain(c.Address, 320) {
		return ErrConfig
	}
	h, p, err := net.SplitHostPort(c.Address)
	if err != nil || h == "" || strings.ContainsAny(h, "%[] ") {
		return ErrConfig
	}
	port, err := strconv.ParseUint(p, 10, 16)
	if err != nil || port == 0 {
		return ErrConfig
	}
	if net.ParseIP(h) == nil {
		if len(h) > 253 || strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") {
			return ErrConfig
		}
		for _, part := range strings.Split(h, ".") {
			if part == "" || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
				return ErrConfig
			}
			for _, r := range part {
				letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
				digit := r >= '0' && r <= '9'
				if !letter && !digit && r != '-' {
					return ErrConfig
				}
			}
		}
	}
	if c.Database == "" || !plain(c.Database, 128) || strings.TrimSpace(c.Database) != c.Database {
		return ErrConfig
	}
	if !plain(c.Collection, 128) || !plain(c.Username, 4096) || !plain(c.Password, 16384) || !plain(c.CAFile, 4096) {
		return ErrConfig
	}
	if c.Engine == "redis" {
		db, err := strconv.Atoi(c.Database)
		if err != nil || db < 0 || db > 15 || strconv.Itoa(db) != c.Database {
			return ErrConfig
		}
	}
	if c.Engine == "mongodb" && (strings.ContainsAny(c.Database, "/\\.\"$ ") || strings.Contains(c.Collection, "\x00")) {
		return ErrConfig
	}
	return nil
}

func plain(s string, max int) bool {
	if len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

// TLSConfig returns nil only for literal loopback plaintext without TLS/CAFile.
// It never enables an insecure mode, fallback, client-key loading, or discovery.
func TLSConfig(c Config) (*tls.Config, error) {
	if ValidateConfig(c) != nil {
		return nil, ErrConfig
	}
	h, _, _ := net.SplitHostPort(c.Address)
	ip := net.ParseIP(h)
	if ip != nil && ip.IsLoopback() && !c.TLS && c.CAFile == "" {
		return nil, nil
	}
	t := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: h}
	if c.CAFile != "" {
		file, err := os.Open(c.CAFile)
		if err != nil {
			return nil, ErrConfig
		}
		defer func() { _ = file.Close() }() // Read-only certificate cleanup.
		data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		if err != nil {
			return nil, ErrConfig
		}
		if len(data) > 1<<20 {
			return nil, ErrLimit
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, ErrConfig
		}
		t.RootCAs = pool
	}
	return t, nil
}
