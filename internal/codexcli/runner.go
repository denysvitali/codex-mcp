package codexcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

type Runner struct {
	cfg       RunnerConfig
	logger    *logrus.Logger
	semaphore chan struct{}
}

type RunnerConfig struct {
	CodexBin          string
	Root              string
	AllowDirs         []string
	DefaultYolo       bool
	DefaultModel      string
	DefaultSandbox    string
	MaxConcurrentRuns int
}

type RunRequest struct {
	Prompt           string
	Cwd              string
	ThreadID         string
	Model            string
	Profile          string
	Sandbox          string
	TimeoutMS        int
	SkipGitRepoCheck *bool
	Async            bool
}

type RunResult struct {
	ThreadID      string `json:"thread_id"`
	FinalMessage  string `json:"final_message"`
	Usage         Usage  `json:"usage"`
	ElapsedMS     int64  `json:"elapsed_ms"`
	ExitCode      int    `json:"exit_code"`
	Mode          string `json:"mode"`
	RawEventCount int    `json:"raw_event_count"`
	StderrTail    string `json:"stderr_tail,omitempty"`
}

type Usage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

type parserState struct {
	threadID     string
	finalMessage string
	usage        Usage
	eventCount   int
}

type eventEnvelope struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Item     json.RawMessage `json:"item"`
	Usage    *usageEnvelope  `json:"usage"`
}

type usageEnvelope struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

type itemEnvelope struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func NewRunner(cfg RunnerConfig, logger *logrus.Logger) *Runner {
	return &Runner{
		cfg:       cfg,
		logger:    logger,
		semaphore: make(chan struct{}, cfg.MaxConcurrentRuns),
	}
}

func (r *Runner) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return RunResult{}, errors.New("prompt is required")
	}
	if req.TimeoutMS < 0 {
		return RunResult{}, fmt.Errorf("timeout_ms must be non-negative: %d", req.TimeoutMS)
	}

	cwd, err := r.resolveCwd(req.Cwd)
	if err != nil {
		return RunResult{}, err
	}

	r.logger.WithFields(logrus.Fields{
		"cwd":       cwd,
		"thread_id": req.ThreadID,
		"async":     req.Async,
	}).Info("starting codex run")

	select {
	case r.semaphore <- struct{}{}:
	case <-ctx.Done():
		return RunResult{}, ctx.Err()
	}
	defer func() { <-r.semaphore }()

	args, yolo, err := r.buildArgs(ctx, cwd, req)
	if err != nil {
		return RunResult{}, err
	}

	runCtx := ctx
	cancel := func() {}
	if req.TimeoutMS > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, r.cfg.CodexBin, args...)
	cmd.Dir = cwd
	setProcessGroup(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return RunResult{}, fmt.Errorf("start codex: %w", err)
	}
	go func() {
		<-runCtx.Done()
		killProcessGroup(cmd)
	}()

	start := time.Now()
	stderrCh := make(chan string, 1)
	go func() {
		stderrCh <- readTail(stderr, 8192)
	}()

	state, parseErr := parseJSONL(stdout)
	waitErr := cmd.Wait()
	elapsedMS := time.Since(start).Milliseconds()
	stderrTail := <-stderrCh
	exitCode := exitCodeFromError(waitErr)

	if runCtx.Err() != nil {
		killProcessGroup(cmd)
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return RunResult{}, &TimeoutError{
				DurationMS: req.TimeoutMS,
				StderrTail: stderrTail,
				ThreadID:   state.threadID,
			}
		}
		return RunResult{}, ctx.Err()
	}

	if parseErr != nil {
		return RunResult{}, fmt.Errorf("parse codex output: %w", parseErr)
	}
	if waitErr != nil {
		r.logger.WithFields(logrus.Fields{
			"cwd":         cwd,
			"thread_id":   state.threadID,
			"exit_code":   exitCode,
			"stderr_tail": stderrTail,
		}).Warn("codex run failed")
		return RunResult{}, &RunError{
			Err:        waitErr,
			ExitCode:   exitCode,
			StderrTail: stderrTail,
			ThreadID:   state.threadID,
		}
	}
	if state.finalMessage == "" {
		return RunResult{}, fmt.Errorf("codex returned no final agent message")
	}

	mode := "sync"
	if req.Async {
		mode = "task"
	}

	r.logger.WithFields(logrus.Fields{
		"cwd":        cwd,
		"thread_id":  state.threadID,
		"elapsed_ms": elapsedMS,
		"exit_code":  exitCode,
		"raw_events": state.eventCount,
		"mode":       mode,
		"yolo":       yolo,
	}).Info("codex run completed")

	return RunResult{
		ThreadID:      state.threadID,
		FinalMessage:  state.finalMessage,
		Usage:         state.usage,
		ElapsedMS:     elapsedMS,
		ExitCode:      exitCode,
		Mode:          mode,
		RawEventCount: state.eventCount,
		StderrTail:    "",
	}, nil
}

type TimeoutError struct {
	DurationMS int
	StderrTail string
	ThreadID   string
}

func (e *TimeoutError) Error() string {
	if e.StderrTail == "" {
		return fmt.Sprintf("codex run timed out after %dms", e.DurationMS)
	}
	return fmt.Sprintf("codex run timed out after %dms: %s", e.DurationMS, e.StderrTail)
}

