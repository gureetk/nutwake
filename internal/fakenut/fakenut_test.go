package fakenut

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestFakeAnswersListVarAndRejectsOthers(t *testing.T) {
	srv, err := Start("127.0.0.1:0", "ups")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.SetStatus("OB DISCHRG")

	conn, err := net.DialTimeout("tcp", srv.Addr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	rd := bufio.NewReader(conn)

	fmt.Fprint(conn, "LIST VAR ups\n")
	var lines []string
	for {
		l, err := rd.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.TrimSpace(l))
		if strings.HasPrefix(l, "END LIST VAR") {
			break
		}
	}
	if lines[0] != "BEGIN LIST VAR ups" {
		t.Errorf("first line = %q", lines[0])
	}
	found := false
	for _, l := range lines {
		if l == `VAR ups ups.status "OB DISCHRG"` {
			found = true
		}
	}
	if !found {
		t.Errorf("status line missing from %v", lines)
	}

	fmt.Fprint(conn, "BOGUS\n")
	if l, _ := rd.ReadString('\n'); !strings.HasPrefix(l, "ERR UNKNOWN-COMMAND") {
		t.Errorf("unknown command reply = %q", l)
	}
	fmt.Fprint(conn, "LOGOUT\n")
	if l, _ := rd.ReadString('\n'); !strings.HasPrefix(l, "OK") {
		t.Errorf("logout reply = %q", l)
	}
}
