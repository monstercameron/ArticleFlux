// Command deployhook redeploys a site when its CI goes green on the default branch.
//
// It listens on loopback behind nginx and answers GitHub's webhook. The rule it
// enforces is the whole point:
//
//	a push to main is not a reason to deploy — a PASSING BUILD on main is.
//
// So it ignores `push` entirely and acts only on `workflow_run` with
// action=completed, conclusion=success, and a workflow and branch that match the
// configuration. Gating on the push instead would deploy every red commit and
// make the quality gate decorative, which is the failure mode this exists to
// prevent.
//
// Why a receiver on the box rather than an SSH step in the Actions runner: a
// deploy key in GitHub secrets is a credential that logs into the droplet, and
// anything that can read those secrets gets a shell. The secret here permits one
// thing — "rebuild the thing you were already going to rebuild" — and the
// process holding it cannot even do that itself: it shells out through sudo to
// exactly two allow-listed scripts, as an unprivileged user.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// maxBody caps a webhook payload. GitHub's are a few KB; anything approaching a
// megabyte is either a bug or somebody probing, and reading it costs memory
// before the signature has been checked.
const maxBody = 1 << 20

// deployTimeout bounds a single deploy. The update scripts build a 26 MB wasm
// module on a 2-vCPU droplet, so this is generous — but unbounded would let one
// wedged build hold the lock forever and silently stop all future deploys.
const deployTimeout = 30 * time.Minute

// Target is one deployable site.
type Target struct {
	// Repo is the full name GitHub sends, e.g. "monstercameron/ArticleFlux".
	Repo string `json:"repo"`
	// Workflow is the workflow NAME (the `name:` field, not the filename) whose
	// success authorises a deploy. Empty means any workflow, which is almost
	// never what you want: a docs-only workflow going green would ship code.
	Workflow string `json:"workflow"`
	// Branch is the branch that deploys. Everything else is acknowledged and
	// ignored, so feature branches can run CI freely without touching the box.
	Branch string `json:"branch"`
	// Secret is the shared secret configured on the GitHub webhook. Without it
	// this endpoint would accept a deploy from anybody who learned the URL.
	Secret string `json:"secret"`
	// Command is the deploy script, run through sudo. Absolute path.
	Command string `json:"command"`
	// Args are extra arguments for Command.
	Args []string `json:"args"`
}

// Config is the file at -config.
type Config struct {
	// Addr is the loopback address to listen on. nginx is in front.
	Addr string `json:"addr"`
	// LogDir receives one file per deploy attempt.
	LogDir string `json:"log_dir"`
	// StatusToken is a GitHub token with `repo:status`, used to report the
	// outcome of a deploy back onto the commit it deployed.
	//
	// Optional, and everything works without it — but without it a deploy
	// failure is INVISIBLE from GitHub, and that is the single most expensive
	// property this service has. The webhook answers 202 the moment it accepts
	// the job and the deploy runs afterwards, so a delivery that says
	// "deploying …" is a delivery that says nothing about whether the deploy
	// worked. On 2026-07-29 a portfolio deploy failed three times in a row
	// while every delivery in GitHub's log showed a green 202, and the only
	// record of the truth was a file on the box that nobody had reason to open.
	//
	// With a token, a failed deploy is a red check beside the commit, where
	// somebody is already looking.
	StatusToken string   `json:"status_token"`
	Targets     []Target `json:"targets"`
}

