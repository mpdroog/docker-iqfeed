package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

/** CmdInfo is the command information to run a binary as child */
type CmdInfo struct {
	Cmd  string
	Args []string
	Dep  string

	// Post-processing
	PostCmd  string
	PostArgs []string
}

/** run executes a command and stores if it's running after 1sec in the Running-map */
func run(name, path string, flags []string) error {
	base := filepath.Dir(path)
	if e := os.Chdir(base); e != nil {
		return e
	}

	ctxb := context.Background()
	// DevNote: yes no context timeout as we want to run as long as possible
	cmd := exec.CommandContext(ctxb, path, flags...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout

	// Now some dark magic to only
	// mark it as running after 1min to prevent flip-flopping
	var wg sync.WaitGroup
	wg.Add(1)

	var e error
	run := true
	go func() {
		// Blocking action in separate go-routine
		e = cmd.Run()
		run = false
		wg.Done()
	}()

	// Wait X-sec before marking as running
	time.Sleep(time.Second * 1)
	if Verbose {
		slog.Info("exec[run] wakeup", "name", name)
	}

	if run {
		// Save state
		Running.Store(name, cmd.Process.Pid)
		if Verbose {
			slog.Info("exec[run] running", "name", name, "pid", cmd.Process.Pid)
		}
	}

	wg.Wait()
	Running.Delete(name)

	if e == nil && cmd.ProcessState.ExitCode() != 0 {
		e = fmt.Errorf("[%s] exited with exit=%d", name, cmd.ProcessState.ExitCode())
	}

	return e
}

// ensureRunning ensures all given cmds are running
// and else it respawns them in the order given
func ensureRunning(wg *sync.WaitGroup, cmds map[string]CmdInfo) {
	for name, info := range cmds {
		go func(name string, info CmdInfo) {
			defer wg.Done()
			//var lastSleep int64

			for {
				if info.Dep != "" {
					for {
						if _, ok := Running.Load(info.Dep); ok == true {
							// Service avail
							if Verbose {
								slog.Info("exec[ensureRunning] depAvail", "name", name)
							}
							break
						}
						if Verbose {
							slog.Info("exec[ensureRunning] await", "name", name, "dep", info.Dep)
						}
						time.Sleep(time.Millisecond * 250) // 0.25sec
					}
				}
				e := run(name, info.Cmd, info.Args)
				slog.Error("exec[ensureRunning] process.Stop", "name", name, "e", e.Error())

				if len(info.PostCmd) > 0 {
					// Run something after the process stopped
					if e := run(name, info.PostCmd, info.PostArgs); e != nil {
						slog.Error("exec[ensureRunning] PostCmd", "name", name, "e", e.Error())
					}
				}

				/*if time.Now().Unix()-lastSleep < 10 {
					// prevent hammering less than 10sec ago, sleep!
					fmt.Printf("[%s] (sleep 5sec)\n", name)
					time.Sleep(time.Second * 5)
				}*/
				//lastSleep = time.Now().Unix()
				if Verbose {
					slog.Info("exec[ensureRunning] forNext", "name", name)
				}
			}
		}(name, info)
	}
}
