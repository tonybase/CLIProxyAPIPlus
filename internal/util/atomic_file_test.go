package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteJSONFileAtomicWritesValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	payload := []byte(`{"type":"kiro","access_token":"at"}`)

	if err := WriteJSONFileAtomic(path, payload, 0o600); err != nil {
		t.Fatalf("WriteJSONFileAtomic() err = %v", err)
	}
	persisted, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatalf("read back: %v", errRead)
	}
	if string(persisted) != string(payload) {
		t.Fatalf("persisted = %s, want %s", persisted, payload)
	}
}

// A shorter payload must fully replace a longer one. Truncate-then-write could
// leave the old tail behind, which is exactly the corruption this guards against.
func TestWriteJSONFileAtomicReplacesLongerContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	long := []byte(`{"a":"` + strings.Repeat("x", 512) + `"}`)
	if err := os.WriteFile(path, long, 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	short := []byte(`{"a":"y"}`)
	if err := WriteJSONFileAtomic(path, short, 0o600); err != nil {
		t.Fatalf("WriteJSONFileAtomic() err = %v", err)
	}
	persisted, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatalf("read back: %v", errRead)
	}
	if string(persisted) != string(short) {
		t.Fatalf("persisted = %s, want exactly %s", persisted, short)
	}
}

func TestWriteJSONFileAtomicRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	if err := WriteJSONFileAtomic(path, []byte(`{"unclosed":`), 0o600); err == nil {
		t.Fatal("WriteJSONFileAtomic() err = nil, want error for invalid JSON")
	}
	// The rejection must happen before touching the target.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("target should not exist after rejected write, stat err = %v", err)
	}
	assertNoTempLeftovers(t, dir)
}

// An existing file must survive a rejected write untouched.
func TestWriteJSONFileAtomicLeavesExistingFileOnInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	original := []byte(`{"v":1}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := WriteJSONFileAtomic(path, []byte(`{"broken":`), 0o600); err == nil {
		t.Fatal("WriteJSONFileAtomic() err = nil, want error for invalid JSON")
	}
	persisted, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatalf("read back: %v", errRead)
	}
	if string(persisted) != string(original) {
		t.Fatalf("persisted = %s, want untouched %s", persisted, original)
	}
}

func TestWriteFileAtomicCreatesMissingDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "auth.json")
	if err := WriteFileAtomic(path, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic() err = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat written file: %v", err)
	}
}

func TestWriteFileAtomicAppliesPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := WriteFileAtomic(path, []byte("payload"), 0o640); err != nil {
		t.Fatalf("WriteFileAtomic() err = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// CreateTemp defaults to 0600, so this also proves the chmod ran.
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("perm = %o, want 640", got)
	}
}

// Temp files must not accumulate in the auth directory: a directory scan looking
// for auth JSON would otherwise pick them up.
func TestWriteFileAtomicLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	for range 3 {
		if err := WriteFileAtomic(path, []byte(`{"v":1}`), 0o600); err != nil {
			t.Fatalf("WriteFileAtomic() err = %v", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "auth.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("dir contains %v, want only auth.json", names)
	}
}

func assertNoTempLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temp file %s left behind", entry.Name())
		}
	}
}
