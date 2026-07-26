package util

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"
)

// writeJSONFileAttempts bounds retries when the persisted content fails
// validation. The atomic write itself cannot tear, so a failure here means an
// outside writer (e.g. an instance still running an older build that writes
// non-atomically) clobbered the file; retrying gives it a chance to settle.
const writeJSONFileAttempts = 3

// WriteFileAtomic writes data to path so readers never observe a partial file.
//
// A plain os.WriteFile truncates and then writes, which are separate operations
// (separate RPCs on NFS). Concurrent writers on a shared directory can interleave
// between the two and leave a file holding the head of one write followed by the
// tail of another, which fails to parse. Writing to a temporary file in the same
// directory and renaming over the target avoids that: rename is atomic within a
// directory, so a reader sees either the old file or the new one.
//
// The temporary file carries a random suffix so writers on different hosts never
// collide on it, and it is removed if any step before the rename fails.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create dir failed: %w", err)
	}

	// The random suffix keeps the temp name from ending in .json, so directory
	// scans looking for auth files skip it while it is still being written.
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file failed: %w", err)
	}
	tmpName := tmp.Name()
	closed := false

	// discard drops the temp file when the write cannot be published, so a failed
	// attempt does not leave stray files behind in the auth directory.
	discard := func() {
		if !closed {
			if errClose := tmp.Close(); errClose != nil {
				log.Debugf("atomic write: close temp file %s failed: %v", tmpName, errClose)
			}
		}
		if errRemove := os.Remove(tmpName); errRemove != nil && !os.IsNotExist(errRemove) {
			log.Debugf("atomic write: remove temp file %s failed: %v", tmpName, errRemove)
		}
	}

	if _, errWrite := tmp.Write(data); errWrite != nil {
		discard()
		return fmt.Errorf("write temp file failed: %w", errWrite)
	}
	// Flush before the rename so the published file cannot contain the new length
	// with unwritten contents if the host loses power right after.
	if errSync := tmp.Sync(); errSync != nil {
		discard()
		return fmt.Errorf("sync temp file failed: %w", errSync)
	}
	if errClose := tmp.Close(); errClose != nil {
		closed = true
		discard()
		return fmt.Errorf("close temp file failed: %w", errClose)
	}
	closed = true
	// CreateTemp uses 0600; apply the caller's mode explicitly so the published
	// file does not silently depend on that default.
	if errChmod := os.Chmod(tmpName, perm); errChmod != nil {
		discard()
		return fmt.Errorf("chmod temp file failed: %w", errChmod)
	}
	if errRename := os.Rename(tmpName, path); errRename != nil {
		discard()
		return fmt.Errorf("rename temp file failed: %w", errRename)
	}
	return nil
}

// WriteJSONFileAtomic writes JSON atomically and reads it back to confirm the
// persisted bytes still parse.
//
// The rename makes the write itself indivisible, so the read-back is a guard
// against writers outside this code path rather than against our own write. A
// peer's newer content also passes validation, which is fine: the newest token
// wins and other instances converge when they reload the directory.
func WriteJSONFileAtomic(path string, data []byte, perm os.FileMode) error {
	if !json.Valid(data) {
		return fmt.Errorf("refusing to write invalid JSON to %s", path)
	}
	for attempt := 0; attempt < writeJSONFileAttempts; attempt++ {
		if err := WriteFileAtomic(path, data, perm); err != nil {
			return err
		}
		persisted, errRead := os.ReadFile(path)
		if errRead != nil {
			return fmt.Errorf("read back file failed: %w", errRead)
		}
		if json.Valid(persisted) {
			return nil
		}
		log.Warnf("atomic write: %s holds invalid JSON after write, retrying", path)
		time.Sleep(time.Second)
	}
	return fmt.Errorf("persisted content failed JSON validation: %s", path)
}
