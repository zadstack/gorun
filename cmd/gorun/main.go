// Command gorun runs the GoRun CMS. It is a thin wrapper around pkg/cli; a
// downstream build can import pkg/cli and pass options to add its modules and
// commands.
package main

import (
	"os"

	"github.com/zadstack/gorun/pkg/cli"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() { os.Exit(cli.Main(os.Args[1:], cli.WithVersion(version))) }
