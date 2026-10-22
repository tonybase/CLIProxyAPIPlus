// Package authshard decides which auth files belong to the current node when
// several nodes share one auth directory (e.g. over NFS).
//
// Ownership is computed from the file basename with Jump Consistent Hash, so
// growing the cluster from N to N+1 nodes only moves ~1/(N+1) of the files.
//
// A worker node loads and refreshes only the files it owns. A manager node
// loads every file so it can list and manage all accounts, but never refreshes
// credentials, leaving that to the owning worker.
package authshard

import (
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

const (
	// RoleWorker loads and refreshes only the auth files hashed to its node index.
	RoleWorker = "worker"
	// RoleManager loads every auth file but never refreshes credentials.
	RoleManager = "manager"
)

type shardState struct {
	count   int
	index   int
	manager bool
}

var current atomic.Pointer[shardState]

// Resolve overlays the AUTH_SHARD_ENABLED / AUTH_SHARD_COUNT / AUTH_SHARD_INDEX /
// AUTH_SHARD_ROLE environment variables on cfg. The result is never stored back in
// the config, so saving config.yaml cannot copy one node's overrides into a shared file.
func Resolve(cfg config.AuthShardConfig) (config.AuthShardConfig, error) {
	out := cfg
	if raw := strings.TrimSpace(os.Getenv("AUTH_SHARD_ENABLED")); raw != "" {
		v, errParse := strconv.ParseBool(raw)
		if errParse != nil {
			return out, fmt.Errorf("auth-shard: invalid AUTH_SHARD_ENABLED %q", raw)
		}
		out.Enabled = v
	}
	if raw := strings.TrimSpace(os.Getenv("AUTH_SHARD_COUNT")); raw != "" {
		v, errParse := strconv.Atoi(raw)
		if errParse != nil {
			return out, fmt.Errorf("auth-shard: invalid AUTH_SHARD_COUNT %q", raw)
		}
		out.NodeCount = v
	}
	if raw := strings.TrimSpace(os.Getenv("AUTH_SHARD_INDEX")); raw != "" {
		v, errParse := strconv.Atoi(raw)
		if errParse != nil {
			return out, fmt.Errorf("auth-shard: invalid AUTH_SHARD_INDEX %q", raw)
		}
		out.NodeIndex = v
	}
	if raw := strings.TrimSpace(os.Getenv("AUTH_SHARD_ROLE")); raw != "" {
		out.Role = raw
	}
	return out, nil
}

// Configure applies cfg, with environment overrides, to this process.
// It reports whether the effective layout changed. On error the previous layout is kept.
func Configure(cfg config.AuthShardConfig) (bool, error) {
	resolved, errResolve := Resolve(cfg)
	if errResolve != nil {
		return false, errResolve
	}
	next, errState := stateFor(resolved)
	if errState != nil {
		return false, errState
	}
	prev := current.Swap(next)
	if prev == nil {
		prev = &shardState{}
	}
	return *prev != *next, nil
}

func stateFor(cfg config.AuthShardConfig) (*shardState, error) {
	if !cfg.Enabled {
		return &shardState{}, nil
	}
	switch role := strings.ToLower(strings.TrimSpace(cfg.Role)); role {
	case "", RoleWorker:
	case RoleManager:
		return &shardState{manager: true}, nil
	default:
		return nil, fmt.Errorf("auth-shard: unknown role %q (want %s or %s)", cfg.Role, RoleWorker, RoleManager)
	}
	if cfg.NodeCount < 1 || cfg.NodeCount > math.MaxInt32 {
		return nil, fmt.Errorf("auth-shard: node-count %d out of range [1, %d]", cfg.NodeCount, math.MaxInt32)
	}
	if cfg.NodeIndex < 0 || cfg.NodeIndex >= cfg.NodeCount {
		return nil, fmt.Errorf("auth-shard: node-index %d out of range [0, %d)", cfg.NodeIndex, cfg.NodeCount)
	}
	if cfg.NodeCount == 1 {
		// A single worker owns everything, which is the same as no sharding.
		return &shardState{}, nil
	}
	return &shardState{count: cfg.NodeCount, index: cfg.NodeIndex}, nil
}

// Enabled reports whether this node is a sharded worker or a manager.
func Enabled() bool {
	s := current.Load()
	return s != nil && (s.manager || s.count > 1)
}

// IsManager reports whether this node runs as a manager.
func IsManager() bool {
	s := current.Load()
	return s != nil && s.manager
}

// Layout returns the worker node count and index. count is 0 when this node does not split files.
func Layout() (count, index int) {
	s := current.Load()
	if s == nil {
		return 0, 0
	}
	return s.count, s.index
}

// Describe returns a short human-readable description of the active layout for logs.
func Describe() string {
	s := current.Load()
	switch {
	case s == nil || (!s.manager && s.count <= 1):
		return "disabled"
	case s.manager:
		return "manager (loads all auth files, refreshes none)"
	default:
		return fmt.Sprintf("worker %d of %d", s.index, s.count)
	}
}

// Owns reports whether this node loads the auth file at name.
// It is true for every file when sharding is disabled or the node is a manager.
func Owns(name string) bool {
	s := current.Load()
	if s == nil || s.manager || s.count <= 1 {
		return true
	}
	return OwnerOf(name, s.count) == s.index
}

// Refreshes reports whether this node refreshes the credential in the auth file at
// name and writes its runtime state. Exactly one worker refreshes each file; a manager
// refreshes none.
func Refreshes(name string) bool {
	s := current.Load()
	if s == nil {
		return true
	}
	if s.manager {
		return false
	}
	if s.count <= 1 {
		return true
	}
	return OwnerOf(name, s.count) == s.index
}

// OwnerOf returns the worker index that owns name in a cluster of count workers.
func OwnerOf(name string, count int) int {
	if count <= 1 {
		return 0
	}
	if count > math.MaxInt32 {
		count = math.MaxInt32
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
