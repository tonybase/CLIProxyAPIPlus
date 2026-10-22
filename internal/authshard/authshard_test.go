package authshard

import (
	"fmt"
	"math"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func resetShard(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { _, _ = Configure(config.AuthShardConfig{}) })
}

func worker(count, index int) config.AuthShardConfig {
	return config.AuthShardConfig{Enabled: true, NodeCount: count, NodeIndex: index}
}

func TestOwnsDisabled(t *testing.T) {
	resetShard(t)
	for _, cfg := range []config.AuthShardConfig{{}, {NodeCount: 3, NodeIndex: 1}, worker(1, 0)} {
		if _, err := Configure(cfg); err != nil {
			t.Fatalf("Configure(%+v): %v", cfg, err)
		}
		if Enabled() || !Owns("kiro-social-a@b.com.json") || !Refreshes("kiro-social-a@b.com.json") {
			t.Fatalf("cfg %+v should own and refresh every file", cfg)
		}
	}
}

func TestConfigureValidation(t *testing.T) {
	resetShard(t)
	invalid := []config.AuthShardConfig{
		worker(3, 3),
		worker(3, -1),
		worker(0, 0),
		worker(math.MaxInt32+1, 0),
		{Enabled: true, NodeCount: 2, Role: "observer"},
	}
	for _, cfg := range invalid {
		if _, err := Configure(cfg); err == nil {
			t.Fatalf("Configure(%+v) should fail", cfg)
		}
	}
	changed, err := Configure(worker(3, 1))
	if err != nil || !changed {
		t.Fatalf("Configure(3,1) changed=%v err=%v", changed, err)
	}
	changed, err = Configure(worker(3, 1))
	if err != nil || changed {
		t.Fatalf("repeat Configure(3,1) changed=%v err=%v", changed, err)
	}
	if _, err = Configure(worker(3, 9)); err == nil {
		t.Fatal("Configure(3,9) should fail")
	}
	if count, index := Layout(); count != 3 || index != 1 {
		t.Fatalf("invalid config replaced layout: got %d/%d, want 3/1", index, count)
	}
}

func TestManagerOwnsAllRefreshesNone(t *testing.T) {
	resetShard(t)
	if _, err := Configure(config.AuthShardConfig{Enabled: true, Role: "Manager"}); err != nil {
		t.Fatal(err)
	}
	if !Enabled() || !IsManager() {
		t.Fatal("manager role should be enabled")
	}
	for i := range 20 {
		name := fmt.Sprintf("kiro-social-user%d.json", i)
		if !Owns(name) {
			t.Fatalf("manager should own %s", name)
		}
		if Refreshes(name) {
			t.Fatalf("manager should not refresh %s", name)
		}
	}
}

func TestResolveEnvOverrides(t *testing.T) {
	resetShard(t)
	t.Setenv("AUTH_SHARD_ENABLED", "true")
	t.Setenv("AUTH_SHARD_COUNT", "4")
	t.Setenv("AUTH_SHARD_INDEX", " 2 ")
	t.Setenv("AUTH_SHARD_ROLE", "worker")
	cfg := config.AuthShardConfig{NodeCount: 3}
	if _, err := Configure(cfg); err != nil {
		t.Fatal(err)
	}
	if count, index := Layout(); count != 4 || index != 2 {
		t.Fatalf("layout = %d/%d, want 2/4", index, count)
	}
	if cfg.NodeCount != 3 || cfg.Enabled {
		t.Fatalf("Configure mutated the config value: %+v", cfg)
	}
}

func TestResolveInvalidEnvFails(t *testing.T) {
	resetShard(t)
	t.Setenv("AUTH_SHARD_INDEX", "node-2")
	if _, err := Configure(worker(3, 0)); err == nil {
		t.Fatal("invalid AUTH_SHARD_INDEX should fail")
	}
}

func TestOwnershipPartitionsFiles(t *testing.T) {
	resetShard(t)
	const total = 1000
	const count = 3
	owners := make([]int, count)
	for i := range total {
		name := fmt.Sprintf("kiro-social-user%d@example.com.json", i)
		hits := 0
		for idx := range count {
			if _, err := Configure(worker(count, idx)); err != nil {
				t.Fatal(err)
			}
			if Owns(name) != Refreshes(name) {
				t.Fatalf("worker Owns and Refreshes disagree for %s", name)
			}
			if Owns(name) {
				hits++
				owners[idx]++
			}
		}
		if hits != 1 {
			t.Fatalf("%s owned by %d nodes, want exactly 1", name, hits)
		}
	}
	expected := total / count
	for idx, n := range owners {
		if n < expected*9/10 || n > expected*11/10 {
			t.Fatalf("node %d owns %d files, want about %d", idx, n, expected)
		}
	}
}

func TestScaleOutMovesFewFiles(t *testing.T) {
	const total = 2000
	moved := 0
	for i := range total {
		name := fmt.Sprintf("kiro-idc-%05d.json", i)
		if OwnerOf(name, 3) != OwnerOf(name, 4) {
			moved++
		}
	}
	// Ideal is 1/4 of the files; allow some slack.
	if moved < total*20/100 || moved > total*30/100 {
		t.Fatalf("moved %d of %d files when scaling 3->4, want about 25%%", moved, total)
	}
}

func TestOwnerOfIgnoresCaseAndDir(t *testing.T) {
	a := OwnerOf("/mnt/nfs/auths/Kiro-Social-X.json", 5)
	b := OwnerOf("kiro-social-x.json", 5)
	if a != b {
		t.Fatalf("owner differs by path/case: %d vs %d", a, b)
	}
}
