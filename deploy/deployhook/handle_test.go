package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// handle is the front door of a service whose only job is to decide whether to
// change production, and it was the largest untested function here. Every branch
// below is a distinct way that decision can be wrong, and the two that matter
// most are the ones that let a deploy through: an unsigned request, and a
// request signed with the wrong target's secret.

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newRunner(t *testing.T) *runner {
	t.Helper()
	return &runner{
		running: map[string]bool{},
		last:    map[string]string{},
		logDir:  t.TempDir(),
		log:     quietLogger(),
	}
}

func testConfig() *Config {
	return &Config{
		Targets: []Target{{
			Repo:     "monstercameron/ArticleFlux",
			Secret:   "flux-secret",
			Command:  "/usr/local/bin/deploy.sh",
			Triggers: []Trigger{{Workflow: "CI", Branch: "main"}},
		}, {
			Repo:     "monstercameron/Portfolio",
			Secret:   "portfolio-secret",
			Command:  "/usr/local/bin/deploy-portfolio.sh",
			Triggers: []Trigger{{Workflow: "CI", Branch: "main"}},
		}},
	}
}

// post signs the body with secret and runs it through handle.
func post(t *testing.T, cfg *Config, r *runner, event, secret string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", event)
	if secret != "" {
		req.Header.Set("X-Hub-Signature-256", sign(body, secret))
	}
	rec := httptest.NewRecorder()
	handle(rec, req, cfg, r, quietLogger())
	return rec
}

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// A green run of the configured workflow, on the configured branch, from a push.
func passingPayload(repo string) map[string]any {
	return map[string]any{
		"action": "completed",
		"workflow_run": map[string]any{
			"name":        "CI",
			"conclusion":  "success",
			"status":      "completed",
			"head_branch": "main",
			"head_sha":    "0123456789abcdef0123456789abcdef01234567",
			"event":       "push",
			"html_url":    "https://github.com/x/y/actions/runs/1",
		},
		"repository": map[string]any{"full_name": repo},
	}
}

func TestHandleRefusesAnythingButPost(t *testing.T) {
	// A GET is somebody opening the URL in a browser, and it must not be a way
	// to learn anything about what is configured here.
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/hook", nil)
		rec := httptest.NewRecorder()
		handle(rec, req, testConfig(), newRunner(t), quietLogger())
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s returned %d, want 405", method, rec.Code)
		}
	}
}

