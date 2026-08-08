package main

import (
	"bufio"
	"bytes"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/fluxcast/produce"
	"github.com/monstercameron/ArticleFlux/internal/rundown"
)

// The CLI's small helpers, several of which decide something security-shaped and
// none of which had a test. `-dev` serves the local superadmin with no login at
// all, so the functions that decide what "loopback" means and how an environment
// variable becomes a boolean are not conveniences — they are the difference
// between a development server and an open one.

// --- isLoopback ---------------------------------------------------------------

func TestIsLoopbackAcceptsTheAddressesThatReallyAreLocal(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1:9000",
		"127.0.0.53:9000", // the whole 127/8 block is loopback, not just .1
		"localhost:9000",
		"[::1]:9000",
	} {
		if !isLoopback(addr) {
			t.Errorf("%s is a loopback bind and was not recognised as one", addr)
		}
	}
}

// The ones that must NOT count. A wildcard bind is reachable from the network,
// and treating it as local is how a no-login server ends up answering the world.
func TestIsLoopbackRejectsEverythingReachable(t *testing.T) {
	for _, addr := range []string{
		"0.0.0.0:9000",
		"[::]:9000",
		":9000", // the same wildcard, spelled the way people actually write it
		"192.168.1.10:9000",
		"10.0.0.5:9000",
		"203.0.113.7:9000",
		"feed.earlcameron.com:443",
	} {
		if isLoopback(addr) {
			t.Errorf("%s is reachable from off the box and was treated as loopback", addr)
		}
	}
}

// A malformed address must fall to the SAFE answer. isLoopback's callers use it
// to decide whether to warn about an exposed instance, so "I could not parse
// that" has to mean "assume it is exposed".
func TestIsLoopbackTreatsAnUnparseableAddressAsNotLocal(t *testing.T) {
	for _, addr := range []string{"", "not-an-address", "127.0.0.1", "[::1"} {
		if isLoopback(addr) {
			t.Errorf("%q could not be parsed and was assumed local", addr)
		}
	}
}

// --- envBool / envBoolDefault -------------------------------------------------

// The asymmetry between these two is deliberate and documented, and it is the
// kind of thing a well-meaning refactor collapses into one function. These tests
// exist to make that refactor fail.

func TestEnvBoolIsTrueOnlyForTheAffirmativeSpellings(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "True", "yes", "YES", "on", " on "} {
		t.Setenv("ARTICLEFLUX_TEST_BOOL", v)
		if !envBool("ARTICLEFLUX_TEST_BOOL") {
			t.Errorf("%q should be true", v)
		}
	}
}

// Everything else is false, INCLUDING a misspelling. For ARTICLEFLUX_DEV the
// failure mode of "meant to turn it off and it stayed on" is an unauthenticated
// reader; the other direction is a login prompt. Ambiguity resolves to the safe
// one.
func TestEnvBoolIsFalseForEverythingElseIncludingTypos(t *testing.T) {
	for _, v := range []string{"", "0", "false", "no", "off", "ture", "yess", "enabled", "y", "2"} {
		t.Setenv("ARTICLEFLUX_TEST_BOOL", v)
		if envBool("ARTICLEFLUX_TEST_BOOL") {
			t.Errorf("%q should be false; only the affirmative spellings turn a dev mode on", v)
		}
	}
}

func TestEnvBoolIsFalseWhenUnset(t *testing.T) {
	if envBool("ARTICLEFLUX_DEFINITELY_UNSET_KEY") {
		t.Error("an unset key read as true")
	}
}

// envBoolDefault runs the other way: only an explicit, recognisable "off" turns
// the feature off, and an unparseable value keeps the default rather than
// silently reading as false.
func TestEnvBoolDefaultKeepsTheDefaultForAnythingItDoesNotRecognise(t *testing.T) {
	for _, v := range []string{"", "maybe", "ture", "2", "enabled"} {
		t.Setenv("ARTICLEFLUX_TEST_BOOL", v)
		if !envBoolDefault("ARTICLEFLUX_TEST_BOOL", true) {
			t.Errorf("%q turned off a feature whose default is on", v)
		}
		if envBoolDefault("ARTICLEFLUX_TEST_BOOL", false) {
			t.Errorf("%q turned on a feature whose default is off", v)
		}
	}
}

