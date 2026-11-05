//go:build !windows

package companion

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Descriptor is a credential, intended only for direct user-selected file
// import. Its contents must never be passed to a logger or console writer.
type Descriptor struct {
	ProtocolVersion int    `json:"protocolVersion"`
	Endpoint        string `json:"endpoint"`
	Token           string `json:"token"`
	ServerInstance  string `json:"serverInstance"`
	CLIVersion      string `json:"cliVersion"`
}

// Run serves only while this explicitly invoked foreground command is alive.
func Run(ctx context.Context, version string, out io.Writer) error {
	if runtime.GOOS != "darwin" {
		return errors.New("companion is only available on macOS")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return errors.New("unable to open local companion listener")
	}
	defer func() { _ = listener.Close() }()
	var tokenBytes [32]byte
	if _, err = rand.Read(tokenBytes[:]); err != nil {
		return errors.New("secure randomness unavailable")
	}
	instance, err := randomID()
	if err != nil {
		return errors.New("secure randomness unavailable")
	}
	descriptor := Descriptor{1, "http://" + listener.Addr().String(), base64.RawURLEncoding.EncodeToString(tokenBytes[:]), instance, version}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return errors.New("user configuration directory unavailable")
	}
	return serveSession(ctx, listener, out, configDir, descriptor, LocalEngine{})
}

// serveSession is the complete foreground lifecycle shared with owned fixture
// tests. Fixtures supply synthetic credentials and an injected engine, so they
// never inspect user connection files or invoke real service managers.
func serveSession(ctx context.Context, listener net.Listener, out io.Writer, configDir string, descriptor Descriptor, engine Engine) error {
	defer func() { _ = listener.Close() }()
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	path, retire, err := writeDescriptor(configDir, descriptor)
	if err != nil {
		return errors.New("unable to create private connection file; check the user configuration directory")
	}
	defer retire()
	// Paths and fixed setup text only: never format descriptor or its token.
	_, _ = fmt.Fprintf(out, "Connection file: %s\nIn Localhost Explorer, choose Connect Companion and select this file.\nKeep this command running; press Ctrl-C to disconnect.\n", path)
	handler := NewHandler(listener.Addr().String(), descriptor.Token, descriptor.ServerInstance, descriptor.CLIVersion, engine)
	server := &http.Server{Handler: handler,
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: MaxHeaders, ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = server.Close()
		case <-done:
		}
	}()
	err = server.Serve(listener)
	handler.stopAndWait()
	close(done)
	if errors.Is(err, http.ErrServerClosed) || ctx.Err() != nil {
		return nil
	}
	return errors.New("local companion server stopped")
}

func openDirectoryAt(parent int, name string, private bool) (int, error) {
	if err := unix.Mkdirat(parent, name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return -1, err
	}
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	if int(stat.Uid) != os.Getuid() || (private && stat.Mode&0777 != 0700) {
		_ = unix.Close(fd)
		return -1, errors.New("directory is not privately owned")
	}
	return fd, nil
}

func writeDescriptor(configDir string, descriptor Descriptor) (string, func(), error) {
	// Open each application-owned component relative to a directory handle.
	// O_NOFOLLOW and exclusive creation prevent replacing existing symlinks/files.
	root, err := unix.Open(configDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = unix.Close(root) }()
	le, err := openDirectoryAt(root, "le", false)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = unix.Close(le) }()
	dir, err := openDirectoryAt(le, "companion", true)
	if err != nil {
		return "", nil, err
	}
	name := "connection-" + descriptor.ServerInstance + ".leconnection"
	if !idPattern.MatchString(descriptor.ServerInstance) {
		_ = unix.Close(dir)
		return "", nil, errors.New("invalid instance identifier")
	}
	fd, err := unix.Openat(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		_ = unix.Close(dir)
		return "", nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	var owned unix.Stat_t
	var ownedInfo os.FileInfo
	if err = unix.Fchmod(fd, 0600); err == nil {
		err = json.NewEncoder(file).Encode(descriptor)
	}
	if err == nil {
		err = file.Sync()
	}
	if err == nil {
		err = unix.Fstat(fd, &owned)
	}
	if err == nil {
		ownedInfo, err = file.Stat()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = unix.Unlinkat(dir, name, 0)
		_ = unix.Close(dir)
		return "", nil, err
	}
	retire := func() {
		defer func() { _ = unix.Close(dir) }()
		var current unix.Stat_t
		if unix.Fstatat(dir, name, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && current.Dev == owned.Dev && current.Ino == owned.Ino && current.Size == owned.Size {
			currentFD, openErr := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if openErr == nil {
				currentFile := os.NewFile(uintptr(currentFD), name)
				info, statErr := currentFile.Stat()
				var again unix.Stat_t
				same := unix.Fstat(currentFD, &again) == nil && again.Dev == owned.Dev && again.Ino == owned.Ino
				_ = currentFile.Close()
				if statErr == nil && same && info.ModTime().Equal(ownedInfo.ModTime()) {
					_ = unix.Unlinkat(dir, name, 0)
				}
			}
		}
	}
	return filepath.Join(configDir, "le", "companion", name), retire, nil
}
