package config

import "testing"

func TestListenPort(t *testing.T) {
	cfg := &Config{Port: 8317}

	t.Setenv(PortEnv, "")
	if got := cfg.ListenPort(); got != 8317 {
		t.Fatalf("unset PORT: ListenPort() = %d, want 8317", got)
	}

	t.Setenv(PortEnv, " 9000 ")
	if got := cfg.ListenPort(); got != 9000 {
		t.Fatalf("PORT=9000: ListenPort() = %d, want 9000", got)
	}
	if cfg.Port != 8317 {
		t.Fatalf("ListenPort mutated cfg.Port to %d", cfg.Port)
	}
}

func TestPortFromEnvRejectsInvalid(t *testing.T) {
	for _, raw := range []string{"abc", "0", "-1", "65536", "80a"} {
		t.Setenv(PortEnv, raw)
		if _, err := PortFromEnv(); err == nil {
			t.Fatalf("PORT=%q should be rejected", raw)
		}
	}
}