func TestEnvBoolDefaultHonoursAnExplicitValue(t *testing.T) {
	for _, v := range []string{"0", "false", "no", "off", "OFF", " off "} {
		t.Setenv("ARTICLEFLUX_TEST_BOOL", v)
		if envBoolDefault("ARTICLEFLUX_TEST_BOOL", true) {
			t.Errorf("%q did not turn the feature off", v)
		}
	}
	for _, v := range []string{"1", "true", "yes", "on"} {
		t.Setenv("ARTICLEFLUX_TEST_BOOL", v)
		if !envBoolDefault("ARTICLEFLUX_TEST_BOOL", false) {
			t.Errorf("%q did not turn the feature on", v)
		}
	}
}

// The two must not be collapsed into one another. If this ever passes for both
// polarities, the asymmetry above has been lost.
func TestEnvBoolAndEnvBoolDefaultDisagreeOnAnUnrecognisedValue(t *testing.T) {
	t.Setenv("ARTICLEFLUX_TEST_BOOL", "ture")
	if envBool("ARTICLEFLUX_TEST_BOOL") == envBoolDefault("ARTICLEFLUX_TEST_BOOL", true) {
		t.Error("envBool and envBoolDefault now agree on an unrecognised value; " +
			"the deliberate asymmetry between them has been lost")
	}
}

// --- envOr --------------------------------------------------------------------

func TestEnvOrTreatsAnEmptyValueAsUnset(t *testing.T) {
	// .env.example ships keys with nothing after the `=` so they are visible and
	// documented. A copied-but-unfilled line must mean "I did not set this".
	for _, v := range []string{"", "   ", "\t"} {
		t.Setenv("ARTICLEFLUX_TEST_STR", v)
		if got := envOr("ARTICLEFLUX_TEST_STR", "fallback"); got != "fallback" {
			t.Errorf("%q was read as a value, giving %q", v, got)
		}
	}
	t.Setenv("ARTICLEFLUX_TEST_STR", "  set  ")
	if got := envOr("ARTICLEFLUX_TEST_STR", "fallback"); got != "set" {
		t.Errorf("envOr = %q, want the trimmed value", got)
	}
}

// --- splitList / originSummary ------------------------------------------------

