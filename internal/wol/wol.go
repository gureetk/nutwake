// Package wol sends Wake-on-LAN magic packets.
package wol

import (
	"bytes"
	"fmt"
	"net"
)

// Packet returns the 102-byte magic packet for mac: six 0xFF bytes followed by
// the MAC address repeated sixteen times.
func Packet(mac net.HardwareAddr) ([]byte, error) {
	if len(mac) != 6 {
		return nil, fmt.Errorf("MAC address must be 6 bytes, got %d", len(mac))
	}
	var b bytes.Buffer
	b.Write(bytes.Repeat([]byte{0xFF}, 6))
	for i := 0; i < 16; i++ {
		b.Write(mac)
	}
	return b.Bytes(), nil
}

// Send broadcasts the magic packet for mac to addr (host:port, normally the
// subnet's broadcast address and UDP port 9).
func Send(addr string, mac net.HardwareAddr) error {
	pkt, err := Packet(mac)
	if err != nil {
		return err
	}
	conn, err := net.Dial("udp4", addr)
	if err != nil {
		return fmt.Errorf("wake packet to %s: %w", addr, err)
	}
	defer conn.Close()
	if _, err := conn.Write(pkt); err != nil {
		return fmt.Errorf("wake packet to %s: %w", addr, err)
	}
	return nil
}
