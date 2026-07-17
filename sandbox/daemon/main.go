package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/gkgoat1/scripts/commitment"
	"github.com/gkgoat1/scripts/commitment/anchor"
	"github.com/gkgoat1/scripts/internal/proxypass"
	interposeconfig "github.com/gkgoat1/scripts/interpose/config"
	"github.com/gkgoat1/scripts/interpose/core"
	"github.com/gkgoat1/scripts/interpose/policy/tcc"
	"github.com/gkgoat1/scripts/interpose/wrappers"
	sandboxconfig "github.com/gkgoat1/scripts/sandbox/config"
	"github.com/gkgoat1/scripts/sandbox/hashmap"
	"github.com/gkgoat1/scripts/sandbox/protocol"
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
	path, hash        string // hash is the complete canonical hash-map digest
	files             hashmap.Map
}
type server struct {
	socket, shim, identity, keychain string
	cacheDir                         string
	policy                           sandboxconfig.Config
	policyActive, policyTrusted      bool
	allowGetTaskAllow                bool
	envVars                          map[string]map[string]bool
	hashUpdaters                     map[string]map[string]bool
	cacheSources                     map[string]string
	procs                            map[int]process
	mu                               sync.Mutex

	// protectedRoots is computed once at startup by verifyPolicy: the fixed
	// built-in TCC roots, plus config-supplied ExtraProtectedPaths only if
	// they verified against the commitment anchor. See policy_verify.go.
	protectedRoots []string
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
		case "--codesign-identity":
			if i+1 < len(os.Args) {
				s.identity = os.Args[i+1]
				i++
			}
		case "--codesign-keychain":
			if i+1 < len(os.Args) {
				s.keychain = os.Args[i+1]
				i++
			}
		case "--home":
			if i+1 < len(os.Args) {
				layout, err := sandboxconfig.NewLayout(os.Args[i+1])
				if err != nil {
					panic(err)
				}
				s.cacheDir = layout.TransientRoot
				cfg, err := sandboxconfig.Load(layout.ConfigPath)
				if err == nil {
					if cfg.AutoInterpose.Enabled && runtime.GOOS != "darwin" {
						fmt.Fprintln(os.Stderr, "sandbox: autoInterpose is not supported on Linux; see sandbox/linux-known-gaps.md")
						os.Exit(2)
					}
					s.policy = cfg
					proofData, readErr := os.ReadFile(layout.ProofPath)
					var proof commitment.ProofFile
					var proofErr error
					if readErr != nil {
						proofErr = readErr
					} else {
						proof, proofErr = commitment.DecodeProofFile(proofData)
					}
					reader := anchor.PlistAnchorReader{Converter: anchor.NewRealPlistToJSON(), Path: layout.AnchorPath}
					root, anchorErr := reader.ReadRoot()
					switch {
					case errors.Is(anchorErr, anchor.ErrAnchorNotInstalled):
						// An isolated logical home is explicitly allowed to run its
						// local config for tests, but never shares the committed cache.
						s.policyActive = true
					case anchorErr == nil && proofErr == nil:
						if p, ok := proof.Entries[sandboxconfig.PolicyLeafID]; ok && commitment.VerifyProof(cfg.CommitLeaf(), p, root) {
							s.policyActive, s.policyTrusted, s.cacheDir = true, true, layout.CacheDir
						}
					}
				}
				i++
			}
		case "--allow-get-task-allow":
			s.allowGetTaskAllow = true
		}
	}

	defaultRoots, drErr := tcc.DefaultProtectedRoots()
	if drErr != nil {
		defaultRoots = nil // extremely unlikely (os.UserHomeDir failing); degrade rather than panic
	}
	proofData, proofReadErr := os.ReadFile(interposeconfig.DefaultConfigPath() + ".proof")
	var proofFile commitment.ProofFile
	proofErr := proofReadErr
	if proofReadErr == nil {
		proofFile, proofErr = commitment.DecodeProofFile(proofData)
	}
	extra, _, warnMsg := verifyPolicy(interposeconfig.Load(),
		anchor.PlistAnchorReader{Converter: anchor.NewRealPlistToJSON()}, proofFile, proofErr)
	if warnMsg != "" {
		fmt.Fprintf(os.Stderr, "[warn] sandboxd: %s\n", warnMsg)
	}
	s.protectedRoots = append(append([]string{}, defaultRoots...), extra...)

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