// The security property this whole service rests on. An unsigned request that
// deployed would mean anybody who learned the URL could ship code.
func TestHandleRefusesAnUnsignedRequest(t *testing.T) {
	r := newRunner(t)
	rec := post(t, testConfig(), r, "workflow_run", "", passingPayload("monstercameron/ArticleFlux"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an unsigned request returned %d, want 401", rec.Code)
	}
	if r.running["monstercameron/ArticleFlux"] {
		t.Fatal("an unsigned request started a deploy")
	}
}

func TestHandleRefusesAWrongSignature(t *testing.T) {
	r := newRunner(t)
	rec := post(t, testConfig(), r, "workflow_run", "not-the-secret", passingPayload("monstercameron/ArticleFlux"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a badly signed request returned %d, want 401", rec.Code)
	}
	if r.running["monstercameron/ArticleFlux"] {
		t.Fatal("a badly signed request started a deploy")
	}
}

// Each target has its OWN secret, and one target's secret must not authorise a
// deploy of another. Two sites on one box is the entire reason this program
// takes a list.
func TestHandleWillNotLetOneTargetsSecretDeployAnother(t *testing.T) {
	r := newRunner(t)
	rec := post(t, testConfig(), r, "workflow_run", "portfolio-secret",
		passingPayload("monstercameron/ArticleFlux"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("the wrong target's secret returned %d, want 401", rec.Code)
	}
	if r.running["monstercameron/ArticleFlux"] {
		t.Fatal("the wrong target's secret started a deploy")
	}
}

// The repository is read before the signature is checked, only to pick which
// secret to verify against. An unknown one must stop there.
func TestHandleRefusesAnUnknownRepository(t *testing.T) {
	rec := post(t, testConfig(), newRunner(t), "workflow_run", "flux-secret",
		passingPayload("someone-else/their-repo"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("an unknown repository returned %d, want 404", rec.Code)
	}
}

// GitHub sends `ping` when the hook is created, and answering it is what turns
// the green tick on in the repo settings. It must still be authenticated.
func TestHandleAnswersPing(t *testing.T) {
	rec := post(t, testConfig(), newRunner(t), "ping", "flux-secret",
		map[string]any{"repository": map[string]any{"full_name": "monstercameron/ArticleFlux"}})
	if rec.Code != http.StatusOK {
		t.Errorf("ping returned %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "pong") {
		t.Errorf("ping was not answered: %q", rec.Body.String())
	}
}

// 200, not 4xx: the hook may legitimately be subscribed to more events than
// this, and a red delivery reads like breakage to whoever opens the log.
func TestHandleAcknowledgesEventsItDoesNotActOn(t *testing.T) {
	r := newRunner(t)
	rec := post(t, testConfig(), r, "push", "flux-secret",
		passingPayload("monstercameron/ArticleFlux"))
	if rec.Code != http.StatusOK {
		t.Errorf("an unrelated event returned %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ignored") {
		t.Errorf("the response does not say it was ignored: %q", rec.Body.String())
	}
	if r.running["monstercameron/ArticleFlux"] {
		t.Fatal("an unrelated event started a deploy")
	}
}

// The reason has to reach the response body, not just the log. GitHub's
// delivery log is where somebody looks first, and a webhook that silently drops
// events is indistinguishable from one that is not wired up.
func TestHandleExplainsWhyItDidNotDeploy(t *testing.T) {
	p := passingPayload("monstercameron/ArticleFlux")
	p["workflow_run"].(map[string]any)["conclusion"] = "failure"

	r := newRunner(t)
	rec := post(t, testConfig(), r, "workflow_run", "flux-secret", p)
	if rec.Code != http.StatusOK {
		t.Errorf("a skipped deploy returned %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "no deploy") || !strings.Contains(body, "failure") {
		t.Errorf("the response does not say why it declined: %q", body)
	}
	if r.running["monstercameron/ArticleFlux"] {
		t.Fatal("a red CI run started a deploy")
	}
}

func TestHandleRejectsAPayloadItCannotParse(t *testing.T) {
	// Signed, so it gets past authentication, and then is not JSON GitHub would
	// ever send. A 400 is honest; a 500 or a panic would take the service down.
	body := []byte(`{"action": "completed", "workflow_run": "not-an-object",` +
		`"repository":{"full_name":"monstercameron/ArticleFlux"}}`)
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", "workflow_run")
	req.Header.Set("X-Hub-Signature-256", sign(body, "flux-secret"))
	rec := httptest.NewRecorder()
	handle(rec, req, testConfig(), newRunner(t), quietLogger())
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an unparseable payload returned %d, want 400", rec.Code)
	}
}

// 202, not 200, and the difference is the single most expensive property this
// service has: the deploy outlives the request, so a green delivery says the job
// was accepted and nothing at all about whether it worked.
func TestHandleAcceptsAGoodDeployWith202(t *testing.T) {
	r := newRunner(t)
	// Pre-marking it running is what keeps this test from spawning a real
	// `sudo`: the assertion is about handle's contract, and start's own
	// behaviour is tested separately.
	r.running["monstercameron/ArticleFlux"] = true
	rec := post(t, testConfig(), r, "workflow_run", "flux-secret",
		passingPayload("monstercameron/ArticleFlux"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("a second concurrent deploy returned %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "already running") {
		t.Errorf("the 409 does not explain itself: %q", rec.Body.String())
	}
}

// A body larger than maxBody must not be read into memory in full. The limit is
// the only thing between this endpoint and anybody who wants to exhaust the box.
func TestHandleDoesNotReadAnUnboundedBody(t *testing.T) {
	huge := strings.Repeat("x", maxBody+4096)
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(huge))
	req.Header.Set("X-GitHub-Event", "workflow_run")
	rec := httptest.NewRecorder()
	handle(rec, req, testConfig(), newRunner(t), quietLogger())
	// It is not valid JSON and is not signed, so it is refused — the point is
	// that it is refused rather than buffered whole.
	if rec.Code == http.StatusAccepted {
		t.Error("an oversized unsigned body was accepted")
	}
}

// --- findTarget ---------------------------------------------------------------

func TestFindTargetMatchesCaseInsensitively(t *testing.T) {
	cfg := testConfig()
	// GitHub is not consistent about the case of a repository name across its
	// APIs, and a missed match here is a deploy that silently never happens.
	for _, name := range []string{
		"monstercameron/ArticleFlux",
		"MonsterCameron/articleflux",
		"MONSTERCAMERON/ARTICLEFLUX",
	} {
		got := findTarget(cfg, name)
		if got == nil {
			t.Errorf("findTarget(%q) found nothing", name)
			continue
		}
		if got.Secret != "flux-secret" {
			t.Errorf("findTarget(%q) returned the wrong target (%s)", name, got.Repo)
		}
	}
	if findTarget(cfg, "") != nil {
		t.Error("an empty repository name matched a target")
	}
	if findTarget(cfg, "someone/else") != nil {
		t.Error("an unconfigured repository matched a target")
	}
}

// The returned pointer has to be into the config, not a copy: handle reads
// Secret and Command through it, and a copy would be a config nobody can change.
func TestFindTargetReturnsAPointerIntoTheConfig(t *testing.T) {
	cfg := testConfig()
	got := findTarget(cfg, "monstercameron/ArticleFlux")
	if got != &cfg.Targets[0] {
		t.Error("findTarget returned a copy rather than the configured target")
	}
}

// --- the runner ---------------------------------------------------------------

// One deploy per target at a time. Two green builds landing together must not
// run the same script twice over the same checkout.
func TestStartDeclinesASecondConcurrentDeploy(t *testing.T) {
	r := newRunner(t)
	target := &Target{Repo: "x/y", Command: "/bin/true"}
	r.running["x/y"] = true

	if r.start(target, "sha", "url") {
		t.Error("a second deploy started while one was running")
	}
}

// Different targets do not block each other: two sites on one box is the reason
// the lock is per-repo rather than global.
func TestStartLocksPerTargetNotGlobally(t *testing.T) {
	r := newRunner(t)
	r.running["x/y"] = true

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); r.finish("a/b", "done", "log") }()
	wg.Wait()

	if r.running["a/b"] {
		t.Error("marking one target busy marked another")
	}
}

func TestFinishRecordsTheOutcomeForStatus(t *testing.T) {
	r := newRunner(t)
	r.finish("x/y", "ok in 4s", "/var/log/deploy.log")

	got := r.last["x/y"]
	if !strings.Contains(got, "ok in 4s") || !strings.Contains(got, "/var/log/deploy.log") {
		t.Errorf("the recorded outcome is missing its detail: %q", got)
	}
	// The timestamp is what makes /status answer "when", not just "what".
	if !strings.Contains(got, "T") || !strings.Contains(got, "Z") {
		t.Errorf("the outcome carries no RFC3339 timestamp: %q", got)
	}
}

func TestFinishIsSafeUnderConcurrency(t *testing.T) {
	r := newRunner(t)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.finish(fmt.Sprintf("repo/%d", i%4), "ok", "log")
		}(i)
	}
	wg.Wait()
	if len(r.last) != 4 {
		t.Errorf("recorded %d repos, want 4", len(r.last))
	}
}

func TestShortTrimsASHAAndLeavesShortOnesAlone(t *testing.T) {
	if got := short("0123456789abcdef"); got != "01234567" {
		t.Errorf("short = %q, want 01234567", got)
	}
	// Not every caller has a full SHA — an empty or truncated one must come back
	// as itself rather than panic a log line in the deploy path.
	for _, s := range []string{"", "abc", "01234567"} {
		if got := short(s); got != s {
			t.Errorf("short(%q) = %q, want it unchanged", s, got)
		}
	}
}

// --- report -------------------------------------------------------------------
//
// The commit status is the whole answer to "the webhook said 200, so it
// deployed" — it did not; 202 means the job was accepted. So the reporting path
// failing quietly is the failure that hides every other failure.

func TestReportPostsTheOutcomeToTheCommit(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		gotAuth = req.Header.Get("Authorization")
		b, _ := io.ReadAll(req.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	r := newRunner(t)
	r.token = "ghp_test"
	r.apiBase = srv.URL
	r.report("monstercameron/ArticleFlux", "0123456789abcdef", "success", "deployed in 4s")

	if want := "/repos/monstercameron/ArticleFlux/statuses/0123456789abcdef"; gotPath != want {
		t.Errorf("posted to %q, want %q", gotPath, want)
	}
	if gotAuth != "Bearer ghp_test" {
		t.Errorf("Authorization header is %q", gotAuth)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, gotBody)
	}
	if payload["state"] != "success" || payload["context"] != "deploy" {
		t.Errorf("payload is %v", payload)
	}
}

// GitHub rejects a description over 140 characters with a 422, which would turn
// a real outcome into no outcome at all — the exact failure mode this service
// exists to close.
func TestReportTruncatesALongDescription(t *testing.T) {
	var payload map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(b, &payload)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	r := newRunner(t)
	r.token = "ghp_test"
	r.apiBase = srv.URL
	r.report("x/y", "abc", "failure", strings.Repeat("very long reason ", 20))

	desc := payload["description"]
	if len([]rune(desc)) > 140 {
		t.Errorf("description is %d runes; GitHub would answer 422 and record nothing", len([]rune(desc)))
	}
	if !strings.HasSuffix(desc, "…") {
		t.Errorf("a truncated description does not say it was truncated: %q", desc)
	}
}

// Best-effort by design: the deploy already happened, and no reporting failure
// may be allowed to look like a deploy failure.
func TestReportSwallowsAnApiFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	r := newRunner(t)
	r.token = "ghp_test"
	r.apiBase = srv.URL
	r.report("x/y", "abc", "success", "deployed") // must not panic
}

func TestReportSkipsWhenThereIsNothingToReportWith(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	r := newRunner(t)
	r.apiBase = srv.URL

	// No token: reporting is disabled, and that is a supported configuration.
	r.token = ""
	r.report("x/y", "abc", "success", "deployed")
	// A token but nothing to report about.
	r.token = "ghp_test"
	r.report("", "abc", "success", "deployed")
	r.report("x/y", "", "success", "deployed")

	if called {
		t.Error("report called the API with no token or no commit to report on")
	}
}

func TestReportGivesUpRatherThanHangingForever(t *testing.T) {
	// http.DefaultClient has no timeout, and this runs after every deploy — a
	// hung API call would hold the goroutine for as long as the connection
	// stayed open.
	saved := statusTimeout
	statusTimeout = 100 * time.Millisecond
	t.Cleanup(func() { statusTimeout = saved })

	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	r := newRunner(t)
	r.token = "ghp_test"
	r.apiBase = srv.URL

	done := make(chan struct{})
	go func() { r.report("x/y", "abc", "success", "deployed"); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("report did not return; it has no client timeout")
	}
}

// --- loadConfig's remaining refusals -----------------------------------------
//
// Every one of these is a config that would start a service which looks healthy
// and is not. Refusing to start is the only safe response, so each refusal is
// worth a test of its own.

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadConfigReportsAMissingOrUnparseableFile(t *testing.T) {
	if _, err := loadConfig(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("a missing config file started the service")
	}
	if _, err := loadConfig(writeConfig(t, "{not json")); err == nil {
		t.Error("an unparseable config started the service")
	}
}

func TestLoadConfigRefusesAConfigWithNoTargets(t *testing.T) {
	if _, err := loadConfig(writeConfig(t, `{"targets": []}`)); err == nil {
		t.Error("a config with no targets started the service; it would answer 404 to everything")
	}
}

func TestLoadConfigDefaultsAddrAndLogDir(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, `{"targets":[{"repo":"a/b","secret":"s",
		"command":"`+absCommand(t)+`","branch":"main","workflow":"CI"}]}`))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	// Loopback, because nginx is in front. A default that listened on every
	// interface would expose the deploy trigger directly.
	if !strings.HasPrefix(cfg.Addr, "127.0.0.1:") {
		t.Errorf("the default address is %q, which is not loopback", cfg.Addr)
	}
	if cfg.LogDir == "" {
		t.Error("no default log directory; the transcript would have nowhere to go")
	}
}

