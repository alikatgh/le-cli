package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// A test binary stands in for a vendor process, using only synthetic
	// fixture data. Production Backup still supplies its real discrete argv.
	if len(os.Args) > 1 && os.Args[1] == "--format=custom" {
		if strings.Contains(strings.Join(os.Args[1:], " "), "fixture-password") {
			os.Exit(9)
		}
		switch os.Getenv("PGDATABASE") {
		case "fixture-ok":
			fmt.Print("fixture-backup")
		case "fixture-large":
			fmt.Print(strings.Repeat("x", 1024))
		case "fixture-wait":
			time.Sleep(10 * time.Second)
		case "fixture-fail":
			fmt.Fprint(os.Stderr, "fixture-password")
			os.Exit(3)
		default:
			os.Exit(8)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestBackupPrivatePartialAndDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows backup intentionally unavailable")
	}
	for _, mode := range []string{"ok", "large", "wait", "fail"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "new.dump")
			c := Config{Engine: "postgres", Address: "127.0.0.1:5432", Database: "fixture-" + mode, Password: "fixture-password"}
			o := DefaultBackupOptions(path)
			o.Binary = os.Args[0]
			o.MaxBytes = 32
			o.Timeout = 3 * time.Second
			if mode == "wait" {
				o.Timeout = 150 * time.Millisecond
			}
			result, err := Backup(context.Background(), c, o)
			if mode == "ok" {
				if err != nil || !result.Complete || result.Bytes != 14 {
					t.Fatalf("%+v %v", result, err)
				}
			} else if err == nil || result.Complete {
				t.Fatalf("failed dump accepted %+v %v", result, err)
			}
			if err != nil && strings.Contains(err.Error(), "fixture-password") {
				t.Fatal("vendor diagnostics escaped")
			}
			info, statErr := os.Stat(path)
			if statErr != nil || info.Size() != result.Bytes || info.Mode().Perm() != 0600 {
				t.Fatalf("private partial output %+v %v", info, statErr)
			}
			if mode == "large" && result.Bytes != 32 {
				t.Fatal("byte bound not enforced")
			}
		})
	}
}

func TestBackupCommandSecretsAndScope(t *testing.T) {
	c := Config{Engine: "postgres", Address: "127.0.0.1:5432", Database: "fixture", Username: "fixture-user", Password: "fixture-password"}
	_, args, env, _, err := backupCommand(c, DefaultBackupOptions("unused"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), c.Password) || !containsEnv(env, "PGPASSWORD="+c.Password) {
		t.Fatal("credential transport")
	}
	c.Engine = "redis"
	c.Address = "redis.example.invalid:6379"
	c.Database = "2"
	name, args, env, format, err := backupCommand(c, DefaultBackupOptions("unused"))
	if err != nil || name != "redis-cli" || !strings.Contains(format, "BGSAVE") || !containsEnv(env, "REDISCLI_AUTH="+c.Password) || !strings.Contains(strings.Join(args, " "), "--tls-expected-peer-name redis.example.invalid") || !hasArg(args, "--rdb") {
		t.Fatalf("redis: %v %v", args, err)
	}
	if strings.Contains(strings.Join(args, " "), c.Password) {
		t.Fatal("password in argv")
	}
}
func containsEnv(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func hasArg(values []string, want string) bool { return containsEnv(values, want) }
func TestBackupPreservesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.dump")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{Engine: "postgres", Address: "127.0.0.1:5432", Database: "fixture"}
	o := DefaultBackupOptions(path)
	o.Binary = os.Args[0]
	_, err := Backup(context.Background(), c, o)
	if err == nil {
		t.Fatal("overwrite accepted")
	}
	// #nosec G304 -- Original fixture file in this test's owned temporary directory.
	b, _ := os.ReadFile(path)
	if !reflect.DeepEqual(b, []byte("original")) {
		t.Fatal("original changed")
	}
}
func TestVendorConfigRejectsUnsafeOptions(t *testing.T) {
	for _, contents := range []string{"[client]\nuser=fixture\npassword=fixture\n", "[client]\ninit-command=DELETE FROM records\n", "!include /somewhere\n", "[other]\nuser=fixture\n"} {
		path := filepath.Join(t.TempDir(), "fixture.cnf")
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		err := validateVendorConfig(path, Config{Engine: "mysql"})
		if (err == nil) != strings.HasPrefix(contents, "[client]\nuser") {
			t.Fatalf("unexpected validation for synthetic profile: %v", err)
		}
	}
	for _, uri := range []string{"mongodb://127.0.0.1:27017/fixture?directConnection=true", "mongodb://other.invalid:27017/fixture?directConnection=true", "mongodb://127.0.0.1:27017/fixture?directConnection=true&tlsInsecure=true", "mongodb://127.0.0.1:27017/fixture?directConnection=true&authMechanism=MONGODB-AWS", "mongodb://127.0.0.1:27017/fixture?directConnection=true&authMechanism=SCRAM-SHA-256&authMechanism=MONGODB-AWS", "mongodb://127.0.0.1:27017/fixture?directConnection=true&tls=true&tls=false", "mongodb://127.0.0.1:27017/fixture?directConnection=true&directConnection=false", "mongodb://127.0.0.1:27017/fixture?directConnection=true&authSource=a;b"} {
		path := filepath.Join(t.TempDir(), "fixture.yml")
		if err := os.WriteFile(path, []byte("uri: '"+uri+"'\npassword: fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
		err := validateVendorConfig(path, Config{Engine: "mongodb", Address: "127.0.0.1:27017", Database: "fixture"})
		if (err == nil) != (uri == "mongodb://127.0.0.1:27017/fixture?directConnection=true") {
			t.Fatalf("unexpected URI validation: %v", err)
		}
	}
}
