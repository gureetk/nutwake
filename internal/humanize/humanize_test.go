package humanize

import (
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                                     "0 s",
		4 * time.Second:                       "4 s",
		12*time.Minute + 20*time.Second:       "12 min",
		time.Hour + 5*time.Minute:             "1 h 05 min",
		26*time.Hour + 3*time.Minute:          "26 h 03 min",
		59*time.Second + 600*time.Millisecond: "1 min",
	}
	for d, want := range cases {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestPercent(t *testing.T) {
	if got := Percent(96.4); got != "96" {
		t.Errorf("got %q", got)
	}
}
