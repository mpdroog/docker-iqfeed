package main

import (
	"flag"
	"fmt"
	"github.com/hashicorp/go-reap"
	"github.com/maurice2k/tcpserver"
	"os"
	"sync"
)

var (
	Running *sync.Map
	Verbose bool
)

func main() {
	Running = new(sync.Map)
	flag.BoolVar(&Verbose, "v", false, "Show all that happens")

	prod := os.Getenv("PROD")
	if prod == "" {
		fmt.Printf("Missing env.PROD\n")
		os.Exit(1)
		return
	}
	login := os.Getenv("LOGIN")
	if login == "" {
		fmt.Printf("Missing env.LOGIN\n")
		os.Exit(1)
		return
	}
	pass := os.Getenv("PASS")
	if pass == "" {
		fmt.Printf("Missing env.PASS\n")
		os.Exit(1)
		return
	}
	// TODO: Crash if " symbol is found in env-vars?

	// Config for all cmds
	cmds := map[string]CmdInfo{
		"xvfb": CmdInfo{Dep: "", Cmd: "/usr/bin/Xvfb", Args: []string{":0", "-screen", "0", "1024x768x24"}},
		"iqfeed": CmdInfo{Dep: "xvfb", Cmd: "wine64", Args: []string{
			"/home/wine/.wine/drive_c/Program Files/DTN/IQFeed/iqconnect.exe",
			"-product", prod,
			"-version", "IQFEED_LAUNCHER",
			"-login", login,
			"-password", pass,
			"-autoconnect",
		}},
	}
	if Verbose {
		fmt.Printf("exec=%+v\n", cmds)
	}

	// TODO: Maybe add some stupdity check for infinit waiting?
	// TODO: Some check for typo's i.e. dep to something that doesn't exist?

	// Reap processing for PID1
	if reap.IsSupported() {
		pids := make(reap.PidCh, 1)
		errors := make(reap.ErrorCh, 1)
		done := make(chan struct{})
		var reapLock sync.RWMutex
		go reap.ReapChildren(pids, errors, done, &reapLock)
		// TODO: Log reaped children?
		close(done)
	} else {
		fmt.Println("WARN: go-reap isn't supported on your platform")
	}

	var wg sync.WaitGroup
	wg.Add(len(cmds))

	ensureRunning(&wg, cmds)

	// TODO: Remaining is TCP-proxy that keeps conn in shape before offering

	// admin-port
	go admin()

	server, e := tcpserver.NewServer(":9101")
	if e != nil {
		fmt.Println(e)
		return
	}

	server.SetRequestHandler(tcpProxy)
	server.Listen()
	server.Serve()

	wg.Wait()
}