func TestSplitListTrimsAndDropsEmpties(t *testing.T) {
	got := splitList(" https://a.example , ,https://b.example,  ")
	want := []string{"https://a.example", "https://b.example"}
	if len(got) != len(want) {
		t.Fatalf("splitList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("splitList[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// An empty flag must produce no entries at all, not one empty string — an
	// allowlist holding "" would match nothing and read as configured.
	if got := splitList(""); len(got) != 0 {
		t.Errorf("splitList(\"\") = %v, want nothing", got)
	}
	if got := splitList("  ,  , "); len(got) != 0 {
		t.Errorf("a flag of only separators produced %v", got)
	}
}

// The empty case must not read like a safe default. "(unset …)" in the boot line
// is what tells somebody the allowlist is not doing anything.
func TestOriginSummaryDoesNotDressUpAnEmptyAllowlist(t *testing.T) {
	got := originSummary(nil)
	if !strings.Contains(got, "unset") {
		t.Errorf("an empty allowlist is summarised as %q, which does not say it is unset", got)
	}
	if got := originSummary([]string{"https://a.example", "https://b.example"}); got != "https://a.example,https://b.example" {
		t.Errorf("originSummary = %q", got)
	}
}

// --- logPosture ---------------------------------------------------------------

// The boot line is the only place the running posture is stated. A production
// instance that logged like a development one, or the reverse, is how somebody
// spends an afternoon on the wrong theory.

func logLines(t *testing.T, dev bool, addr string, origins []string, behindProxy bool) string {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logPosture(log, dev, addr, origins, behindProxy, 1)
	return buf.String()
}

func TestLogPostureShoutsAboutDevelopmentMode(t *testing.T) {
	out := logLines(t, true, "127.0.0.1:9000", nil, false)
	if !strings.Contains(out, "NO LOGIN") {
		t.Errorf("the dev boot line does not say there is no login:\n%s", out)
	}
	// Warn, not Info: this describes a server anyone who can reach the port
	// owns, and it must not read like routine startup chatter.
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("the dev boot line is not a warning:\n%s", out)
	}
}

func TestLogPostureStatesProductionPlainly(t *testing.T) {
	out := logLines(t, false, "127.0.0.1:9000", []string{"https://feed.example"}, true)
	if !strings.Contains(out, "login required") {
		t.Errorf("the production boot line does not state the posture:\n%s", out)
	}
	if strings.Contains(out, "NO LOGIN") {
		t.Errorf("a production boot claimed there is no login:\n%s", out)
	}
}

// A public bind with no origin allowlist falls back to comparing Origin against
// Host, which holds only as long as whatever is in front forwards Host
// faithfully. Somebody has to be told.
func TestLogPostureWarnsAboutAPublicBindWithNoOriginAllowlist(t *testing.T) {
	out := logLines(t, false, "0.0.0.0:9000", nil, false)
	if !strings.Contains(out, "no -origin set") {
		t.Errorf("a public bind with no allowlist drew no warning:\n%s", out)
	}
	// The same instance on loopback is the ordinary local case and must stay
	// quiet, or the warning becomes noise nobody reads.
	quiet := logLines(t, false, "127.0.0.1:9000", nil, false)
	if strings.Contains(quiet, "no -origin set") {
		t.Errorf("a loopback bind drew the public-bind warning:\n%s", quiet)
	}
}

// Proxied but not declared means every client address in the log is the proxy's
// — the difference between "who is hammering the login" being answerable and
// not.
func TestLogPostureWarnsWhenAProxyLooksUndeclared(t *testing.T) {
	out := logLines(t, false, "127.0.0.1:9000", []string{"https://feed.example"}, false)
	if !strings.Contains(out, "-behind-proxy") {
		t.Errorf("a loopback bind with origins but no declared proxy drew no warning:\n%s", out)
	}
	declared := logLines(t, false, "127.0.0.1:9000", []string{"https://feed.example"}, true)
	if strings.Contains(declared, "every client address in the log will be the proxy's") {
		t.Errorf("the shipped deployment shape drew a warning:\n%s", declared)
	}
}

// --- statusRecorder / logging -------------------------------------------------

func TestStatusRecorderRemembersTheStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	s := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	s.WriteHeader(http.StatusTeapot)

	if s.status != http.StatusTeapot {
		t.Errorf("recorded %d, want %d", s.status, http.StatusTeapot)
	}
	// And it must still reach the client, not merely be remembered.
	if rec.Code != http.StatusTeapot {
		t.Errorf("the underlying writer got %d", rec.Code)
	}
}

// Wrapping a ResponseWriter without Flush silently breaks streaming, and without
// Hijack the WebSocket upgrade fails with no obvious cause. Both are pass-through
// by design, and both are the kind of method a refactor drops.
func TestStatusRecorderPassesFlushThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	s := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	s.Flush()
	if !rec.Flushed {
		t.Error("Flush did not reach the underlying writer; streaming would silently buffer")
	}
}

// A writer that cannot hijack must say so rather than panic — the tunnel needs a
// diagnosable failure, not a crashed request.
func TestStatusRecorderReportsWhenHijackIsUnsupported(t *testing.T) {
	s := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	if _, _, err := s.Hijack(); err == nil {
		t.Error("Hijack on a non-hijackable writer reported success")
	}
}

// hijackable is a ResponseWriter that can be hijacked, standing in for the real
// server's connection so the pass-through is proven rather than assumed.
type hijackable struct {
	http.ResponseWriter
	called bool
}

func (h *hijackable) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.called = true
	return nil, nil, nil
}

func TestStatusRecorderPassesHijackThrough(t *testing.T) {
	h := &hijackable{ResponseWriter: httptest.NewRecorder()}
	s := &statusRecorder{ResponseWriter: h, status: http.StatusOK}
	if _, _, err := s.Hijack(); err != nil {
		t.Fatalf("Hijack: %v", err)
	}
	if !h.called {
		t.Error("Hijack did not reach the underlying writer; the WebSocket upgrade would fail")
	}
}