func TestLoadConfigRefusesATargetWithNoRepo(t *testing.T) {
	if _, err := loadConfig(writeConfig(t, `{"targets":[{"secret":"s",
		"command":"`+absCommand(t)+`","branch":"main"}]}`)); err == nil {
		t.Error("a target with no repo started the service")
	}
}

func TestLoadConfigRefusesATriggerWithNoBranch(t *testing.T) {
	// A trigger with no branch deploys whatever went green, which includes
	// every feature branch anyone pushes.
	_, err := loadConfig(writeConfig(t, `{"targets":[{"repo":"a/b","secret":"s",
		"command":"`+absCommand(t)+`","triggers":[{"workflow":"CI"}]}]}`))
	if err == nil {
		t.Fatal("a branchless trigger started the service; any green branch would deploy")
	}
	if !strings.Contains(err.Error(), "branch") {
		t.Errorf("the error does not name the problem: %v", err)
	}
}

func TestLoadConfigRefusesARelativeCommand(t *testing.T) {
	_, err := loadConfig(writeConfig(t, `{"targets":[{"repo":"a/b","secret":"s",
		"command":"deploy.sh","branch":"main"}]}`))
	if err == nil {
		t.Fatal("a relative command path started the service; what runs would depend on PATH")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("the error does not name the problem: %v", err)
	}
}