func sendFD(uc *net.UnixConn, status string, fd int) error {
	var oob []byte
	if fd >= 0 {
		oob = syscall.UnixRights(fd)
	}
	_, _, err := uc.WriteMsgUnix([]byte(status+"\n"), oob, nil)
	return err
}
func (s *server) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	magic, err := r.Peek(len(protocol.Magic))
	if err == nil && string(magic) == protocol.Magic {
		_, _ = r.Discard(len(protocol.Magic))
		s.handleProtocol(c, r)
		return
	}
	for {
		line, e := r.ReadString('\n')
		if e != nil {
			return
		}
		s.command(c, strings.TrimSpace(line))
	}
}

type remoteGuestOperations struct {
	server *server
	conn   net.Conn
	reader *bufio.Reader
	nextID uint64
}

func (o *remoteGuestOperations) Stderr() io.Writer { return io.Discard }
func (o *remoteGuestOperations) ReadFile(ctx context.Context, path string) ([]byte, error) {
	result, err := o.operation(ctx, protocol.Operation{Kind: protocol.OpRead, Path: path})
	if err != nil {
		return nil, err
	}
	return result.Data, nil
}
func (o *remoteGuestOperations) ConfirmPIN(ctx context.Context, prompt string) error {
	_, err := o.operation(ctx, protocol.Operation{Kind: protocol.OpConfirm, Prompt: prompt})
	return err
}
func (o *remoteGuestOperations) Run(ctx context.Context, command core.Command) (core.Result, error) {
	if !filepath.IsAbs(command.Path) {
		return core.Result{}, fmt.Errorf("approved command has a non-absolute path")
	}
	// An approved helper must itself be rewritten so its new process image loads
	// the shim. Spawning the host system binary directly here would put Git's
	// snapshot/restore work outside the guest sandbox.
	guestPath, err := o.server.rewrite(command.Path)
	if err != nil {
		return core.Result{}, fmt.Errorf("stage approved guest command: %w", err)
	}
	var capture bool
	if command.Stdout != nil {
		capture = true
	}
	result, err := o.operation(ctx, protocol.Operation{Kind: protocol.OpRun, Path: guestPath, Args: append([]string{filepath.Base(command.Path)}, command.Args...), Dir: command.Dir, Env: command.Env, Capture: capture})
	if err != nil {
		return core.Result{}, err
	}
	if capture {
		_, _ = command.Stdout.Write(result.Data)
	}
	return core.Result{ExitCode: int(result.ExitCode)}, nil
}
func (o *remoteGuestOperations) operation(ctx context.Context, op protocol.Operation) (protocol.OperationResult, error) {
	if err := ctx.Err(); err != nil {
		return protocol.OperationResult{}, err
	}
	o.nextID++
	op.ID = o.nextID
	payload, err := protocol.EncodeOperation(op)
	if err != nil {
		return protocol.OperationResult{}, err
	}
	if err := protocol.WriteFrame(o.conn, protocol.TypeOpReq, payload); err != nil {
		return protocol.OperationResult{}, err
	}
	typ, response, err := protocol.ReadFrame(o.reader)
	if err != nil {
		return protocol.OperationResult{}, err
	}
	if typ != protocol.TypeOpRes {
		return protocol.OperationResult{}, fmt.Errorf("unexpected guest operation response")
	}
	result, err := protocol.DecodeOperationResult(response)
	if err != nil {
		return protocol.OperationResult{}, err
	}
	if result.ID != op.ID || !result.OK {
		if result.Message == "" {
			result.Message = "guest operation denied"
		}
		return protocol.OperationResult{}, errors.New(result.Message)
	}
	return result, nil
}

