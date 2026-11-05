//go:build !windows

package companion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testDescriptor() Descriptor {
	return Descriptor{1, "http://127.0.0.1:12345", "synthetic-test-credential", strings.Repeat("a", 32), "test"}
}

func TestDescriptorPermissionsExclusiveAndRetirement(t *testing.T) {
	root := t.TempDir()
	path, retire, err := writeDescriptor(root, testDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		mode os.FileMode
	}{{path, 0600}, {filepath.Dir(path), 0700}} {
		info, err := os.Stat(tc.path)
		if err != nil || info.Mode().Perm() != tc.mode {
			t.Fatalf("mode %s: %v %v", tc.path, info, err)
		}
	}
	if _, _, err := writeDescriptor(root, testDescriptor()); err == nil {
		t.Fatal("existing descriptor overwritten")
	}
	retire()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("owned descriptor not retired", err)
	}
}

func TestDescriptorPreservesExistingAndSymlinkPaths(t *testing.T) {
	for _, name := range []string{"le", "companion"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			target := t.TempDir()
			link := filepath.Join(root, "le")
			if name == "companion" {
				if err := os.Mkdir(link, 0700); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(link, "companion")
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, _, err := writeDescriptor(root, testDescriptor()); err == nil {
				t.Fatal("symlink accepted")
			}
			if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("symlink changed")
			}
			entries, err := os.ReadDir(target)
			if err != nil || len(entries) != 0 {
				t.Fatal("symlink target modified")
			}
		})
	}
	root := t.TempDir()
	// #nosec G301 -- Deliberately nonprivate t.TempDir fixture verifies unsafe directories are rejected and preserved.
	if err := os.Mkdir(filepath.Join(root, "le"), 0755); err != nil {
		t.Fatal(err)
	}
	// #nosec G301 -- Deliberately nonprivate t.TempDir fixture verifies unsafe directories are rejected and preserved.
	if err := os.Mkdir(filepath.Join(root, "le", "companion"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := writeDescriptor(root, testDescriptor()); err == nil {
		t.Fatal("nonprivate existing directory accepted")
	}
	info, _ := os.Stat(filepath.Join(root, "le", "companion"))
	if info.Mode().Perm() != 0755 {
		t.Fatal("generic existing directory chmodded")
	}
}

func TestRetirementPreservesReplacedOrModifiedFile(t *testing.T) {
	for _, replace := range []bool{false, true} {
		root := t.TempDir()
		path, retire, err := writeDescriptor(root, testDescriptor())
		if err != nil {
			t.Fatal(err)
		}
		if replace {
			replacement := filepath.Join(root, "replacement")
			if err := os.WriteFile(replacement, []byte("external replacement"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(path, []byte("externally modified"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		retire()
		if _, err := os.Stat(path); err != nil {
			t.Fatal("external file retired", err)
		}
	}
}