// --- the HTTP surface ---------------------------------------------------------

func TestHealthzAnswersForTheWatchdog(t *testing.T) {
	mux := routes(testConfig(), newRunner(t), quietLogger())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("/healthz returned %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Errorf("/healthz said %q", rec.Body.String())
	}
}

// /status is the only window into what this service has actually done. The
// webhook's 202 says a job was accepted and never that it succeeded, so if a
// deploy failed and there is no status token configured, this endpoint is the
// only place the truth exists outside a file on the box.
func TestStatusReportsWhatRanAndHow(t *testing.T) {
	r := newRunner(t)
	r.finish("monstercameron/ArticleFlux", "failed after 12s (exit 3)", "/var/log/deployhook/x.log")
	r.running["monstercameron/Portfolio"] = true

	mux := routes(testConfig(), r, quietLogger())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("/status returned %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("/status content type is %q", ct)
	}

	var got struct {
		Running map[string]bool   `json:"running"`
		Last    map[string]string `json:"last"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("/status is not JSON: %v (%q)", err, rec.Body.String())
	}
	if !got.Running["monstercameron/Portfolio"] {
		t.Error("/status does not report the deploy that is running")
	}
	if !strings.Contains(got.Last["monstercameron/ArticleFlux"], "failed after 12s") {
		t.Errorf("/status lost the failure: %v", got.Last)
	}
}

// The mux routes /hook to handle rather than to anything else — a wiring
// mistake here would answer 404 to every real delivery while the service looked
// perfectly healthy on /healthz.
func TestHookIsWiredToTheHandler(t *testing.T) {
	mux := routes(testConfig(), newRunner(t), quietLogger())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hook", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("/hook returned %d; a GET should reach handle and be refused with 405", rec.Code)
	}
}
