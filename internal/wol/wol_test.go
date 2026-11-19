package wol

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestPacketLayout(t *testing.T) {
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:01")
	p, err := Packet(mac)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 102 {
		t.Fatalf("len = %d, want 102", len(p))
	}
	if !bytes.Equal(p[:6], bytes.Repeat([]byte{0xFF}, 6)) {
		t.Errorf("header = % x", p[:6])
	}
	for i := 0; i < 16; i++ {
		if !bytes.Equal(p[6+i*6:12+i*6], mac) {
			t.Errorf("repetition %d = % x", i, p[6+i*6:12+i*6])
		}
	}
}

func TestPacketRejectsWrongLength(t *testing.T) {
	for _, mac := range []net.HardwareAddr{nil, {1, 2, 3}, make(net.HardwareAddr, 8)} {
		if _, err := Packet(mac); err == nil {
			t.Errorf("Packet(%v) should fail", mac)
		}
	}
}

func TestSendDeliversThePacket(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:02")
	if err := Send(pc.LocalAddr().String(), mac); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := Packet(mac)
	if !bytes.Equal(buf[:n], want) {
		t.Errorf("received % x", buf[:n])
	}
}

func TestSendErrors(t *testing.T) {
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:02")
	if err := Send("not an address", mac); err == nil {
		t.Error("expected an error for a bad address")
	}
	if err := Send("127.0.0.1:9", net.HardwareAddr{1, 2}); err == nil {
		t.Error("expected an error for a bad MAC")
	}
}
