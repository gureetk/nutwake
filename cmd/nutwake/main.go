// Command nutwake keeps a homelab safe through power cuts: it shuts machines
// down cleanly when a UPS runs low, wakes them when power is back, and serves
// a status page with the history.
package main

import (
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println("nutwake", version)
		return
	}
	fmt.Fprintln(os.Stderr, "nutwake is under construction. Try: nutwake version")
	os.Exit(2)
}
