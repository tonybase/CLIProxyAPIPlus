// Package authshard decides which auth files belong to the current node when
// several nodes share one auth directory (e.g. over NFS).
//
// Ownership is computed from the file basename with Jump Consistent Hash, so
// growing the cluster from N to N+1 nodes only moves ~1/(N+1) of the files.
package authshard

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"sync/atomic"
)

type shardState struct {
	count int
	index int
}

var current atomic.Pointer[shardState]

// Validate checks that index is within [0, count) when sharding is enabled.
func Validate(count, index int) error {
	if count <= 1 {
		return nil
	}
	if index < 0 || index >= count {
		return fmt.Errorf("auth-shard: node-index %d out of range [0, %d)", index, count)
	}
	return nil
}

// Configure sets the shard layout for this process. count <= 1 disables sharding.
// It reports whether the effective layout changed.
func Configure(count, index int) (bool, error) {
	if errValidate := Validate(count, index); errValidate != nil {
		return false, errValidate
	}
	next := &shardState{}
	if count > 1 {
		next.count = count
		next.index = index
	}
	prev := current.Swap(next)
	if prev == nil {
		prev = &shardState{}
	}
	return *prev != *next, nil
}

// Enabled reports whether sharding is active.
func Enabled() bool {
	s := current.Load()
	return s != nil && s.count > 1
}

// Layout returns the active node count and index. count is 0 when disabled.
func Layout() (count, index int) {
	s := current.Load()
	if s == nil {
		return 0, 0
	}
	return s.count, s.index
}

// Owns reports whether the auth file at name belongs to this node.
// It always returns true when sharding is disabled.
func Owns(name string) bool {
	s := current.Load()
	if s == nil || s.count <= 1 {
		return true
	}
	return OwnerOf(name, s.count) == s.index
}

// OwnerOf returns the node index that owns name in a cluster of count nodes.
func OwnerOf(name string, count int) int {
	if count <= 1 {
		return 0
	}
	key := strings.ToLower(filepath.Base(name))
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int(jumpHash(h.Sum64(), int32(count)))
}

// jumpHash implements Jump Consistent Hash (Lamping & Veach, 2014).
func jumpHash(key uint64, buckets int32) int32 {
	var b int64 = -1
	var j int64
	for j < int64(buckets) {
		b = j
		key = key*2862933555777941757 + 1
		j = int64(float64(b+1) * (float64(int64(1)<<31) / float64((key>>33)+1)))
	}
	return int32(b)
}
