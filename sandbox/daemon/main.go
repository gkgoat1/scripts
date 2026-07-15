package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

type process struct{ pid, pgid int }

func main() {
	if len(os.Args) >= 4 && os.Args[1] == "--client" {
		client(os.Args[2], os.Args[3:])
		return
	}
	socket := "/tmp/sandboxd.sock"
	if len(os.Args) == 3 && os.Args[1] == "--socket" {
		socket = os.Args[2]
	}
	_ = os.Remove(socket)
	if err := os.MkdirAll(dir(socket), 0700); err != nil {
		panic(err)
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		panic(err)
	}
	defer os.Remove(socket)
	defer l.Close()
	_ = os.Chmod(socket, 0600)
	procs := map[int]process{}
	var mu sync.Mutex
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		mu.Lock()
		defer mu.Unlock()
		for _, p := range procs {
			killGroup(p)
		}
		os.Exit(0)
	}()
	for {
		c, err := l.Accept()
		if err == nil {
			go handle(c, procs, &mu)
		}
	}
}
func client(socket string, args []string) {
	c, err := net.Dial("unix", socket)
	if err != nil {
		os.Exit(1)
	}
	defer c.Close()
	fmt.Fprintln(c, strings.Join(args, " "))
	_, _ = io.Copy(os.Stdout, c)
}

func dir(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		if i == 0 {
			return "/"
		}
		return p[:i]
	}
	return "."
}
func killGroup(p process) {
	if p.pgid > 0 {
		_ = syscall.Kill(-p.pgid, syscall.SIGKILL)
	}
	_ = syscall.Kill(p.pid, syscall.SIGKILL)
}
func handle(c net.Conn, procs map[int]process, mu *sync.Mutex) {
	defer c.Close()
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return
	}
	f := strings.Fields(line)
	if len(f) == 0 {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	switch strings.ToUpper(f[0]) {
	case "PING":
		fmt.Fprintln(c, "OK")
	case "REGISTER":
		if len(f) >= 2 {
			pid, _ := strconv.Atoi(f[1])
			pgid := pid
			if len(f) > 2 {
				pgid, _ = strconv.Atoi(f[2])
			}
			procs[pid] = process{pid, pgid}
			fmt.Fprintln(c, "OK")
		}
	case "KILL":
		if len(f) > 1 {
			pid, _ := strconv.Atoi(f[1])
			if p, ok := procs[pid]; ok {
				killGroup(p)
				delete(procs, pid)
			}
			fmt.Fprintln(c, "OK")
		}
	case "KILLALL":
		for pid, p := range procs {
			killGroup(p)
			delete(procs, pid)
		}
		fmt.Fprintln(c, "OK")
	case "LIST":
		for _, p := range procs {
			fmt.Fprintf(c, "%d %d\n", p.pid, p.pgid)
		}
	}
}
