package web

import "time"

// Text that mentions a clock time carries a token instead, and the page
// replaces it with the time in the viewer's own time zone. The Pi does not
// know what zone the person reading is in.
//
//	{{time:2026-11-13T20:19:00Z}}      8:19 pm
//	{{date:2026-11-13T20:19:00Z}}      Fri Nov 13
//	{{datetime:2026-11-13T20:19:00Z}}  Fri Nov 13, 8:19 pm

func token(kind string, t time.Time) string {
	return "{{" + kind + ":" + t.UTC().Format(time.RFC3339) + "}}"
}

func tokTime(t time.Time) string { return token("time", t) }
func tokDate(t time.Time) string { return token("date", t) }
