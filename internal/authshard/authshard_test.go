package authshard

import (
	"fmt"
	"testing"
)

func resetShard(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { _, _ = Configure(0, 0) })
}

func TestOwnsDisabled(t *testing.T) {
	resetShard(t)
	for _, count := range []int{0, 1} {
		if _, err := Configure(count, 0); err != nil {
			t.Fatalf("Configure(%d,0): %v", count, err)
		}
		if !Owns("kiro-social-a@b.com.json") {
			t.Fatalf("count=%d should own every file", count)
		}
	}
}

func TestConfigureValidation(t *testing.T) {
	resetShard(t)
	for _, tc := range []struct{ count, index int }{{3, 3}, {3, -1}, {2, 5}} {
		if _, err := Configure(tc.count, tc.index); err == nil {
			t.Fatalf("Configure(%d,%d) should fail", tc.count, tc.index)
		}
	}
	changed, err := Configure(3, 1)
	if err != nil || !changed {
		t.Fatalf("Configure(3,1) changed=%v err=%v", changed, err)
	}
	changed, err = Configure(3, 1)
	if err != nil || changed {
		t.Fatalf("repeat Configure(3,1) changed=%v err=%v", changed, err)
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
			if _, err := Configure(count, idx); err != nil {
				t.Fatal(err)
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
