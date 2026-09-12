//nolint:gocritic // Server and Status are passed by value to match the rest of package mcp.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	DefaultListen     = "127.0.0.1:7701"
	DefaultLogTail    = 80
	healthProbeWait   = 3 * time.Second
	healthProbeEvery  = 100 * time.Millisecond
	healthHTTPTimeout = time.Second
	stopWait          = 3 * time.Second
	stopPoll          = 50 * time.Millisecond
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 2 * time.Second
	dirPerm           = 0o755
	filePerm          = 0o644
	pidFileName       = "mcp-server.pid"
	logFileName       = "mcp-server.log"
)

// Runtime is the managed MCP listener (pidfile + log + HTTP /mcp).
// stdio `lw mcp serve` is a separate IDE-spawned process and is not this.
type Runtime struct {
	HomeDir string
	Listen  string
	Bin     string
}

// Status is the JSON shape for `lw mcp status`.
type Status struct {
	Listen  string `json:"listen"`
	PidFile string `json:"pid_file"`
	LogFile string `json:"log_file"`
	MCPJSON string `json:"mcp_json,omitempty"`
	Detail  string `json:"detail,omitempty"`
	State   string `json:"state"`
	Health  string `json:"health,omitempty"`
	Pid     int    `json:"pid,omitempty"`
	Running bool   `json:"running"`
}

func (r Runtime) listenAddr() string {
	if strings.TrimSpace(r.Listen) == "" {
		return DefaultListen
	}

	return strings.TrimSpace(r.Listen)
}

func (r Runtime) pidPath() string {
	return filepath.Join(r.HomeDir, ".lightwave", "run", pidFileName)
}

func (r Runtime) logPath() string {
	return filepath.Join(r.HomeDir, ".lightwave", "logs", logFileName)
}

func (r Runtime) Status(ctx context.Context) Status {
	st := Status{
		State:   "stopped",
		Listen:  r.listenAddr(),
		PidFile: r.pidPath(),
		LogFile: r.logPath(),
	}

	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, ".mcp.json")
		if _, err := os.Stat(candidate); err == nil {
			st.MCPJSON = candidate
		}
	}

	pid, err := r.readPID()
	if err != nil || pid == 0 {
		st.Detail = "no pidfile"
		return st
	}

	st.Pid = pid
	if !pidAlive(pid) {
		st.Detail = "stale pidfile"
		return st
	}

	st.Running = true
	st.State = "running"
	st.Health = probeHealth(ctx, r.listenAddr())

	return st
}

func (r Runtime) ReadLogs(tail int) (string, error) {
	if tail <= 0 {
		tail = DefaultLogTail
	}

	data, err := os.ReadFile(r.logPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no log file at %s — start the managed listener with `lw mcp start`", r.logPath())
		}

		return "", err
	}

	return lastLines(string(data), tail), nil
}

type StartOpts struct {
	Foreground bool
	DryRun     bool
}

func (r Runtime) Start(ctx context.Context, srv Server, opts StartOpts) (Status, error) {
	st := r.Status(ctx)
	if opts.DryRun {
		st.State = "dry-run"
		st.Detail = fmt.Sprintf("would start managed MCP listener on %s (pidfile %s, log %s)", r.listenAddr(), r.pidPath(), r.logPath())

		return st, nil
	}

	if st.Running {
		st.Detail = "already running"
		return st, nil
	}

	if err := os.MkdirAll(filepath.Dir(r.pidPath()), dirPerm); err != nil {
		return st, err
	}

	if err := os.MkdirAll(filepath.Dir(r.logPath()), dirPerm); err != nil {
		return st, err
	}

	if opts.Foreground {
		if err := os.WriteFile(r.pidPath(), []byte(strconv.Itoa(os.Getpid())+"\n"), filePerm); err != nil {
			return st, err
		}
		defer func() { _ = os.Remove(r.pidPath()) }()

		return st, srv.ListenAndServe(ctx, r.listenAddr())
	}

	bin := r.Bin
	if bin == "" {
		var err error

		bin, err = os.Executable()
		if err != nil {
			return st, err
		}
	}

	logFile, err := os.OpenFile(r.logPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		return st, err
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.CommandContext(context.WithoutCancel(ctx), bin, "mcp", "start", "--foreground", "--listen", r.listenAddr())
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return st, fmt.Errorf("start mcp listener: %w", err)
	}

	if err := os.WriteFile(r.pidPath(), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), filePerm); err != nil {
		_ = cmd.Process.Kill()
		return st, err
	}

	if err := waitHealthy(ctx, r.listenAddr()); err != nil {
		return r.Status(ctx), err
	}

	return r.Status(ctx), nil
}

