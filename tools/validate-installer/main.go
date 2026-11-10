// validate-installer checks Velopack artifacts without requiring release secrets.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tsukiyoz/resona/internal/updates"
)

func main() {
	filename := flag.String("package", "", "Velopack full nupkg")
	version := flag.String("version", "", "expected desktop version")
	flag.Parse()
	if err := updates.ValidateWindowsInstaller(*filename, *version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