// handleProtocol serves one bounded exec transaction. It is deliberately
// separate from the legacy text protocol so argv/environment cannot be split
// by whitespace. The dylib implementation will send the magic before its first
// EXEC request and service typed operation requests until a final decision.
func (s *server) handleProtocol(c net.Conn, r *bufio.Reader) {
	if err := protocol.WriteMagic(c); err != nil {
		return
	}
	typ, payload, err := protocol.ReadFrame(r)
	if err != nil || typ != protocol.TypeExecReq {
		return
	}
	req, err := protocol.DecodeExecRequest(payload)
	if err != nil {
		s.writeExecResult(c, protocol.ExecResult{Message: "invalid exec request"})
		return
	}
	result := s.evaluateExec(c, r, req)
	s.writeExecResult(c, result)
}
func (s *server) writeExecResult(c net.Conn, result protocol.ExecResult) {
	payload, err := protocol.EncodeExecResult(result)
	if err == nil {
		_ = protocol.WriteFrame(c, protocol.TypeExecRes, payload)
	}
}
func (s *server) evaluateExec(c net.Conn, r *bufio.Reader, req protocol.ExecRequest) protocol.ExecResult {
	pid, err := peerPID(c)
	if err != nil {
		return protocol.ExecResult{Message: "cannot identify guest peer"}
	}
	s.mu.Lock()
	_, registered := s.procs[pid]
	s.mu.Unlock()
	if !registered {
		return protocol.ExecResult{Message: "guest peer is not registered"}
	}
	if !s.policyTrusted || !s.policy.AutoInterpose.Enabled {
		return protocol.ExecResult{Message: "auto-interposition policy is not committed"}
	}
	if !filepath.IsAbs(req.Path) || !filepath.IsAbs(req.Dir) || len(req.Argv) == 0 {
		return protocol.ExecResult{Message: "invalid exec path, directory, or argv"}
	}
	path, err := filepath.EvalSymlinks(req.Path)
	if err != nil {
		return protocol.ExecResult{Message: "cannot resolve exec path"}
	}
	name, wrapper := s.autoWrapper(path)
	if wrapper == nil {
		return protocol.ExecResult{Allowed: true, Path: path, Argv: req.Argv, Env: req.Env}
	}
	view := core.PolicyView{ExtraProtectedPaths: append([]string(nil), s.policy.AutoInterpose.Policy.ExtraProtectedPaths...), DisableSnapshot: append([]string(nil), s.policy.AutoInterpose.Policy.DisableSnapshot...), SnapshotPrefix: "interpose/snapshot", CommandAllowlist: s.policy.AutoInterpose.Policy.CommandAllowlist}
	ops := &remoteGuestOperations{server: s, conn: c, reader: r}
	ctx := &core.Context{Name: name, Args: append([]string(nil), req.Argv[1:]...), RealBinary: path, Dir: req.Dir, Env: append([]string(nil), req.Env...), Ops: ops, Policy: view}
	args, err := wrapper.Transform(ctx, ctx.Args)
	if err == nil {
		ctx.Args = args
		err = wrapper.Before(ctx)
	}
	if err != nil {
		return protocol.ExecResult{Message: "interposition denied: " + err.Error()}
	}
	return protocol.ExecResult{Allowed: true, Path: path, Argv: append([]string{req.Argv[0]}, ctx.Args...), Env: req.Env}
}
func (s *server) autoWrapper(path string) (string, core.Wrapper) {
	name := filepath.Base(path)
	if filepath.Dir(path) != "/usr/bin" && filepath.Dir(path) != "/bin" {
		return "", nil
	}
	enabled := false
	for _, configured := range s.policy.AutoInterpose.Commands {
		if configured == name {
			enabled = true
			break
		}
	}
	if !enabled {
		return "", nil
	}
	switch name {
	case "git":
		return name, wrappers.Git{}
	case "find":
		return name, wrappers.Find{}
	case "grep":
		return name, wrappers.Grep{}
	case "kill", "pkill", "killall", "osascript":
		return name, wrappers.ProtectedCommand{CommandName: name}
	}
	return "", nil
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
		pid, err := peerPID(c)
		if err != nil {
			fmt.Fprintln(c, "ERR peerpid")
			return
		}
		parent, _ := strconv.Atoi(f[2])
		s.register(pid, parent, f[3])
		fmt.Fprintln(c, "OK")
	case "FORK": // FORK child parent path
		if len(f) < 4 {
			fmt.Fprintln(c, "ERR fork")
			return
		}
		pid, err := peerPID(c)
		if err != nil {
			fmt.Fprintln(c, "ERR peerpid")
			return
		}
		parent, _ := strconv.Atoi(f[2])
		s.register(pid, parent, f[3])
		fmt.Fprintln(c, "OK")
	case "ENV": // ENV pid path variable
		if len(f) < 4 {
			fmt.Fprintln(c, "DENY")
			return
		}
		pid, err := peerPID(c)
		if err != nil {
			fmt.Fprintln(c, "DENY")
			return
		}
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
		pid, err := peerPID(c)
		if err != nil {
			fmt.Fprintln(c, policyDenied)
			return
		}
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
	case "CONNECT": // CONNECT pid host port
		if len(f) < 4 {
			fmt.Fprintln(c, "DENIED missing host/port")
			return
		}
		uc, ok := c.(*net.UnixConn)
		if !ok {
			fmt.Fprintln(c, "DENIED not unix")
			return
		}
		host := f[2]
		port := f[3]
		target := net.JoinHostPort(host, port)
		ctx, cancel := context.WithTimeout(context.Background(), 2*proxypass.DefaultDialTimeout)
		defer cancel()
		conn, err := proxypass.DialHost(ctx, target)
		if err != nil {
			fmt.Fprintf(c, "DENIED %s\n", err)
			return
		}
		tcpConn, ok := conn.(*net.TCPConn)
		if !ok {
			conn.Close()
			fmt.Fprintln(c, "DENIED non-tcp")
			return
		}
		file, err := tcpConn.File()
		if err != nil {
			conn.Close()
			fmt.Fprintf(c, "DENIED %s\n", err)
			return
		}
		fd := int(file.Fd())
		if err := sendFD(uc, "OK", fd); err != nil {
			file.Close()
			conn.Close()
			fmt.Fprintf(c, "DENIED %s\n", err)
			return
		}
		file.Close()
		conn.Close()
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
		pid, err := peerPID(c)
		if err != nil {
			fmt.Fprintln(c, "ERR peerpid")
			return
		}
		s.mu.Lock()
		if p, ok := s.procs[pid]; ok {
			killGroup(p)
			delete(s.procs, pid)
		}
		s.mu.Unlock()
		fmt.Fprintln(c, "OK")
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
	m := hashmap.Map{Version: hashmap.Version, Files: map[string]string{}}
	m, err := m.AddPath(path)
	if err != nil {
		return
	}
	digest, err := m.Digest()
	if err != nil {
		return
	}
	canonical, _ := hashmap.CanonicalPath(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	if source, ok := s.cacheSources[canonical]; ok {
		canonical = source
	}
	s.procs[pid] = process{pid: pid, parent: parent, pgid: pid, path: canonical, hash: digest, files: m}
}
func fileHash(path string) (string, error) { return hashmap.FileHash(path) }
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
	p, ok := s.procs[pid]
	if !ok {
		return false
	}
	if s.policyActive {
		return s.policy.EnvAllowed(varName, p.hash)
	}
	return false
}
func (s *server) updateHash(pid int, path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[pid]
	if !ok {
		return false
	}
	candidate, err := p.files.AddPath(path)
	if err != nil {
		return false
	}
	digest, err := candidate.Digest()
	if err != nil {
		return false
	}
	if !s.policyActive || !s.policy.AllowsUpdate(p.hash, filepath.Ext(path), digest) {
		return false
	}
	canonical, _ := hashmap.CanonicalPath(path)
	p.hash, p.path, p.files = digest, canonical, candidate
	s.procs[pid] = p
	return true
}

