// Command ghostwire is a standalone zero-RST TCP proxy for blue/green deployments and database migration cutover.
//
// Usage:
//
//	ghostwire --config ghostwire.yaml
//	ghostwire --proxy-listen 0.0.0.0:5432 --source-backend 10.0.0.1:5432 --target-backend 10.0.0.2:5432
//
// The proxy sits between clients and a database (or any TCP service), allowing
// zero-reset cutover from a source backend to a target backend. Existing
// connections are preserved (no RST), while new connections are routed to the
// target after cutover.
package main

import (
	"fmt"
	"os"

	"github.com/aegis-migrate/portage/pkg/ghostwire"
)

func main() {
	if err := ghostwire.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ghostwire: %v\n", err)
		os.Exit(1)
	}
}
