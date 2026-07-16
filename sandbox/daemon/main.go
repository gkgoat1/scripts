package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
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

	"github.com/gkgoat1/scripts/interpose/policy/tcc"
)

const (
	policyAllowed = "ALLOWED"
	policyUpdated = "UPDATED"
	policyRO      = "RO"
	policyDenied  = "DENIED"
)

var shellConfigs = map[string]bool{
	".zshrc":        true,
	".bashrc":       true,
	".profile":      true,
	".bash_profile": true,
	".zprofile":     true,
	".zlogin":       true,
}

type process struct {
	pid, parent, pgid int
	path, hash        string
}
type server struct {
	socket, shim      string
	allowGetTaskAllow bool
	envVars           map[string]map[string]bool
	hashUpdaters      map[string]map[string]bool
	cacheSources      map[string]string
	procs             map[int]process
	mu                sync.Mutex
}

type originalEntitlements struct{ jit, unsigned, task bool }

func main() {
	if len(os.Args) >= 4 && os.Args[1] == "--client" {
		client(os.Args[2], os.Args[3:])
		return
	}
	s := &server{socket: "/tmp/sandboxd.sock", envVars: map[string]map[string]bool{}, hashUpdaters: map[string]map[string]bool{}, cacheSources: map[string]string{}, procs: map[int]process{}}
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
		case "--allow-get-task-allow":
			s.allowGetTaskAllow = true
		case "--env-allow":
			if i+1 < len(os.Args) {
				s.addEnvPolicy(os.Args[i+1])
				i++
			}
		case "--hash-updater":
			if i+1 < len(os.Args) {
				s.addHashUpdater(os.Args[i+1])
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
		c, e := l.Accept()
		if e == nil {
			go s.handle(c)
		}
	}
}
func client(socket string, args []string) {
	c, e := net.Dial("unix", socket)
	if e != nil {
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
	r := bufio.NewReader(c)
	for {
		line, e := r.ReadString('\n')
		if e != nil {
			return
		}
		s.command(c, strings.TrimSpace(line))
	}
}
func (s *server) command(c net.Conn, line string) {
	if line == "" {
		return
	}
	f := strings.Fields(line)
	cmd := strings.ToUpper(f[0])
	switch cmd {
	case "PING":
		fmt.Fprintln(c, "OK")
	case "REGISTER": // REGISTER pid parent path
		if len(f) < 4 {
			fmt.Fprintln(c, "ERR register")
			return
		}
		pid, _ := strconv.Atoi(f[1])
		parent, _ := strconv.Atoi(f[2])
		s.register(pid, parent, f[3])
		fmt.Fprintln(c, "OK")
	case "FORK": // FORK child parent path
		if len(f) < 4 {
			fmt.Fprintln(c, "ERR fork")
			return
		}
		pid, _ := strconv.Atoi(f[1])
		parent, _ := strconv.Atoi(f[2])
		s.register(pid, parent, f[3])
		fmt.Fprintln(c, "OK")
	case "ENV": // ENV pid path variable
		if len(f) < 4 {
			fmt.Fprintln(c, "DENY")
			return
		}
		pid, _ := strconv.Atoi(f[1])
		if s.envAllowed(pid, f[3]) {
			fmt.Fprintln(c, "ALLOW")
		} else {
			fmt.Fprintln(c, "DENY")
		}
	case "OPEN": // OPEN pid path flags
		if len(f) < 3 {
			fmt.Fprintln(c, policyDenied)
			return
		}
		pid, _ := strconv.Atoi(f[1])
		flags := 0
		if len(f) >= 4 {
			flags, _ = strconv.Atoi(f[3])
		}
		switch s.pathPolicy(f[2]) {
		case policyDenied:
			fmt.Fprintln(c, policyDenied)
			return
		case policyRO:
			if flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_APPEND|syscall.O_CREAT|syscall.O_TRUNC) != 0 {
				fmt.Fprintln(c, policyDenied)
				return
			}
		}
		if s.updateHash(pid, f[2]) {
			fmt.Fprintln(c, policyUpdated)
		} else {
			fmt.Fprintln(c, policyAllowed)
		}
	case "REWRITE":
		if len(f) < 2 {
			fmt.Fprintln(c, "ERR rewrite")
			return
		}
		p, e := s.rewrite(f[1])
		if e != nil {
			fmt.Fprintf(c, "ERR %s\n", e)
		} else {
			fmt.Fprintf(c, "OK %s\n", p)
		}
	case "KILL":
		if len(f) > 1 {
			pid, _ := strconv.Atoi(f[1])
			s.mu.Lock()
			if p, ok := s.procs[pid]; ok {
				killGroup(p)
				delete(s.procs, pid)
			}
			s.mu.Unlock()
			fmt.Fprintln(c, "OK")
		}
	case "KILLALL":
		s.mu.Lock()
		for pid, p := range s.procs {
			killGroup(p)
			delete(s.procs, pid)
		}
		s.mu.Unlock()
		fmt.Fprintln(c, "OK")
	case "LIST":
		s.mu.Lock()
		for _, p := range s.procs {
			fmt.Fprintf(c, "%d %d %s\n", p.pid, p.parent, p.hash)
		}
		s.mu.Unlock()
	}
}
func (s *server) register(pid, parent int, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if source, ok := s.cacheSources[path]; ok {
		path = source
	}
	sum, e := fileHash(path)
	if e != nil {
		return
	}
	s.procs[pid] = process{pid: pid, parent: parent, pgid: pid, path: path, hash: sum}
}
func fileHash(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	x := sha256.Sum256(b)
	return hex.EncodeToString(x[:]), nil
}
func (s *server) addEnvPolicy(spec string) {
	p := strings.SplitN(spec, "=", 2)
	if len(p) != 2 || p[0] == "" {
		panic("--env-allow VARIABLE=SHA256[,SHA256]")
	}
	if s.envVars[p[0]] == nil {
		s.envVars[p[0]] = map[string]bool{}
	}
	for _, h := range strings.Split(p[1], ",") {
		if len(h) != 64 {
			panic("environment allow hash must be SHA-256")
		}
		s.envVars[p[0]][strings.ToLower(h)] = true
	}
}
func (s *server) addHashUpdater(spec string) {
	p := strings.SplitN(spec, "=", 2)
	if len(p) != 2 || p[0] == "" {
		panic("--hash-updater BINARY_SHA256=EXT[,EXT]")
	}
	if s.hashUpdaters[strings.ToLower(p[0])] == nil {
		s.hashUpdaters[strings.ToLower(p[0])] = map[string]bool{}
	}
	for _, x := range strings.Split(p[1], ",") {
		x = strings.ToLower(x)
		if !strings.HasPrefix(x, ".") {
			x = "." + x
		}
		s.hashUpdaters[strings.ToLower(p[0])][x] = true
	}
}
func (s *server) envAllowed(pid int, varName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	allowed := s.envVars[varName]
	if len(allowed) == 0 {
		return true
	}
	p, ok := s.procs[pid]
	return ok && allowed[p.hash]
}
func (s *server) updateHash(pid int, path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[pid]
	if !ok {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	if !s.hashUpdaters[p.hash][ext] {
		return false
	}
	h, e := fileHash(path)
	if e != nil {
		return false
	}
	p.hash = h
	p.path = path
	s.procs[pid] = p
	return true
}

func (s *server) pathPolicy(path string) string {
	norm, err := tcc.NormalizePath(path)
	if err != nil {
		return policyDenied
	}
	if tcc.IsProtected(norm) {
		return policyDenied
	}
	dir := filepath.Dir(norm)
	base := filepath.Base(norm)
	for _, part := range []string{dir, base} {
		name := filepath.Base(part)
		if len(name) > 1 && name[0] == '.' && name != "." && name != ".." {
			if shellConfigs[name] {
				return policyRO
			}
			return policyDenied
		}
	}
	return policyAllowed
}

func (s *server) rewrite(src string) (string, error) {
	if s.shim == "" {
		return "", fmt.Errorf("daemon has no --shim")
	}
	in, e := os.ReadFile(src)
	if e != nil {
		return "", e
	}
	shim, e := os.ReadFile(s.shim)
	if e != nil {
		return "", e
	}
	orig, _ := readOriginalEntitlements(src)
	h := sha256.New()
	h.Write(in)
	h.Write(shim)
	h.Write([]byte(fmt.Sprintf("%t%t%t", orig.jit, orig.unsigned, orig.task && s.allowGetTaskAllow)))
	name := hex.EncodeToString(h.Sum(nil))
	cache, e := os.UserCacheDir()
	if e != nil {
		return "", e
	}
	dir := filepath.Join(cache, "sandbox", "binaries")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	out := filepath.Join(dir, name)
	if st, x := os.Stat(out); x == nil && st.Mode()&0111 != 0 {
		s.mu.Lock()
		s.cacheSources[out] = src
		s.mu.Unlock()
		return out, nil
	}
	if e = rewriteMachO(in, out, s.shim); e != nil {
		return "", e
	}
	_ = os.Chmod(out, 0755)
	if _, x := os.Stat("/usr/bin/codesign"); x == nil {
		if e = signHardened(out, dir, orig, s.allowGetTaskAllow); e != nil {
			return "", e
		}
	}
	s.mu.Lock()
	s.cacheSources[out] = src
	s.mu.Unlock()
	return out, nil
}
func readOriginalEntitlements(path string) (originalEntitlements, error) {
	var r originalEntitlements
	if e := exec.Command("codesign", "--verify", "--strict", path).Run(); e != nil {
		return r, e
	}
	out, e := exec.Command("codesign", "-d", "--entitlements", ":-", path).CombinedOutput()
	if e != nil {
		return r, e
	}
	d := xml.NewDecoder(strings.NewReader(string(out)))
	key := ""
	for {
		t, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return r, e
		}
		if x, ok := t.(xml.StartElement); ok {
			if x.Name.Local == "key" {
				var v string
				if e = d.DecodeElement(&v, &x); e != nil {
					return r, e
				}
				key = v
			}
			if (x.Name.Local == "true" || x.Name.Local == "false") && key != "" {
				v := x.Name.Local == "true"
				switch key {
				case "com.apple.security.cs.allow-jit":
					r.jit = v
				case "com.apple.security.cs.allow-unsigned-executable-memory":
					r.unsigned = v
				case "com.apple.security.get-task-allow":
					r.task = v
				}
				key = ""
			}
		}
	}
	return r, nil
}
func signHardened(path, dir string, o originalEntitlements, allow bool) error {
	p := filepath.Join(dir, "entitlements.plist")
	t := o.task && allow
	v := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>com.apple.security.cs.allow-jit</key><%s/><key>com.apple.security.cs.allow-unsigned-executable-memory</key><%s/><key>com.apple.security.get-task-allow</key><%s/></dict></plist>`, boolTag(o.jit), boolTag(o.unsigned), boolTag(t))
	if e := os.WriteFile(p, []byte(v), 0600); e != nil {
		return e
	}
	if e := exec.Command("codesign", "--force", "--sign", "-", "--timestamp=none", "--options", "runtime", "--entitlements", p, path).Run(); e != nil {
		return fmt.Errorf("hardened codesign: %w", e)
	}
	return nil
}
func boolTag(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
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
		sz := uint64(binary.LittleEndian.Uint32(in[off+4:]))
		if sz < 8 || off+sz > uint64(len(in)) {
			return fmt.Errorf("invalid load command size")
		}
		if binary.LittleEndian.Uint32(in[off:]) == 0x19 && sz >= 48 && first == 0 {
			first = binary.LittleEndian.Uint64(in[off+40:])
		}
		off += sz
	}
	if first == 0 || first < old {
		return fmt.Errorf("no safe load-command padding")
	}
	n := uint64(len(name) + 1)
	cs := (24 + n + 7) &^ 7
	if first-old < cs {
		return fmt.Errorf("no Mach-O load-command padding")
	}
	b := append([]byte(nil), in...)
	for j := old; j < old+cs; j++ {
		b[j] = 0
	}
	binary.LittleEndian.PutUint32(b[old:], 0xc)
	binary.LittleEndian.PutUint32(b[old+4:], uint32(cs))
	binary.LittleEndian.PutUint32(b[old+8:], 24)
	binary.LittleEndian.PutUint32(b[old+12:], 2)
	binary.LittleEndian.PutUint32(b[old+16:], 0x10000)
	binary.LittleEndian.PutUint32(b[old+20:], 0x10000)
	copy(b[old+24:], name)
	binary.LittleEndian.PutUint32(b[16:], ncmd+1)
	binary.LittleEndian.PutUint32(b[20:], sizeof+uint32(cs))
	return os.WriteFile(out, b, 0755)
}
