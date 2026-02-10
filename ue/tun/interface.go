package tun

import (
	"fmt"
	"os/exec"

	"github.com/songgao/water"
)

// TUNInterface represents a TUN network interface
type TUNInterface struct {
	iface *water.Interface
	name  string
	ip    string
}

// NewTUNInterface creates and configures a new TUN interface
func NewTUNInterface(name string, ip string) (*TUNInterface, error) {
	tunCfg := water.Config{
		DeviceType: water.TUN,
	}
	tunCfg.Name = name

	iface, err := water.New(tunCfg)
	if err != nil {
		return nil, fmt.Errorf("error creating TUN device: %w", err)
	}

	// Configure IP address, bring up interface, and add route for internet (via TUN)
	cmds := [][]string{
		{"ip", "addr", "add", fmt.Sprintf("%s/32", ip), "dev", name},
		{"ip", "link", "set", "dev", name, "up"},
		// Add route for common internet destinations
		{"ip", "route", "add", "8.8.8.8/32", "dev", name},
		{"ip", "route", "add", "1.1.1.1/32", "dev", name},
	}

	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			// Ignore route errors (might already exist)
			if cmd[0] == "ip" && cmd[1] == "route" {
				continue
			}
			iface.Close()
			return nil, fmt.Errorf("error configuring TUN device: %w", err)
		}
	}

	return &TUNInterface{
		iface: iface,
		name:  name,
		ip:    ip,
	}, nil
}

// Read reads a packet from the TUN interface
func (t *TUNInterface) Read(buf []byte) (int, error) {
	return t.iface.Read(buf)
}

// Write writes a packet to the TUN interface
func (t *TUNInterface) Write(packet []byte) (int, error) {
	return t.iface.Write(packet)
}

// Close brings down and removes the TUN interface
func (t *TUNInterface) Close() error {
	cmds := [][]string{
		{"ip", "route", "del", "8.8.8.8/32", "dev", t.name},
		{"ip", "route", "del", "1.1.1.1/32", "dev", t.name},
		{"ip", "link", "set", "dev", t.name, "down"},
		{"ip", "addr", "flush", "dev", t.name},
	}

	for _, cmd := range cmds {
		exec.Command(cmd[0], cmd[1:]...).Run() // Ignore errors on cleanup
	}

	return t.iface.Close()
}

// GetName returns the TUN interface name
func (t *TUNInterface) GetName() string {
	return t.name
}

// GetIP returns the configured IP address
func (t *TUNInterface) GetIP() string {
	return t.ip
}
