// Command shuttle-server runs Shuttle Server as a plain program: start it,
// open the page, stop it with Ctrl-C or the menu bar icon.
//
//	shuttle-server [-data DIR] [-clear-safe-mode]   run
//	shuttle-server -version
//	shuttle-server hash FILE...                      XXH128, as xxhsum -H2 prints it
//	shuttle-server install-launcher                  make "Shuttle Server.app" (macOS)
//	shuttle-server soak -dir DIR [-minutes N] [-files N]
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/YC-BB-20/shuttle-server/internal/app"
	"github.com/YC-BB-20/shuttle-server/internal/config"
	"github.com/YC-BB-20/shuttle-server/internal/diskio"
	"github.com/YC-BB-20/shuttle-server/internal/launcher"
	"github.com/YC-BB-20/shuttle-server/internal/soak"
	"github.com/YC-BB-20/shuttle-server/internal/version"
	"github.com/YC-BB-20/shuttle-server/internal/xxh3"
)

func main() {
	home, _ := os.UserHomeDir()
	defData := filepath.Join(home, "Shuttle-Server-Data")
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hash":
			os.Exit(hash(os.Args[2:]))
		case "install-launcher":
			os.Exit(installLauncher(defData))
		case "soak":
			os.Exit(runSoak(os.Args[2:]))
		}
	}
	data := flag.String("data", defData, "data folder: settings, state, logs, records, temp")
	clear := flag.Bool("clear-safe-mode", false, "leave safe mode after fixing the problem it reported")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("Shuttle Server", version.String())
		return
	}
	code := app.Run(app.Options{DataDir: *data, ClearSafeMode: *clear, Stdout: os.Stdout})
	if code == app.ExitRestart {
		// Start again in place: same process, same arguments, new program.
		err := syscall.Exec(app.RestartPath, os.Args, os.Environ())
		fmt.Fprintln(os.Stderr, "Could not restart Shuttle Server:", err)
		code = 1
	}
	os.Exit(code)
}

func hash(files []string) int {
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "usage: shuttle-server hash FILE...")
		return 2
	}
	code := 0
	buf := make([]byte, 4<<20)
	for _, p := range files {
		f, err := diskio.OpenRead(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
			continue
		}
		h := xxh3.New()
		for {
			n, err := f.Read(buf)
			h.Write(buf[:n])
			if err != nil {
				break
			}
		}
		f.Close()
		fmt.Printf("%s  %s\n", h.Sum128(), p)
	}
	return code
}

func installLauncher(data string) int {
	bin, err := os.Executable()
	if err == nil {
		bin, err = filepath.EvalSymlinks(bin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	port := "8470"
	if c, _, err := config.Load(filepath.Join(data, "config.json")); err == nil {
		if _, p, err := net.SplitHostPort(c.Listen); err == nil {
			port = p
		}
	}
	app, err := launcher.Install(bin, data, port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not make the launcher:", err)
		return 1
	}
	fmt.Println("Made", app)
	fmt.Println("Double-click it in Finder (Go → Home → Applications), or press Cmd-Space and type Shuttle Server.")
	return 0
}

func runSoak(args []string) int {
	fs := flag.NewFlagSet("soak", flag.ExitOnError)
	dir := fs.String("dir", "", "an empty test folder on the disk to test (required)")
	minutes := fs.Int("minutes", 60, "how long to run")
	files := fs.Int("files", 100000, "number of small files")
	fs.Parse(args)
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "usage: shuttle-server soak -dir FOLDER [-minutes N] [-files N]")
		return 2
	}
	os.MkdirAll(*dir, 0o755)
	s, err := soak.Run(soak.Options{Dir: *dir, Files: *files, BigFiles: 4, BigSize: 1 << 30,
		Duration: time.Duration(*minutes) * time.Minute, Every: time.Minute, Out: os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Soak stopped:", err)
		return 1
	}
	ok, why := soak.Flat(s)
	fmt.Println("Result:", why, "— readings in", filepath.Join(*dir, "soak.csv"))
	if !ok {
		return 1
	}
	return 0
}
