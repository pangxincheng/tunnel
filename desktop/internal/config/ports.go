package config

import "fmt"

const (
	firstPort = 5102
	minPort   = 1024
	maxPort   = 65535
)

// assigned reports whether port is assigned to any device other than (server, device).
func (c *Config) assigned(port int, server, device string) bool {
	for _, s := range c.Servers {
		for d, f := range s.Forwards {
			if s.Name == server && d == device {
				continue
			}
			if f.Port == port {
				return true
			}
		}
	}
	return false
}

// AllocatePort returns the first port >= 5102 that no device is assigned and
// isFree accepts; 0 if none.
func (c *Config) AllocatePort(isFree func(int) bool) int {
	for p := firstPort; p <= maxPort; p++ {
		if !c.assigned(p, "", "") && isFree(p) {
			return p
		}
	}
	return 0
}

// CheckPort validates a port for (server, device): 1024-65535 and not used by
// any other device (on or off) across all servers.
func (c *Config) CheckPort(server, device string, port int) error {
	if port < minPort || port > maxPort {
		return fmt.Errorf("端口须在 %d–%d 之间", minPort, maxPort)
	}
	if c.assigned(port, server, device) {
		return fmt.Errorf("端口 %d 已分配给其他设备", port)
	}
	return nil
}
