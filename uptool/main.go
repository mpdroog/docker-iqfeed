package main

import (
	"bytes"
	"bufio"
	"context"
	"fmt"
	"github.com/hashicorp/go-reap"
	"github.com/maurice2k/tcpserver"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const defaultConnectTimeout = 3 * time.Second
/* EOM is End Of Message stream */
const EOM = "!ENDMSG!,"

var (
	Running *sync.Map
)

type CmdInfo struct {
	Cmd  string
	Args []string

	Dep string
}

func run(name, path string, flags []string) error {
	base := filepath.Dir(path)
	if e := os.Chdir(base); e != nil {
		return e
	}

	ctxb := context.Background()
	ctx, cancel := context.WithTimeout(ctxb, 1*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, flags...)
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
	fmt.Printf("[%s] still running\n", name)

	if run {
		// Save state
		Running.Store(name, struct{}{})
		fmt.Printf("[%s] marked as running\n", name)
	}

	wg.Wait()
	Running.Delete(name)

	return e
}

func main() {
	Running = new(sync.Map)

	// TODO: os.Exit(1) ?
	prod := os.Getenv("PROD")
	if prod == "" {
		fmt.Printf("Missing env.PROD\n")
		return
	}
	login := os.Getenv("LOGIN")
	if login == "" {
		fmt.Printf("Missing env.LOGIN\n")
		return
	}
	pass := os.Getenv("PASS")
	if pass == "" {
		fmt.Printf("Missing env.PASS\n")
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
	fmt.Printf("exec=%+v\n", cmds)

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
		fmt.Println("Sorry, go-reap isn't supported on your platform.")
	}

	var wg sync.WaitGroup
	wg.Add(len(cmds))

	for name, info := range cmds {
		go func(name string, info CmdInfo) {
			defer wg.Done()
			var lastSleep int64

			for {
				if info.Dep != "" {
					for {
						if _, ok := Running.Load(info.Dep); ok == true {
							// Service avail
							fmt.Printf("[%s] dep avail\n", name)
							break
						}
						fmt.Printf("[%s] await %s\n", name, info.Dep)
						time.Sleep(time.Second)
					}
				}
				e := run(name, info.Cmd, info.Args)
				if e != nil {
					fmt.Printf("[%s] %s\n", name, e.Error())
				}

				if time.Now().Unix()-lastSleep < 10 {
					// less than 10sec ago, sleep!
					fmt.Printf("[%s] (sleep 5sec)\n", name)
					time.Sleep(time.Second * 5)
				}
				lastSleep = time.Now().Unix()
				fmt.Printf("[%s] respawn\n", name)
			}
		}(name, info)
	}

	// TODO: Remaining is TCP-proxy that keeps conn in shape before offering

	// admin-port
	go func() {
		init := true
		for {
			if init == false {
				// Always sleep after first try
				time.Sleep(time.Second * 3)
			}
			init = false

			// wait for running
			for {
				name := "iqfeed"
				if _, ok := Running.Load(name); ok == true {
					// Service avail
					fmt.Printf("[%s] dep avail\n", name)
					break
				}
				fmt.Printf("[%s] await\n", name)
				time.Sleep(time.Second)
			}

			// TODO: Timeouts?

			fmt.Printf("[keepalive] connect\n")
			// Keep alive conn
			conn, e := net.Dial("tcp", "127.0.0.1:9300")
			if e != nil {
				fmt.Printf("[keepalive.Dial] e=%s\n", e.Error())
				continue
			}

			c := bufio.NewReader(conn)

			// Check if conn working
			{
				if _, e := conn.Write([]byte("T\r\n")); e != nil {
					conn.Close() // TODO: err?
					fmt.Printf("[keepalive.WriteT] e=%s\n", e.Error())
					continue
				}
				line, _, e := c.ReadLine()
				if e != nil {
					conn.Close() // TODO: err?
					fmt.Printf("[keepalive.ReadLineT] e=%s\n", e.Error())
					continue
				}
				fmt.Printf("[keepalive.ReadLineT] %s\n", line)
			}


			connectCount := 0
			firstPkg := true
			for {
				line, _, e := c.ReadLine()
				if e != nil {
					fmt.Printf("[keepalive.WriteTS] e=%s\n", e.Error())
					break
				}
				fmt.Printf("[keepalive.ReadLine] %s\n", line)

				// S,STATS,,,0,0,1,0,0,0,,,Not Connected,6.2.0.25,\"490914\",0,0.0,0.0,0.08,0.08,0.08,
				if bytes.HasPrefix(line, []byte("S,STATS")) {
					tok := bytes.SplitN(line, []byte(","), 16)
					if bytes.Equal(tok[12], []byte("Not Connected")) {
						if _, e := conn.Write([]byte("S,CONNECT\r\n")); e != nil {
							fmt.Printf("[keepalive.Write CONNECT] e=%s\n", e.Error())
							break
						}
						fmt.Printf("[keepalive.Write] >> S,CONNECT\n")
						if (connectCount >= 10) {
							fmt.Printf("[keepalive.Timeout] failed connecting (10 attempts)\n")
							break
						}
						connectCount++
					} else if firstPkg && bytes.Equal(tok[12], []byte("Connected")) {
						firstPkg = false
						Running.Store("admin", struct{}{})						
					}
				}
			}
			Running.Delete("admin")

			if e := conn.Close(); e != nil {
				fmt.Printf("[keepalive.Close] e=%s\n", e.Error())
			}
		}
	}()

	server, e := tcpserver.NewServer(":9101")
	if e != nil {
		fmt.Println(e)
		return
	}

	server.SetRequestHandler(handleConn)
	server.Listen()
	server.Serve()

	wg.Wait()
}

func handleConn(conn tcpserver.Connection) {
	defer func() {
		if e := conn.Close(); e != nil {
			fmt.Errorf("handleConn.Close: %s\n", e.Error())
		}
	}()
	fmt.Printf("handleConn: new req\n")

	if _, ok := Running.Load("iqfeed"); !ok {
		conn.Write([]byte("E,NO_DAEMON\r\n"))
		return		
	}
	if _, ok := Running.Load("admin"); !ok {
		conn.Write([]byte("E,NO_ADMIN\r\n"))
		return		
	}

	// Start the clock
	// TODO: 50sec fine?
	dur, e := time.ParseDuration("12s")
	if e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		conn.Write([]byte("E,PARSE_DURATION\r\n"))
		return
	}
	deadline := time.Now().Add(dur)

	if e := conn.SetDeadline(deadline); e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		conn.Write([]byte("E,CONN_SET_DEADLINE\r\n"))
		return
	}

	upConn, e := net.DialTimeout("tcp", "127.0.0.1:9100", defaultConnectTimeout)
	if e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		conn.Write([]byte("E,UPSTREAM CONN_TIMEOUT\r\n"))
		return
	}
	defer upConn.Close()

	// Test if upstream conn is usable
	{
		if _, e := upConn.Write([]byte("TEST\r\n")); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
			conn.Write([]byte("E,UPSTREAM_T\r\n"))
			return
		}
		b := make([]byte, len("E,!SYNTAX_ERROR!,"))
		if _, e := upConn.Read(b); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
			conn.Write([]byte("E,UPSTREAM_T_RES\r\n"))
			return
		}
		if !bytes.Equal(b, []byte("E,!SYNTAX_ERROR!,")) {
			fmt.Printf("handleConn: invalid res=%s\n", b)
			conn.Write([]byte("E,UPSTREAM_T_INV\r\n"))
			return
		}
	}

	if e := upConn.SetDeadline(deadline); e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		conn.Write([]byte("E,UPSTREAM SET_DEADLINE\r\n"))
		return
	}

	// Inform client the connection is ready
	if _, e := conn.Write([]byte("READY\r\n")); e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		return
	}

	// Streaming cmds
	streamReplies := map[string]struct{}{
		"HDX,": struct{}{},
		"HWX,": struct{}{},
		"HMX,": struct{}{},
		"HTD,": struct{}{},
		"HTT,": struct{}{},
		"HIX": struct{}{},
		"HID": struct{}{},
		"HIT": struct{}{},
		"HDT,": struct{}{},
	}

	// Own proxy
	r := bufio.NewReader(conn)
	r2 := bufio.NewReader(upConn)

	for {
		// 0. timeouts
		// increase timeout upon receiving data
		deadline := time.Now().Add(dur)

		if e := conn.SetDeadline(deadline); e != nil {
			fmt.Errorf("conn.SetDeadline e=%s\n", e.Error())
			break
		}
		if e := upConn.SetDeadline(deadline); e != nil {
			fmt.Errorf("upConn.SetDeadline e=%s\n", e.Error())
			break
		}

		// 1. client cmd
		bin, e := r.ReadBytes(byte('\n'))
		bin = bytes.TrimSpace(bin)
		if e != nil {
			fmt.Errorf("conn.ReadBytes e=%s\n", e.Error())
			break
		}
		cmd := bin[bytes.Index(bin, []byte(",")):]

		if _, e := upConn.Write(bin); e != nil {
			fmt.Errorf("upConn.Write e=%s\n", e.Error())
			break
		}
		if _, e := upConn.Write([]byte("\r\n")); e != nil {
			fmt.Errorf("upConn.Write e=%s\n", e.Error())
			break
		}

		// 2. stream?
		if _, isStream := streamReplies[string(cmd)]; isStream {
			for {
				// read until EOM
				bin, e := r2.ReadBytes(byte('\n'))
				bin = bytes.TrimSpace(bin)
				if e != nil {
					fmt.Errorf("upConn.Read e=%s\n", e.Error())
					break
				}
				if _, e := conn.Write(bin); e != nil {
					fmt.Errorf("conn.Write e=%s\n", e.Error())
					break
				}
				if _, e := conn.Write([]byte("\r\n")); e != nil {
					fmt.Errorf("conn.Write e=%s\n", e.Error())
					break
				}
				if bytes.Equal(bin, []byte(EOM)) {
					// Done!
					break
				}	
			}
		} else {
			// One reply
			bin, e := r2.ReadBytes(byte('\n'))
			bin = bytes.TrimSpace(bin)
			if e != nil {
				fmt.Errorf("upConn.Read e=%s\n", e.Error())
				break
			}
			if _, e := conn.Write(bin); e != nil {
				fmt.Errorf("conn.Write e=%s\n", e.Error())
				break
			}
			if _, e := conn.Write([]byte("\r\n")); e != nil {
				fmt.Errorf("conn.Write e=%s\n", e.Error())
				break
			}			
		}
	}
}