func TestLoggingRecordsTheRequestAndItsStatus(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := logging(log, false, 1, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/articles", nil))

	out := buf.String()
	for _, want := range []string{"method=GET", "path=/articles", "status=404"} {
		if !strings.Contains(out, want) {
			t.Errorf("the request log is missing %s:\n%s", want, out)
		}
	}
}

// The tunnel is one long-lived request. Logging its duration would emit a single
// line hours later and nothing in between, so it gets an open/close pair instead.
func TestLoggingTreatsTheTunnelAsASession(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := logging(log, false, 1, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/grpc", nil))

	out := buf.String()
	if !strings.Contains(out, "tunnel open") || !strings.Contains(out, "tunnel closed") {
		t.Errorf("the tunnel was not logged as a session:\n%s", out)
	}
	if strings.Contains(out, "msg=req") {
		t.Errorf("the tunnel was also logged as an ordinary request:\n%s", out)
	}
}

// The forwarded header is believed only behind -behind-proxy. Believing it
// otherwise lets any client write its own address into the log.
func TestClientAddrBelievesTheProxyHeaderOnlyWhenToldTo(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")

	if got := clientAddr(req, false, 1); strings.Contains(got, "203.0.113.7") {
		t.Errorf("clientAddr = %q; the header was believed without -behind-proxy", got)
	}
	if got := clientAddr(req, true, 1); !strings.Contains(got, "203.0.113.7") {
		t.Errorf("clientAddr = %q; the header was ignored behind a declared proxy", got)
	}
}

// --- small formatters ---------------------------------------------------------

func TestTruncateForKeepsOutputOnOneLine(t *testing.T) {
	if got := truncateFor("short", 20); got != "short" {
		t.Errorf("a string under the limit was changed: %q", got)
	}
	long := truncateFor(strings.Repeat("x", 100), 20)
	if len([]rune(long)) > 20 {
		t.Errorf("truncateFor returned %d runes for a limit of 20: %q", len([]rune(long)), long)
	}
	// Multi-byte input must not be cut mid-rune, which would emit a replacement
	// character into a terminal transcript.
	multi := truncateFor(strings.Repeat("é", 100), 20)
	if !isValidUTF8(multi) {
		t.Errorf("truncateFor cut a multi-byte rune in half: %q", multi)
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

// A rate of zero would divide by zero downstream; the guard is what keeps a
// rundown from crashing on an empty run.
//
// The local rateOrOne this used to call was replaced by rundown.SafeRate — the
// package that owns the arithmetic now owns the guard on its one input, so the
// printed per-story minutes and the rundown they describe cannot disagree
// about what `-rate 0` means. NaN and +Inf are in the table because
// strconv.ParseFloat, which is what the flag package parses `-rate` with,
// accepts both spellings and neither is caught by a `<= 0` test.
func TestRateGuardNeverReturnsAnUnusableRate(t *testing.T) {
	for _, in := range []float64{0, -1, -0.0001, math.NaN(), math.Inf(1), math.Inf(-1)} {
		got := rundown.SafeRate(in)
		if !(got > 0) || math.IsInf(got, 0) {
			t.Errorf("SafeRate(%v) = %v, which would divide by zero or make the "+
				"target meaningless", in, got)
		}
	}
	if got := rundown.SafeRate(1.5); got != 1.5 {
		t.Errorf("SafeRate(1.5) = %v, want it unchanged", got)
	}
}

func TestCommitIsAlwaysPrintable(t *testing.T) {
	// The build stamp goes in a boot line and a footer. A test binary has no vcs
	// revision, so this proves the fallback rather than the happy path — which
	// is the case that would otherwise print an empty string.
	got := commit()
	if got == "" {
		t.Error("commit() returned an empty string; the boot line would say nothing at all")
	}
	if len(got) > 12 {
		t.Errorf("commit() = %q, which is longer than the 12 characters the format allows", got)
	}
}

func TestUsageNamesEverySubcommand(t *testing.T) {
	// usage is what somebody reads after typing the command wrong. A subcommand
	// that exists and is not listed is one nobody finds.
	var buf bytes.Buffer
	old := usageOut
	usageOut = &buf
	t.Cleanup(func() { usageOut = old })

	usage()
	out := buf.String()
	for _, cmd := range []string{"serve", "migrate", "init", "adduser", "passwd", "backup", "import"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("usage does not mention %q:\n%s", cmd, out)
		}
	}
}

// --- serve's refusals ---------------------------------------------------------
//
// These four checks are the most security-critical lines in the program, and
// they are reachable from a test without starting anything: serve validates the
// combination and returns before it opens a database or binds a socket.
//
// The history is written on the checks themselves. DevMode used to be
// `isLoopback(*addr)`, which is a fact about the SOCKET and not about the
// DEPLOYMENT — every reverse-proxy setup, including the nginx one this ships
// with, terminates TLS on :443 and forwards to 127.0.0.1:9000. Under that rule
// the canonical way to host this was also the way to publish an entire reading
// history to anyone who typed the domain.

func serveErr(t *testing.T, args ...string) error {
	t.Helper()
	return serve(cliLogger(), args)
}

// The belt: -dev off loopback is refused outright, because the flag alone would
// eventually be pasted into a systemd unit by somebody who wanted to skip a
// login screen once.
func TestServeRefusesDevModeOnAPublicBind(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:9000", "192.168.1.10:9000", "203.0.113.7:9000", ":9000"} {
		err := serveErr(t, "-dev", "-addr", addr, "-db", tempDB(t))
		if err == nil {
			t.Errorf("-dev on %s was accepted; the reader would be open with no login", addr)
			continue
		}
		if !strings.Contains(err.Error(), "no login") {
			t.Errorf("the refusal for %s does not say what is at stake: %v", addr, err)
		}
		// And it points at the way out, so somebody mid-deploy is not stuck.
		if !strings.Contains(err.Error(), "init") {
			t.Errorf("the refusal for %s does not say what to do instead: %v", addr, err)
		}
	}
}

// The braces: a proxy in front of a loopback bind IS a published instance, and
// that is exactly the case -dev must never apply to. The loopback rule alone
// cannot catch it — the shipped systemd unit binds 127.0.0.1 by design — so a
// stale `.env` carrying ARTICLEFLUX_DEV onto a server would walk the original
// vulnerability straight back in through a new door.
func TestServeRefusesDevModeBehindAProxy(t *testing.T) {
	err := serveErr(t, "-dev", "-behind-proxy", "-addr", "127.0.0.1:9000", "-db", tempDB(t))
	if err == nil {
		t.Fatal("-dev with -behind-proxy was accepted on a loopback bind")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("the refusal does not name the conflict: %v", err)
	}
	if !strings.Contains(err.Error(), "published instance") {
		t.Errorf("the refusal does not explain why a loopback bind is not enough: %v", err)
	}
}

// pprof gets the same two clauses, and the instinct that a profiling endpoint
// needs a weaker rule is wrong: /debug/pprof/profile parks a CPU sampler on the
// process for thirty seconds per unauthenticated GET, so anyone who can reach it
// can hold the reader down.
func TestServeRefusesPprofOnAPublicBind(t *testing.T) {
	err := serveErr(t, "-pprof", "-addr", "0.0.0.0:9000", "-db", tempDB(t))
	if err == nil {
		t.Fatal("-pprof on a public bind was accepted")
	}
	if !strings.Contains(err.Error(), "unauthenticated") {
		t.Errorf("the refusal does not say the surface is unauthenticated: %v", err)
	}
	if !strings.Contains(err.Error(), "SSH tunnel") {
		t.Errorf("the refusal does not offer the safe way to profile: %v", err)
	}
}

func TestServeRefusesPprofBehindAProxy(t *testing.T) {
	err := serveErr(t, "-pprof", "-behind-proxy", "-addr", "127.0.0.1:9000", "-db", tempDB(t))
	if err == nil {
		t.Fatal("-pprof with -behind-proxy was accepted")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("the refusal does not name the conflict: %v", err)
	}
}

// The environment is a real input path, not just a convenience — that is the
// whole reason the -behind-proxy clause exists. A refusal that only checked the
// FLAG would miss the stale-`.env`-on-a-server case entirely.
func TestServeRefusesDevModeSetFromTheEnvironment(t *testing.T) {
	t.Setenv("ARTICLEFLUX_DEV", "1")
	if err := serveErr(t, "-addr", "0.0.0.0:9000", "-db", tempDB(t)); err == nil {
		t.Error("ARTICLEFLUX_DEV=1 on a public bind was accepted")
	}
}

func TestServeRefusesPprofSetFromTheEnvironment(t *testing.T) {
	t.Setenv("ARTICLEFLUX_PPROF", "1")
	if err := serveErr(t, "-addr", "0.0.0.0:9000", "-db", tempDB(t)); err == nil {
		t.Error("ARTICLEFLUX_PPROF=1 on a public bind was accepted")
	}
}

// The combination that IS allowed, and it has to stay allowed or the shipped
// deployment cannot start: loopback bind, proxy declared, no dev mode, no pprof.
// This is checked by getting PAST the validation rather than by running a
// server — an unreachable web root ends it a moment later, which is enough to
// prove the refusals above did not fire.
func TestServeAllowsTheShippedDeploymentShape(t *testing.T) {
	err := serveErr(t, "-behind-proxy", "-addr", "127.0.0.1:0", "-db", tempDB(t),
		"-web", filepath.Join(t.TempDir(), "no-such-web-root"), "-poll", "0")

	// Either outcome is a pass, and the assertion is about WHICH failures are
	// permitted rather than about whether one happened: serve is allowed to get
	// all the way up, and it is allowed to fail on something downstream of the
	// validation. What it may not do is refuse this combination, because this
	// combination is the shipped systemd unit — loopback bind, proxy declared,
	// no dev mode, no pprof — and a refusal here would mean the deployment
	// cannot start.
	if err == nil {
		return
	}
	for _, refusal := range []string{"mutually exclusive", "no login", "unauthenticated"} {
		if strings.Contains(err.Error(), refusal) {
			t.Fatalf("the shipped deployment shape was refused: %v", err)
		}
	}
}

// --- printRundown ---------------------------------------------------------------
//
// The deliverable Job 4 asks for: a running order a person can READ and judge,
// not a struct dump. It is the only view anybody gets of what the producer
// decided, so a row that silently loses its headline or its source is a rundown
// that cannot be judged at all.

func rundownFixture() produce.Produced {
	return produce.Produced{
		Rundown: rundown.Rundown{
			Title:  "Tuesday Briefing",
			Target: 20 * time.Minute,
			Segments: []rundown.Segment{
				{
					Theme: "technology",
					Stories: []rundown.Story{
						{ItemID: "i1", Role: rundown.RoleLead, Words: 300, Sources: []string{"Alpha Journal"}},
						{ItemID: "i2", Role: rundown.RoleQuickHit, Words: 90, Sources: []string{"Beta Notes", "Gamma Wire"}},
					},
				},
				{
					Theme: "", // unsorted
					Stories: []rundown.Story{
						{ItemID: "i3", Role: rundown.RoleQuickHit, Words: 80},
					},
				},
			},
		},
		Titles: map[string]string{
			"i1": "Speculative decoding without a draft model",
			"i2": "The write lock is not your problem",
			// i3 deliberately absent — a story whose headline never arrived.
		},
	}
}

func TestPrintRundownShowsTheRunningOrder(t *testing.T) {
	var buf bytes.Buffer
	printRundown(&buf, rundownFixture(), 1.0)
	got := buf.String()

	for _, want := range []string{
		"Tuesday Briefing",
		"technology",
		"Speculative decoding without a draft model",
		"The write lock is not your problem",
		"Alpha Journal",
		"3 stories",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the rundown omits %q:\n%s", want, got)
		}
	}
}

// A title is prose, and this package refuses to write prose — an empty one is
// carried through honestly rather than replaced with invented copy. The printer
// still has to render something a person can read.
func TestPrintRundownLabelsAnUntitledRundown(t *testing.T) {
	p := rundownFixture()
	p.Rundown.Title = ""
	var buf bytes.Buffer
	printRundown(&buf, p, 1.0)
	if !strings.Contains(buf.String(), "untitled") {
		t.Errorf("an untitled rundown printed a blank first line:\n%s", buf.String())
	}
}

// The unsorted segment is a real segment. Printing it with a blank header would
// make its stories look like they belonged to whatever came before.
func TestPrintRundownLabelsTheUnsortedSegment(t *testing.T) {
	var buf bytes.Buffer
	printRundown(&buf, rundownFixture(), 1.0)
	if !strings.Contains(buf.String(), "(unsorted)") {
		t.Errorf("the themeless segment has no header:\n%s", buf.String())
	}
}

// A story whose headline never arrived falls back to its id rather than printing
// an empty row — an unreadable row is still evidence, a blank one is a bug you
// cannot see.
func TestPrintRundownFallsBackToTheItemIDWhenAHeadlineIsMissing(t *testing.T) {
	var buf bytes.Buffer
	printRundown(&buf, rundownFixture(), 1.0)
	if !strings.Contains(buf.String(), "i3") {
		t.Errorf("a story with no headline printed nothing identifying:\n%s", buf.String())
	}
}

// Same for sources: silence would read as "this story has no outlet", which is a
// claim, rather than "we do not have one on record", which is the truth.
func TestPrintRundownSaysWhenAStoryHasNoSourceOnRecord(t *testing.T) {
	var buf bytes.Buffer
	printRundown(&buf, rundownFixture(), 1.0)
	if !strings.Contains(buf.String(), "no source on record") {
		t.Errorf("a sourceless story printed a blank source:\n%s", buf.String())
	}
}

// The reading rate scales the minute estimates — that is what makes the rundown
// a plan for a particular listener rather than an average one. A faster rate
// must produce a shorter estimate.
func TestPrintRundownScalesTheEstimateWithTheReadingRate(t *testing.T) {
	var slow, fast bytes.Buffer
	printRundown(&slow, rundownFixture(), 1.0)
	printRundown(&fast, rundownFixture(), 2.0)
	if slow.String() == fast.String() {
		t.Error("doubling the reading rate changed nothing in the rundown")
	}
}

// A zero or negative rate is a caller bug, and it must not divide by zero and
// print Inf minutes into the one artefact a person reads.
func TestPrintRundownSurvivesAZeroReadingRate(t *testing.T) {
	var buf bytes.Buffer
	printRundown(&buf, rundownFixture(), 0)
	got := buf.String()
	if strings.Contains(got, "Inf") || strings.Contains(got, "NaN") {
		t.Errorf("a zero rate produced a non-finite estimate:\n%s", got)
	}
}

// An empty rundown is what a thin candidate pool produces. It has to print a
// readable, honest zero rather than a header with nothing under it.
func TestPrintRundownOnAnEmptyRundown(t *testing.T) {
	var buf bytes.Buffer
	printRundown(&buf, produce.Produced{}, 1.0)
	got := buf.String()
	if !strings.Contains(got, "0 stories") {
		t.Errorf("an empty rundown did not report zero stories:\n%s", got)
	}
	if strings.Contains(got, "NaN") {
		t.Errorf("an empty rundown produced NaN:\n%s", got)
	}
}

// The log format is the one setting whose effect is invisible until somebody
// points a collector at this and finds `key=value` where JSON was expected.
func TestLogFormatSelection(t *testing.T) {
	for _, c := range []struct{ name, format, want string }{
		{"default is text", "", "msg=hello"},
		{"text explicitly", "text", "msg=hello"},
		{"json", "json", `"msg":"hello"`},
		{"case and space tolerated", "  JSON ", `"msg":"hello"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, err := newLogHandler(c.format)
			if err != nil {
				t.Fatalf("newLogHandler(%q): %v", c.format, err)
			}
			// The handler writes to os.Stderr by construction, so this checks the
			// TYPE it chose rather than capturing output — replacing the writer
			// would test a handler nobody builds.
			switch c.format {
			case "json", "  JSON ":
				if _, ok := h.(*slog.JSONHandler); !ok {
					t.Errorf("format %q gave %T, want *slog.JSONHandler", c.format, h)
				}
			default:
				if _, ok := h.(*slog.TextHandler); !ok {
					t.Errorf("format %q gave %T, want *slog.TextHandler", c.format, h)
				}
			}
		})
	}

	// Rejected, not defaulted. An operator who asked for JSON and silently got
	// text would debug their log pipeline instead of their typo.
	if _, err := newLogHandler("xml"); err == nil {
		t.Error("newLogHandler accepted an unknown format")
	}
}

// The level control has the same property: a bad value must be refused rather
// than resolved to something plausible.
func TestLogLevelSelection(t *testing.T) {
	t.Cleanup(func() { logLevel.Set(slog.LevelInfo) })

	for name, want := range map[string]slog.Level{
		"debug": slog.LevelDebug,
		"DEBUG": slog.LevelDebug,
		" warn": slog.LevelWarn,
		"error": slog.LevelError,
		"":      slog.LevelInfo,
	} {
		if err := setLogLevel(name); err != nil {
			t.Fatalf("setLogLevel(%q): %v", name, err)
		}
		if got := logLevel.Level(); got != want {
			t.Errorf("setLogLevel(%q) gave %v, want %v", name, got, want)
		}
	}
	if err := setLogLevel("loud"); err == nil {
		t.Error("setLogLevel accepted an unknown level")
	}
}