// hookPayload is the subset of GitHub's workflow_run event this needs. Decoding
// a narrow struct rather than a map keeps the matching honest: a field that is
// not here cannot accidentally influence whether a deploy runs.
type hookPayload struct {
	Action      string `json:"action"`
	WorkflowRun struct {
		Name       string `json:"name"`
		Conclusion string `json:"conclusion"`
		Status     string `json:"status"`
		HeadBranch string `json:"head_branch"`
		HeadSHA    string `json:"head_sha"`
		Event      string `json:"event"`
		HTMLURL    string `json:"html_url"`
	} `json:"workflow_run"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// runner serialises deploys per target and remembers the last result.
type runner struct {
	mu      sync.Mutex
	running map[string]bool
	last    map[string]string
	logDir  string
	log     *slog.Logger
	// token reports outcomes back to GitHub. Empty disables reporting; see
	// Config.StatusToken for why that is a worse place to be than it sounds.
	token string
}

func main() {
	configPath := flag.String("config", "/etc/deployhook/config.json", "path to the JSON config")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.LogDir, 0o750); err != nil {
		log.Error("log dir", "err", err)
		os.Exit(1)
	}

	r := &runner{
		running: map[string]bool{},
		last:    map[string]string{},
		logDir:  cfg.LogDir,
		log:     log,
		token:   cfg.StatusToken,
	}
	if cfg.StatusToken == "" {
		log.Warn("no status_token configured: deploy FAILURES will not be visible in GitHub — " +
			"the webhook's 202 says the job was accepted, never that it succeeded")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/hook", func(w http.ResponseWriter, req *http.Request) { handle(w, req, cfg, r, log) })
	// Liveness for the health watchdog, and a human-readable "is this thing on".
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"running": r.running, "last": r.last})
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	log.Info("deployhook up", "addr", cfg.Addr, "targets", len(cfg.Targets))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("listen", "err", err)
		os.Exit(1)
	}
}

// loadConfig reads and validates the config, refusing anything that would make
// the endpoint unsafe rather than starting up and hoping.
func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:9500"
	}
	if cfg.LogDir == "" {
		cfg.LogDir = "/var/log/deployhook"
	}
	if len(cfg.Targets) == 0 {
		return nil, errors.New("no targets configured")
	}
	for i, t := range cfg.Targets {
		switch {
		case t.Repo == "":
			return nil, fmt.Errorf("target %d: repo is required", i)
		// A target with no secret is an open deploy trigger. Refusing to start is
		// the only safe response: starting up with one unauthenticated target
		// among several is a hole nobody would notice in a green log line.
		case t.Secret == "":
			return nil, fmt.Errorf("target %s: secret is required", t.Repo)
		case t.Command == "" || !filepath.IsAbs(t.Command):
			return nil, fmt.Errorf("target %s: command must be an absolute path", t.Repo)
		case t.Branch == "":
			return nil, fmt.Errorf("target %s: branch is required", t.Repo)
		}
	}
	return &cfg, nil
}

// handle authenticates the request, decides whether it authorises a deploy, and
// says why when it does not. The "why" matters: a webhook that silently drops
// events is indistinguishable from one that is not wired up, and GitHub's
// delivery log is where somebody will look first.
func handle(w http.ResponseWriter, req *http.Request, cfg *Config, r *runner, log *slog.Logger) {
	if req.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, maxBody))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}

	event := req.Header.Get("X-GitHub-Event")
	sig := req.Header.Get("X-Hub-Signature-256")

	// The repository is read BEFORE the signature is verified, only to pick which
	// secret to check against — it is not trusted for anything else. An attacker
	// naming a repo they do not control simply fails the HMAC.
	var probe struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	_ = json.Unmarshal(body, &probe)

	target := findTarget(cfg, probe.Repository.FullName)
	if target == nil {
		log.Warn("unknown repository", "repo", probe.Repository.FullName, "event", event)
		http.Error(w, "unknown repository", http.StatusNotFound)
		return
	}
	if !validSignature(body, sig, target.Secret) {
		log.Warn("bad signature", "repo", target.Repo, "event", event)
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}

	// GitHub sends this when the hook is created. Answering it is what turns the
	// green tick on in the repo settings.
	if event == "ping" {
		fmt.Fprintln(w, "pong")
		return
	}
	if event != "workflow_run" {
		// 200, not an error: the hook may legitimately be subscribed to more than
		// this, and a 4xx here shows up as a red delivery that reads like breakage.
		fmt.Fprintf(w, "ignored: event %q is not workflow_run\n", event)
		return
	}

	var p hookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	if reason := shouldSkip(&p, target); reason != "" {
		log.Info("no deploy", "repo", target.Repo, "reason", reason,
			"workflow", p.WorkflowRun.Name, "branch", p.WorkflowRun.HeadBranch,
			"conclusion", p.WorkflowRun.Conclusion)
		fmt.Fprintf(w, "no deploy: %s\n", reason)
		return
	}

	// 202 and return. A build takes minutes and GitHub gives the endpoint ten
	// seconds before it records a failed delivery, so the deploy has to outlive
	// the request that asked for it.
	started := r.start(target, p.WorkflowRun.HeadSHA, p.WorkflowRun.HTMLURL)
	if !started {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprintf(w, "a deploy for %s is already running\n", target.Repo)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "deploying %s at %s\n", target.Repo, short(p.WorkflowRun.HeadSHA))
}

// findTarget returns the configured target for a repository, or nil.
func findTarget(cfg *Config, repo string) *Target {
	for i := range cfg.Targets {
		if strings.EqualFold(cfg.Targets[i].Repo, repo) {
			return &cfg.Targets[i]
		}
	}
	return nil
}

// shouldSkip returns a human reason not to deploy, or "" to proceed. Every
// condition here is a separate way a deploy could be wrong, and each one is
// stated rather than folded into a single boolean, because the reason is what
// gets read in the delivery log six weeks from now.
func shouldSkip(p *hookPayload, t *Target) string {
	if p.Action != "completed" {
		return fmt.Sprintf("run is %q, not completed", p.Action)
	}
	if p.WorkflowRun.Conclusion != "success" {
		// The whole gate, in one line: red, cancelled, timed out and skipped all
		// land here, and a skipped run is NOT a pass.
		return fmt.Sprintf("conclusion is %q, not success", p.WorkflowRun.Conclusion)
	}
	if p.WorkflowRun.HeadBranch != t.Branch {
		return fmt.Sprintf("branch is %q, not %q", p.WorkflowRun.HeadBranch, t.Branch)
	}
	if t.Workflow != "" && !strings.EqualFold(p.WorkflowRun.Name, t.Workflow) {
		return fmt.Sprintf("workflow is %q, not %q", p.WorkflowRun.Name, t.Workflow)
	}
	// A workflow_run fired by a pull_request carries the PR's merge commit, not
	// the branch tip. Deploying that ships code that is not on main.
	if p.WorkflowRun.Event != "push" && p.WorkflowRun.Event != "workflow_dispatch" {
		return fmt.Sprintf("triggering event is %q, not push", p.WorkflowRun.Event)
	}
	return ""
}

// validSignature checks GitHub's HMAC in constant time.
func validSignature(body []byte, header, secret string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	// hmac.Equal, not bytes.Equal: a length-independent comparison is the only
	// one that does not leak the correct prefix through timing.
	return hmac.Equal(want, mac.Sum(nil))
}

// start launches a deploy unless one is already running for that target.
// Returns false when it declined.
func (r *runner) start(t *Target, sha, runURL string) bool {
	r.mu.Lock()
	if r.running[t.Repo] {
		r.mu.Unlock()
		return false
	}
	r.running[t.Repo] = true
	r.mu.Unlock()

	go r.run(t, sha, runURL)
	return true
}

// run executes the deploy command, capturing everything it prints to a file
// named for the moment it started. The transcript is the only account of what
// happened: nobody is watching a terminal when this fires.
func (r *runner) run(t *Target, sha, runURL string) {
	defer func() {
		r.mu.Lock()
		r.running[t.Repo] = false
		r.mu.Unlock()
	}()

	stamp := time.Now().UTC().Format("20060102-150405")
	name := strings.ReplaceAll(t.Repo, "/", "_")
	logPath := filepath.Join(r.logDir, fmt.Sprintf("%s-%s.log", name, stamp))

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		r.log.Error("deploy log", "repo", t.Repo, "err", err)
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "deploy %s\n  commit %s\n  ci run %s\n  started %s\n\n",
		t.Repo, sha, runURL, time.Now().UTC().Format(time.RFC3339))

	// sudo, with the script allow-listed in sudoers. This process runs as an
	// unprivileged user precisely so that a flaw in the HTTP handling above does
	// not become a root shell — it can ask for a deploy and nothing else.
	args := append([]string{"-n", t.Command}, t.Args...)
	cmd := exec.Command("sudo", args...)
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.Env = append(os.Environ(), "TERM=dumb", "AF_DEPLOY_SHA="+sha)

	start := time.Now()
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(f, "\nFAILED TO START: %v\n", err)
		r.finish(t.Repo, "failed to start: "+err.Error(), logPath)
		r.report(t.Repo, sha, "error", "deploy did not start: "+err.Error())
		return
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		took := time.Since(start).Round(time.Second)
		if err != nil {
			fmt.Fprintf(f, "\nFAILED after %s: %v\n", took, err)
			r.log.Error("deploy failed", "repo", t.Repo, "sha", short(sha), "took", took, "log", logPath)
			r.finish(t.Repo, fmt.Sprintf("failed after %s (%v)", took, err), logPath)
			r.report(t.Repo, sha, "failure", fmt.Sprintf("deploy failed after %s — see %s on the box", took, logPath))
			return
		}
		fmt.Fprintf(f, "\nOK in %s\n", took)
		r.log.Info("deployed", "repo", t.Repo, "sha", short(sha), "took", took, "log", logPath)
		r.finish(t.Repo, fmt.Sprintf("ok in %s at %s", took, short(sha)), logPath)
		r.report(t.Repo, sha, "success", fmt.Sprintf("deployed in %s", took))
	case <-time.After(deployTimeout):
		// Kill it. A build that has hung past the timeout holds the per-target
		// lock, and a held lock means every later green build is silently
		// declined — the deploy pipeline stops without anything looking broken.
		_ = cmd.Process.Kill()
		fmt.Fprintf(f, "\nTIMED OUT after %s — killed\n", deployTimeout)
		r.log.Error("deploy timed out", "repo", t.Repo, "log", logPath)
		r.finish(t.Repo, "timed out after "+deployTimeout.String(), logPath)
		r.report(t.Repo, sha, "failure", "deploy timed out after "+deployTimeout.String())
	}
}

// finish records the outcome for /status.
func (r *runner) finish(repo, result, logPath string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last[repo] = fmt.Sprintf("%s — %s (%s)", time.Now().UTC().Format(time.RFC3339), result, logPath)
}

// report posts the deploy's outcome onto the commit it deployed.
//
// This is the whole answer to "the webhook said 200, so it deployed" — it did
// not; 202 means the job was accepted. A commit status is the one place where a
// deploy failure appears next to the thing that caused it, in a UI somebody
// already has open.
//
// Best-effort by design. A token that has expired, a rate limit, a network blip:
// none of them may turn a SUCCESSFUL deploy into a failed one, so every error
// here is logged and swallowed. The deploy already happened; this is reporting.
func (r *runner) report(repo, sha, state, description string) {
	if r.token == "" || repo == "" || sha == "" {
		return
	}
	// GitHub rejects descriptions over 140 characters with a 422, which would
	// turn a real outcome into no outcome at all.
	if len(description) > 138 {
		description = description[:137] + "…"
	}
	body, err := json.Marshal(map[string]string{
		"state":       state, // success | failure | error | pending
		"context":     "deploy",
		"description": description,
	})
	if err != nil {
		return
	}
	url := "https://api.github.com/repos/" + repo + "/statuses/" + sha
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	// Its own client with a short timeout rather than http.DefaultClient, which
	// has none: a hung API call would hold this goroutine for as long as the
	// connection stayed open, and this runs after every single deploy.
	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		r.log.Warn("commit status not posted", "repo", repo, "sha", short(sha), "err", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		r.log.Warn("commit status refused", "repo", repo, "sha", short(sha), "code", resp.StatusCode)
		return
	}
	r.log.Info("commit status posted", "repo", repo, "sha", short(sha), "state", state)
}

// short trims a commit SHA for log lines.
func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
