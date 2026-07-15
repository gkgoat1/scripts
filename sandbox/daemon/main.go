package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

type process struct{ pid, pgid int }
type server struct {
	socket, shim string
	procs        map[int]process
	mu           sync.Mutex
}

func main() {
	if len(os.Args) >= 4 && os.Args[1] == "--client" {
		client(os.Args[2], os.Args[3:])
		return
	}
	s := &server{socket: "/tmp/sandboxd.sock", procs: map[int]process{}}
	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--socket":
			if i+1 < len(os.Args) {
				s.socket = os.Args[i+1]
				i++
			}
		case "--shim":
			if i+1 < len(os.Args) {
				s.shim = os.Args[i+1]
				i++
			}
		}
	}
	_ = os.Remove(s.socket)
	if err := os.MkdirAll(filepath.Dir(s.socket), 0700); err != nil {
		panic(err)
	}
	l, err := net.Listen("unix", s.socket)
	if err != nil {
		panic(err)
	}
	defer os.Remove(s.socket)
	defer l.Close()
	_ = os.Chmod(s.socket, 0600)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		s.mu.Lock()
		for _, p := range s.procs {
			killGroup(p)
		}
		s.mu.Unlock()
		os.Exit(0)
	}()
	for {
		c, err := l.Accept()
		if err == nil {
			go s.handle(c)
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
func killGroup(p process) {
	if p.pgid > 0 {
		_ = syscall.Kill(-p.pgid, syscall.SIGKILL)
	}
	_ = syscall.Kill(p.pid, syscall.SIGKILL)
}
func (s *server) handle(c net.Conn) {
	defer c.Close()
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return
	}
	line = strings.TrimSuffix(line, "\n")
	if strings.HasPrefix(line, "REWRITE ") {
		p, err := s.rewrite(strings.TrimPrefix(line, "REWRITE "))
		if err != nil {
			fmt.Fprintf(c, "ERR %s\n", err)
			return
		}
		fmt.Fprintf(c, "OK %s\n", p)
		return
	}
	f := strings.Fields(line)
	if len(f) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
			s.procs[pid] = process{pid, pgid}
			fmt.Fprintln(c, "OK")
		}
	case "KILL":
		if len(f) > 1 {
			pid, _ := strconv.Atoi(f[1])
			if p, ok := s.procs[pid]; ok {
				killGroup(p)
				delete(s.procs, pid)
			}
			fmt.Fprintln(c, "OK")
		}
	case "KILLALL":
		for pid, p := range s.procs {
			killGroup(p)
			delete(s.procs, pid)
		}
		fmt.Fprintln(c, "OK")
	case "LIST":
		for _, p := range s.procs {
			fmt.Fprintf(c, "%d %d\n", p.pid, p.pgid)
		}
	}
}

func (s *server) rewrite(src string) (string, error) {
	if s.shim == "" {
		return "", fmt.Errorf("daemon has no --shim")
	}
	in, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	shim, err := os.ReadFile(s.shim)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(in)
	h.Write(shim)
	name := hex.EncodeToString(h.Sum(nil))
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "sandbox", "binaries")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	out := filepath.Join(dir, name)
	if st, e := os.Stat(out); e == nil && st.Mode()&0111 != 0 {
		return out, nil
	}
	if err = rewriteMachO(in, out, s.shim); err != nil {
		return "", err
	}
	_ = os.Chmod(out, 0755)
	if _, e := os.Stat("/usr/bin/codesign"); e == nil {
		if err := exec.Command("codesign", "--force", "--sign", "-", "--timestamp=none", out).Run(); err != nil {
			return "", fmt.Errorf("codesign: %w", err)
		}
	}
	return out, nil
}

// rewriteMachO adds LC_LOAD_DYLIB into existing Mach-O header padding. It never
// shifts segments or modifies offsets, and refuses binaries without safe padding.
func rewriteMachO(in []byte, out, name string) error {
	if len(in) < 32 || binary.LittleEndian.Uint32(in) != 0xfeedfacf {
		return fmt.Errorf("unsupported Mach-O: expected thin 64-bit binary")
	}
	ncmd := binary.LittleEndian.Uint32(in[16:20])
	sizeof := binary.LittleEndian.Uint32(in[20:24])
	old := uint64(32) + uint64(sizeof)
	if old > uint64(len(in)) {
		return fmt.Errorf("invalid load commands")
	}
	var first uint64
	off := uint64(32)
	for i := uint32(0); i < ncmd; i++ {
		if off+8 > uint64(len(in)) {
			return fmt.Errorf("invalid load command")
		}
		cmdsz := uint64(binary.LittleEndian.Uint32(in[off+4:]))
		if cmdsz < 8 || off+cmdsz > uint64(len(in)) {
			return fmt.Errorf("invalid load command size")
		}
		if binary.LittleEndian.Uint32(in[off:]) == 0x19 && cmdsz >= 48 && first == 0 {
			first = binary.LittleEndian.Uint64(in[off+40:])
		}
		off += cmdsz
	}
	if first == 0 || first < old {
		return fmt.Errorf("no safe load-command padding")
	}
	n := uint64(len(name) + 1)
	cmdsz := (24 + n + 7) &^ 7
	if first-old < cmdsz {
		return fmt.Errorf("no Mach-O load-command padding")
	}
	buf := append([]byte(nil), in...)
	for j := old; j < old+cmdsz; j++ {
		buf[j] = 0
	}
	binary.LittleEndian.PutUint32(buf[old:], 0xc)
	binary.LittleEndian.PutUint32(buf[old+4:], uint32(cmdsz))
	binary.LittleEndian.PutUint32(buf[old+8:], 24)
	binary.LittleEndian.PutUint32(buf[old+12:], 2)
	binary.LittleEndian.PutUint32(buf[old+16:], 0x10000)
	binary.LittleEndian.PutUint32(buf[old+20:], 0x10000)
	copy(buf[old+24:], name)
	binary.LittleEndian.PutUint32(buf[16:], ncmd+1)
	binary.LittleEndian.PutUint32(buf[20:], sizeof+uint32(cmdsz))
	return os.WriteFile(out, buf, 0755)
}