// isProtected reports whether norm is or is under one of s.protectedRoots
// (the verified-or-fallback root set computed once at startup — see
// policy_verify.go — rather than the live, unverified global config).
func (s *server) isProtected(norm string) bool {
	return tcc.MatchesRoots(norm, s.protectedRoots)
}

func (s *server) pathPolicy(path string) string {
	norm, err := tcc.NormalizePath(path)
	if err != nil {
		return policyDenied
	}
	if s.isProtected(norm) {
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
	h.Write([]byte("sandbox-rewrite-v2"))
	h.Write([]byte(s.identity))
	h.Write([]byte(fmt.Sprintf("%t%t%t", orig.jit, orig.unsigned, orig.task && s.allowGetTaskAllow)))
	name := hex.EncodeToString(h.Sum(nil))
	cache := s.cacheDir
	if cache == "" {
		cache, e = os.UserCacheDir()
		if e != nil {
			return "", e
		}
		cache = filepath.Join(cache, "sandbox")
	}
	dir := filepath.Join(cache, "binaries")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	entryDir := filepath.Join(dir, name)
	out := filepath.Join(entryDir, "program")
	if st, x := os.Stat(out); x == nil && st.Mode()&0111 != 0 && signedArtifact(out) {
		s.mu.Lock()
		s.cacheSources[out] = src
		s.mu.Unlock()
		return out, nil
	}
	if e = os.MkdirAll(entryDir, 0700); e != nil {
		return "", e
	}
	out = filepath.Join(entryDir, "program")
	shimOut := filepath.Join(entryDir, "x")
	if e = copyFile(s.shim, shimOut); e != nil {
		return "", fmt.Errorf("stage signed sandbox dylib: %w", e)
	}
	_ = os.Chmod(shimOut, 0755)
	if e = rewriteMachO(in, out, "@executable_path/x"); e != nil {
		return "", e
	}
	_ = os.Chmod(out, 0755)
	if _, x := os.Stat("/usr/bin/codesign"); x == nil {
		team, identifier, cdhash, err := signingInfo(shimOut)
		if err != nil {
			return "", err
		}
		if e = signHardened(out, dir, orig, s.allowGetTaskAllow, s.identity, s.keychain, team, identifier, cdhash); e != nil {
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
func signHardened(path, dir string, o originalEntitlements, allow bool, identity, keychain, team, libraryIdentifier, libraryCDHash string) error {
	if identity == "" {
		return fmt.Errorf("sandbox requires SANDBOX_CODESIGN_IDENTITY or SANDBOX_ADHOC=1")
	}
	adhoc := identity == "-"
	if adhoc && libraryCDHash == "" {
		return fmt.Errorf("sandbox dylib has no SHA-256 code-directory hash")
	}
	if !adhoc && (team == "" || team == "not set") {
		return fmt.Errorf("sandbox dylib has no TeamIdentifier; sign it with a real identity or select SANDBOX_ADHOC=1")
	}
	p := filepath.Join(dir, "entitlements.plist")
	constraint := filepath.Join(dir, "library-constraint.plist")
	t := o.task && allow
	v := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>com.apple.security.cs.allow-jit</key><%s/><key>com.apple.security.cs.allow-unsigned-executable-memory</key><%s/><key>com.apple.security.get-task-allow</key><%s/>`, boolTag(o.jit), boolTag(o.unsigned), boolTag(t))
	var c string
	if adhoc {
		decoded, err := hex.DecodeString(libraryCDHash)
		if err != nil || len(decoded) != 20 {
			return fmt.Errorf("invalid sandbox dylib SHA-256 code-directory hash")
		}
		v += `<key>com.apple.security.cs.disable-library-validation</key><true/>`
		c = fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>cdhash</key><data>%s</data></dict></plist>`, base64.StdEncoding.EncodeToString(decoded))
	} else {
		c = fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>team-identifier</key><string>%s</string><key>signing-identifier</key><string>%s</string></dict></plist>`, xmlEscape(team), xmlEscape(libraryIdentifier))
	}
	v += `</dict></plist>`
	if e := os.WriteFile(p, []byte(v), 0600); e != nil {
		return e
	}
	if e := os.WriteFile(constraint, []byte(c), 0600); e != nil {
		return e
	}
	args := []string{"--force", "--sign", identity, "--timestamp=none", "--options", "runtime", "--entitlements", p, "--library-constraint", constraint, path}
	if keychain != "" {
		args = append([]string{"--keychain", keychain}, args...)
	}
	if e := exec.Command("codesign", args...).Run(); e != nil {
		return fmt.Errorf("hardened codesign with library constraint: %w", e)
	}
	return nil
}

func signedArtifact(path string) bool {
	return exec.Command("codesign", "--verify", "--strict", path).Run() == nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0755)
}

func signingInfo(path string) (team, identifier, cdhash string, err error) {
	out, err := exec.Command("codesign", "-dv", "--verbose=4", path).CombinedOutput()
	if err != nil {
		return "", "", "", fmt.Errorf("inspect sandbox dylib signature: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "TeamIdentifier="):
			team = strings.TrimSpace(strings.TrimPrefix(line, "TeamIdentifier="))
		case strings.HasPrefix(line, "Identifier="):
			identifier = strings.TrimSpace(strings.TrimPrefix(line, "Identifier="))
		case strings.HasPrefix(line, "CandidateCDHash sha256="):
			cdhash = strings.TrimSpace(strings.TrimPrefix(line, "CandidateCDHash sha256="))
		}
	}
	if identifier == "" {
		return "", "", "", fmt.Errorf("sandbox dylib has no signing identifier")
	}
	if _, err := hex.DecodeString(cdhash); err != nil || len(cdhash) != 40 {
		return "", "", "", fmt.Errorf("sandbox dylib has no usable SHA-256 code-directory hash")
	}
	return team, identifier, cdhash, nil
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;").Replace(s)
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
	// Prefer replacing an optional command. If the replacement is larger, move
	// only the load-command table into the existing zero padding before code;
	// file offsets and the code-signature blob remain unchanged.
	var anchorOff, anchorSize uint64
	var anchorCount uint32
	off := uint64(32)
	for i := uint32(0); i < ncmd; i++ {
		if off+8 > uint64(len(in)) {
			return fmt.Errorf("invalid load command")
		}
		sz := uint64(binary.LittleEndian.Uint32(in[off+4:]))
		if sz < 8 || off+sz > uint64(len(in)) {
			return fmt.Errorf("invalid load command size")
		}
		cmd := binary.LittleEndian.Uint32(in[off:])
		if cmd == 0x29 && sz == 16 && i+1 < ncmd { // LC_DYLIB_CODE_SIGN_DRS + source
			next := off + sz
			nextSize := uint64(binary.LittleEndian.Uint32(in[next+4:]))
			if binary.LittleEndian.Uint32(in[next:]) == 0x1d && nextSize == 16 {
				anchorOff, anchorSize, anchorCount = off, 32, 2
			}
		}
		if cmd == 0x1d && off+sz == old && anchorOff == 0 { // source only
			anchorOff, anchorSize, anchorCount = off, sz, 1
		}
		off += sz
	}
	if anchorOff == 0 {
		return fmt.Errorf("no replaceable load command for sandbox dylib")
	}
	n := uint64(len(name) + 1)
	cs := (24 + n + 7) &^ 7
	delta := int64(cs) - int64(anchorSize)
	if delta > 0 {
		if old+uint64(delta) > uint64(len(in)) {
			return fmt.Errorf("no Mach-O load-command padding for sandbox dylib")
		}
		for j := old; j < old+uint64(delta); j++ {
			if in[j] != 0 {
				return fmt.Errorf("no replaceable load-command padding")
			}
		}
	}
	b := append([]byte(nil), in...)
	if delta > 0 {
		// Move commands after the replaced command right, backwards to avoid
		// overwriting them. The command-table end moves by delta.
		for j := int64(old) - 1; j >= int64(anchorOff+anchorSize); j-- {
			b[j+delta] = b[j]
		}
	} else if delta < 0 {
		for j := anchorOff + anchorSize; j < old; j++ {
			b[uint64(int64(j)+delta)] = b[j]
		}
	}
	for j := anchorOff; j < anchorOff+cs; j++ {
		b[j] = 0
	}
	binary.LittleEndian.PutUint32(b[anchorOff:], 0xc)
	binary.LittleEndian.PutUint32(b[anchorOff+4:], uint32(cs))
	binary.LittleEndian.PutUint32(b[anchorOff+8:], 24)
	binary.LittleEndian.PutUint32(b[anchorOff+12:], 2)
	binary.LittleEndian.PutUint32(b[anchorOff+16:], 0x10000)
	binary.LittleEndian.PutUint32(b[anchorOff+20:], 0x10000)
	copy(b[anchorOff+24:], name)
	binary.LittleEndian.PutUint32(b[20:], uint32(int64(sizeof)+delta))
	binary.LittleEndian.PutUint32(b[16:], ncmd-anchorCount+1)
	return os.WriteFile(out, b, 0755)
}