func (r Runtime) Stop(ctx context.Context, dryRun bool) (Status, error) {
	st := r.Status(ctx)
	if dryRun {
		if !st.Running {
			st.State = "dry-run"
			st.Detail = "would no-op: managed listener is not running"

			return st, nil
		}

		st.State = "dry-run"
		st.Detail = fmt.Sprintf("would send SIGTERM to pid %d", st.Pid)

		return st, nil
	}

	if !st.Running {
		_ = os.Remove(r.pidPath())
		st.State = "stopped"
		st.Detail = "not running"

		return st, nil
	}

	proc, err := os.FindProcess(st.Pid)
	if err != nil {
		return st, err
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return st, fmt.Errorf("signal pid %d: %w", st.Pid, err)
	}

	deadline := time.Now().Add(stopWait)
	for time.Now().Before(deadline) {
		if !pidAlive(st.Pid) {
			break
		}

		time.Sleep(stopPoll)
	}

	_ = os.Remove(r.pidPath())
	out := r.Status(ctx)
	out.Detail = "stopped"

	return out, nil
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	err := syscall.Kill(pid, 0)

	return err == nil
}

func (r Runtime) readPID() (int, error) {
	data, err := os.ReadFile(r.pidPath())
	if err != nil {
		return 0, err
	}

	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}

	return n, nil
}

func probeHealth(ctx context.Context, addr string) string {
	ctx, cancel := context.WithTimeout(ctx, healthHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL(addr), nil)
	if err != nil {
		return "unprobed"
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "unreachable"
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("http %d", resp.StatusCode)
	}

	return "ok"
}

func waitHealthy(ctx context.Context, addr string) error {
	deadline := time.Now().Add(healthProbeWait)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}

		if probeHealth(ctx, addr) == "ok" {
			return nil
		}

		time.Sleep(healthProbeEvery)
	}

	return fmt.Errorf("mcp listener on %s did not become healthy within %s", addr, healthProbeWait)
}

func healthURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}

	return "http://" + addr + "/health"
}

func lastLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}

	lines := strings.Split(s, "\n")
	if n >= len(lines) {
		return strings.Join(lines, "\n") + "\n"
	}

	return strings.Join(lines[len(lines)-n:], "\n") + "\n"
}

// WriteStatus prints a lifecycle status as JSON or a short human report.
func WriteStatus(st Status, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		return enc.Encode(st)
	}

	state := st.State
	if st.Running {
		fmt.Printf("mcp listener: running pid %d on %s (%s)\n", st.Pid, st.Listen, st.Health)
	} else {
		fmt.Printf("mcp listener: %s\n", state)
	}

	if st.Detail != "" {
		fmt.Printf("  detail: %s\n", st.Detail)
	}

	fmt.Printf("  pidfile: %s\n", st.PidFile)
	fmt.Printf("  logfile: %s\n", st.LogFile)

	if st.MCPJSON != "" {
		fmt.Printf("  .mcp.json: %s\n", st.MCPJSON)
	}

	return nil
}

func (s Server) ListenAndServe(ctx context.Context, addr string) error {
	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	return s.serveListener(ctx, ln)
}

func (s Server) serveListener(ctx context.Context, ln net.Listener) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/mcp", s.handleJSONRPC)
	mux.HandleFunc("/", s.handleJSONRPC)

	httpSrv := &http.Server{Handler: mux, ReadHeaderTimeout: readHeaderTimeout}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpSrv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()

		_ = httpSrv.Shutdown(shutCtx)

		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	}
}

type healthResponse struct {
	Server string `json:"server"`
	OK     bool   `json:"ok"`
	PID    int    `json:"pid"`
	Tools  int    `json:"tools"`
}

func (s Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	tier := ResolveTier(s.HomeDir, s.Persona)
	payload := healthResponse{
		OK:     true,
		Server: "lightwave",
		PID:    os.Getpid(),
		Tools:  len(toolsFor(tier)),
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST JSON-RPC to /mcp", http.StatusMethodNotAllowed)
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(rpcResponse{ //nolint:errchkjson // rpc Result is intentionally any
			JSONRPC: jsonRPCVersion,
			Error:   &rpcError{Code: parseErrorCode, Message: "parse error"},
		})

		return
	}

	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := s.handle(r.Context(), ResolveTier(s.HomeDir, s.Persona), &req)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp) //nolint:errchkjson // rpc Result is intentionally any
}
