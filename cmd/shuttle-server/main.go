// Command shuttle-server runs Shuttle Server as a plain program: start it,
// open the page, stop it with Ctrl-C.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/YC-BB-20/shuttle-server/internal/app"
	"github.com/YC-BB-20/shuttle-server/internal/version"
)

func main() {
	home, _ := os.UserHomeDir()
	data := flag.String("data", filepath.Join(home, "Shuttle-Server-Data"), "data folder: settings, state, logs, records, temp")
	clear := flag.Bool("clear-safe-mode", false, "leave safe mode after fixing the problem it reported")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("Shuttle Server", version.String())
		return
	}
	os.Exit(app.Run(app.Options{DataDir: *data, ClearSafeMode: *clear, Stdout: os.Stdout}))
}
