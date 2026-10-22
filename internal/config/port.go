package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// PortEnv names the environment variable that overrides the configured server port.
const PortEnv = "PORT"

// PortFromEnv returns the server port set through PORT, or 0 when it is unset.
func PortFromEnv() (int, error) {
	raw := strings.TrimSpace(os.Getenv(PortEnv))
	if raw == "" {
		return 0, nil
	}
	port, errAtoi := strconv.Atoi(raw)
	if errAtoi != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid %s %q: want a port in [1, 65535]", PortEnv, raw)
	}
	return port, nil
}

// ListenPort returns the port the API server listens on. A valid PORT environment
// variable takes precedence over the configured value. The override lives only in the
// running process and is never written back to config.yaml, so nodes sharing one
// config file can listen on different ports.
func (c *Config) ListenPort() int {
	if port, errPort := PortFromEnv(); errPort == nil && port > 0 {
		return port
	}
	if c == nil {
		return 0
	}
	return c.Port
}
