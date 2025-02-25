package main

import (
	"flag"
	"fmt"
	"github.com/hashicorp/go-reap"
	"github.com/maurice2k/tcpserver"
	"log/slog"
	"os"
	"sync"
)

var (
	Running *sync.Map
	Verbose bool
)

func main() {
	l := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(l)

	Running = new(sync.Map)
	flag.BoolVar(&Verbose, "v", false, "Show all that happens")
	flag.Parse()

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
	vv := os.Getenv("VERBOSE")
	if vv != "" {
		Verbose = true
		slog.Info("main[Verbose] set through environment", "verbose", Verbose)
	}
	// TODO: Crash if " symbol is found in env-vars?

	// Config for all cmds
	cmds := map[string]CmdInfo{
		"xvfb": CmdInfo{Dep: "", Cmd: "/usr/bin/Xvfb", Args: []string{":0", "-screen", "0", "1024x768x24", "-noreset"}},
		"iqfeed": CmdInfo{Dep: "xvfb", Cmd: "wine64", Args: []string{
			"/home/wine/.wine/drive_c/Program Files/DTN/IQFeed/iqconnect.exe",
			"-product", prod,
			"-version", "IQFEED_LAUNCHER",
			"-login", login,
			"-password", pass,
			"-autoconnect",
		}, PostCmd: "mv", PostArgs: []string{"/home/wine/.wine/drive_c/users/wine/Documents/DTN/IQFeed/IQConnectLog.txt", "/home/wine/IQConnectLog.crash.txt"}},
	}
	if Verbose {
		slog.Info("main[exec]", "cmds", cmds)
	}

	// TODO: Maybe add some stupdity check for infinit waiting?
	// TODO: Some check for typo's i.e. dep to something that doesn't exist?

	// Reap processing for PID1
	if reap.IsSupported() {
		go reap.ReapChildren(nil, nil, nil, nil)
	} else {
		slog.Warn("main[reap]", "go-reap isn't supported on your platform")
	}

	var wg sync.WaitGroup
	wg.Add(len(cmds))

	ensureRunning(&wg, cmds)

	ConnKeepAliveInit()

	// Admin monitoring
	go admin()
	// Client that keeps everything open
	go keepalive("127.0.0.1:5009")
	// HTTP-server
	go httpListen(":8080")

	// TCP-server
	{
		server, e := tcpserver.NewServer(":9101")
		if e != nil {
			slog.Error("tcpserver.NewServer", "e", e.Error())
			return
		}

		server.SetRequestHandler(tcpProxy)
		server.Listen()
		server.Serve()
	}

	// Wait for child processes to exit
	wg.Wait()
}
