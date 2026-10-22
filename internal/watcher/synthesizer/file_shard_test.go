package synthesizer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/authshard"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestFileSynthesizer_Synthesize_AuthShardPartitions(t *testing.T) {
	t.Cleanup(func() { _, _ = authshard.Configure(config.AuthShardConfig{}) })

	dir := t.TempDir()
	const total = 20
	for i := range total {
		name := fmt.Sprintf("kiro-social-user%d.json", i)
		data := fmt.Sprintf(`{"type":"kiro","email":"user%d@example.com"}`, i)
		if errWrite := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); errWrite != nil {
			t.Fatal(errWrite)
		}
	}

	seen := make(map[string]int)
	for index := range 2 {
		if _, errConfigure := authshard.Configure(config.AuthShardConfig{Enabled: true, NodeCount: 2, NodeIndex: index}); errConfigure != nil {
			t.Fatal(errConfigure)
		}
		ctx := &SynthesisContext{
			Config:      &config.Config{},
			AuthDir:     dir,
			Now:         time.Now(),
			IDGenerator: NewStableIDGenerator(),
		}
		auths, errSynth := NewFileSynthesizer().Synthesize(ctx)
		if errSynth != nil {
			t.Fatal(errSynth)
		}
		if len(auths) == 0 || len(auths) == total {
			t.Fatalf("node %d loaded %d auths, want a strict subset of %d", index, len(auths), total)
		}
		for _, auth := range auths {
			seen[auth.ID]++
		}
	}
	if len(seen) != total {
		t.Fatalf("union of shards = %d auths, want %d", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("%s loaded by %d nodes, want exactly 1", id, n)
		}
	}
}
