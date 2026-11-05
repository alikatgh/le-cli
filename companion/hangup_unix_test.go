//go:build !windows

package companion

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alikatgh/le-cli/internal/boundedexec"
)

type hangupEngine struct{ pidPath string }

func (e hangupEngine) Snapshot(ctx context.Context) ([]Row, error) {
	_, err := boundedexec.Output(ctx, "/bin/sh", "-c", `sleep 30 & child=$!; printf '%s %s\n' "$$" "$child" > "$1"; wait`, "owned-hangup-fixture", e.pidPath)
	return nil, err
}
func (hangupEngine) Action(context.Context, Row, string) (string, error) {
	return "", fmt.Errorf("fixture has no actions")
}

// The helper executes the production signal/server/descriptor lifecycle with
// only synthetic credentials and an injected owned subprocess fixture.
func TestCompanionHangupHelper(t *testing.T) {
	if os.Getenv("LE_OWNED_HANGUP_FIXTURE") != "1" {
		return
	}
	root := os.Getenv("LE_OWNED_HANGUP_ROOT")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	descriptor := testDescriptor()
	descriptor.Endpoint = "http://" + listener.Addr().String()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	go func() {
		r, err := http.NewRequest("POST", descriptor.Endpoint+"/v1/snapshot", strings.NewReader("{}"))
		if err != nil {
			return
		}
		r.Header.Set("Authorization", "Bearer "+descriptor.Token)
		r.Header.Set("Content-Type", "application/json")
		response, err := client.Do(r)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	if err := serveSession(context.Background(), listener, io.Discard, root, descriptor, hangupEngine{filepath.Join(root, "owned-pids")}); err != nil {
		t.Fatal(err)
	}
}

func TestHangupCancelsOwnedWorkAndRetiresDescriptor(t *testing.T) {
	root := t.TempDir()
	// #nosec G204 G702 -- Re-executes this owned Go test binary with a fixed test selector and no shell.
	command := exec.Command(os.Args[0], "-test.run=^TestCompanionHangupHelper$")
	command.Env = append(os.Environ(), "LE_OWNED_HANGUP_FIXTURE=1", "LE_OWNED_HANGUP_ROOT="+root)
	var diagnostics bytes.Buffer
	command.Stdout, command.Stderr = &diagnostics, &diagnostics
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	defer func() { _ = command.Process.Kill() }()
	pidPath := filepath.Join(root, "owned-pids")
	var pids []int
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		// #nosec G304 -- pidPath is an owned fixture file beneath this test's t.TempDir.
		data, err := os.ReadFile(pidPath)
		if err == nil {
			fields := strings.Fields(string(data))
			if len(fields) == 2 {
				for _, field := range fields {
					pid, parseErr := strconv.Atoi(field)
					if parseErr != nil || pid <= 1 {
						t.Fatal("invalid owned fixture PID")
					}
					pids = append(pids, pid)
				}
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("fixture exited before work began: %v %s", err, diagnostics.String())
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pids) != 2 {
		t.Fatal("owned fixture did not start")
	}
	defer func() { _ = syscall.Kill(-pids[0], syscall.SIGKILL) }()
	descriptorPath := filepath.Join(root, "le", "companion", "connection-"+testDescriptor().ServerInstance+".leconnection")
	if _, err := os.Stat(descriptorPath); err != nil {
		t.Fatal("owned descriptor not created", err)
	}
	if err := command.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hangup did not exit gracefully: %v %s", err, diagnostics.String())
		}
	case <-time.After(4 * time.Second):
		t.Fatal("hangup did not wait for bounded cleanup")
	}
	if _, err := os.Lstat(descriptorPath); !os.IsNotExist(err) {
		t.Fatal("owned descriptor survived hangup", err)
	}
	for _, pid := range pids {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		state, _ := boundedexec.Output(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "stat=")
		cancel()
		if syscall.Kill(pid, 0) == nil && !strings.HasPrefix(strings.TrimSpace(state), "Z") {
			t.Fatalf("owned child survived hangup: %d %q", pid, state)
		}
	}
}