type RunError struct {
	Err        error
	ExitCode   int
	StderrTail string
	ThreadID   string
}

func (e *RunError) Error() string {
	if e.StderrTail == "" {
		return fmt.Sprintf("codex exited with code %d: %v", e.ExitCode, e.Err)
	}
	return fmt.Sprintf("codex exited with code %d: %v: %s", e.ExitCode, e.Err, e.StderrTail)
}

func (e *RunError) Unwrap() error {
	return e.Err
}

func (r *Runner) buildArgs(ctx context.Context, cwd string, req RunRequest) ([]string, bool, error) {
	yolo := r.cfg.DefaultYolo

	args := []string{"exec"}
	if req.ThreadID != "" {
		args = append(args, "resume", req.ThreadID)
	}
	args = append(args, "--json", "--color", "never")

	model := req.Model
	if model == "" {
		model = r.cfg.DefaultModel
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if req.Profile != "" {
		args = append(args, "--profile", req.Profile)
	}

	if yolo {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	} else {
		sandbox := req.Sandbox
		if sandbox == "" {
			sandbox = r.cfg.DefaultSandbox
		}
		if sandbox != "" {
			args = append(args, "--sandbox", sandbox)
		}
	}

	skipGitRepoCheck, err := r.resolveSkipGitRepoCheck(ctx, cwd, req.SkipGitRepoCheck)
	if err != nil {
		return nil, false, err
	}
	if skipGitRepoCheck {
		args = append(args, "--skip-git-repo-check")
	}

	args = append(args, req.Prompt)
	return args, yolo, nil
}

func (r *Runner) resolveCwd(requested string) (string, error) {
	root, err := evalDir(r.cfg.Root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	cwd := root

	allowDirs := make([]string, 0, len(r.cfg.AllowDirs))
	for _, dir := range r.cfg.AllowDirs {
		resolved, err := evalDir(dir)
		if err != nil {
			return "", fmt.Errorf("resolve allowed dir %q: %w", dir, err)
		}
		allowDirs = append(allowDirs, resolved)
	}

	if requested != "" {
		candidate := requested
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, candidate)
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
		cwd, err = evalDir(abs)
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
	}

	if !isAllowedPath(cwd, root, allowDirs) {
		return "", fmt.Errorf("cwd %q is outside the allowed roots", cwd)
	}

	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("stat cwd: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd is not a directory: %s", cwd)
	}
	return cwd, nil
}

func evalDir(path string) (string, error) {
	clean := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("eval symlinks for %q: %w", clean, err)
	}
	return resolved, nil
}

func (r *Runner) resolveSkipGitRepoCheck(ctx context.Context, cwd string, requested *bool) (bool, error) {
	if requested != nil {
		return *requested, nil
	}
	ok, err := isGitRepo(ctx, cwd)
	if err != nil {
		if errors.Is(err, errGitNotFound) {
			return false, err
		}
		r.logger.WithError(err).WithField("cwd", cwd).Warn("git repo detection failed; enabling skip-git-repo-check")
		return true, nil
	}
	return !ok, nil
}

func parseJSONL(reader io.Reader) (parserState, error) {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var state parserState
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}

		state.eventCount++
		var event eventEnvelope
		if err := json.Unmarshal(line, &event); err != nil {
			return parserState{}, fmt.Errorf("decode event %d: %w", state.eventCount, err)
		}

		switch event.Type {
		case "thread.started":
			if event.ThreadID != "" {
				state.threadID = event.ThreadID
			}
		case "item.completed":
			var item itemEnvelope
			if len(event.Item) == 0 {
				continue
			}
			if err := json.Unmarshal(event.Item, &item); err != nil {
				return parserState{}, fmt.Errorf("decode item.completed: %w", err)
			}
			if item.Type == "agent_message" && item.Text != "" {
				state.finalMessage = item.Text
			}
		case "turn.completed":
			if event.Usage != nil {
				state.usage = Usage{
					InputTokens:       event.Usage.InputTokens,
					CachedInputTokens: event.Usage.CachedInputTokens,
					OutputTokens:      event.Usage.OutputTokens,
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return parserState{}, err
	}
	return state, nil
}

func readTail(reader io.Reader, limit int) string {
	if limit <= 0 {
		return ""
	}

	buf := make([]byte, 0, limit)
	chunk := make([]byte, 4096)

	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			if n >= limit {
				buf = append(buf[:0], chunk[n-limit:n]...)
			} else {
				needed := len(buf) + n - limit
				if needed > 0 {
					buf = append(buf[:0], buf[needed:]...)
				}
				buf = append(buf, chunk[:n]...)
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return ""
		}
	}

	return strings.TrimSpace(string(buf))
}

func isAllowedPath(path string, root string, allowDirs []string) bool {
	if hasPathPrefix(path, root) {
		return true
	}
	for _, dir := range allowDirs {
		if hasPathPrefix(path, dir) {
			return true
		}
	}
	return false
}

func hasPathPrefix(path string, prefix string) bool {
	if path == prefix {
		return true
	}
	rel, err := filepath.Rel(prefix, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func setProcessGroup(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || runtime.GOOS == "windows" {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
