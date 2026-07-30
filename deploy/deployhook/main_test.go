package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gate is the reason this program exists, so the cases that must NOT deploy
// are tested individually. Each one is a distinct way a naive "deploy on
// workflow_run" would ship something nobody approved.
func TestShouldSkip(t *testing.T) {
	target := &Target{Repo: "monstercameron/ArticleFlux", Triggers: []Trigger{{Workflow: "CI", Branch: "main"}}}

	pass := func() *hookPayload {
		var p hookPayload
		p.Action = "completed"
		p.WorkflowRun.Name = "CI"
		p.WorkflowRun.Conclusion = "success"
		p.WorkflowRun.HeadBranch = "main"
		p.WorkflowRun.Event = "push"
		return &p
	}

	if reason := shouldSkip(pass(), target); reason != "" {
		t.Fatalf("a green CI run on main should deploy, got %q", reason)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*hookPayload)
	}{
		// The headline case: red must never ship.
		{"failed run", func(p *hookPayload) { p.WorkflowRun.Conclusion = "failure" }},
		// A cancelled run has not proven anything. It is not a pass.
		{"cancelled run", func(p *hookPayload) { p.WorkflowRun.Conclusion = "cancelled" }},
		// Neither has a skipped one, which is the subtle one: "skipped" reads as
		// benign and is the conclusion a path-filtered workflow reports.
		{"skipped run", func(p *hookPayload) { p.WorkflowRun.Conclusion = "skipped" }},
		{"still running", func(p *hookPayload) { p.Action = "requested" }},
		// A feature branch going green is exactly what should NOT reach the box.
		{"other branch", func(p *hookPayload) { p.WorkflowRun.HeadBranch = "feature/x" }},
		// A docs or pages workflow going green is not evidence the code builds.
		{"other workflow", func(p *hookPayload) { p.WorkflowRun.Name = "Demo (GitHub Pages)" }},
		// A pull_request run tests a merge commit that is not on main. Deploying
		// it ships code that exists nowhere in the branch history.
		{"pull request run", func(p *hookPayload) { p.WorkflowRun.Event = "pull_request" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := pass()
			tc.mutate(p)
			if reason := shouldSkip(p, target); reason == "" {
				t.Fatalf("%s should not deploy, but was allowed", tc.name)
			}
		})
	}
}

// The promotion path, which is the one that was broken.
//
// promote.yml fast-forwards main using GITHUB_TOKEN, and a push made with that
// token does not start a workflow run — so there is never a `CI on main` to wait
// for. The run that DOES exist is the promote workflow itself, and it ran on
// `dev`. A hook configured only for CI-on-main declines it with a tidy reason and
// the box stays on whatever commit was last pushed by hand, indefinitely.
func TestPromotionDeploys(t *testing.T) {
	target := &Target{
		Repo: "monstercameron/ArticleFlux",
		Triggers: []Trigger{
			{Workflow: "CI", Branch: "main"},
			{Workflow: "Promote dev to main", Branch: "dev"},
		},
	}

	promote := func() *hookPayload {
		var p hookPayload
		p.Action = "completed"
		p.WorkflowRun.Name = "Promote dev to main"
		p.WorkflowRun.Conclusion = "success"
		p.WorkflowRun.HeadBranch = "dev" // the ref it was dispatched from
		p.WorkflowRun.Event = "workflow_dispatch"
		return &p
	}

	if reason := shouldSkip(promote(), target); reason != "" {
		t.Fatalf("a successful promotion must deploy, got %q", reason)
	}

	// The other trigger still works: a hand-pushed main going green deploys too.
	var ci hookPayload
	ci.Action = "completed"
	ci.WorkflowRun.Name = "CI"
	ci.WorkflowRun.Conclusion = "success"
	ci.WorkflowRun.HeadBranch = "main"
	ci.WorkflowRun.Event = "push"
	if reason := shouldSkip(&ci, target); reason != "" {
		t.Fatalf("green CI on main must still deploy, got %q", reason)
	}

	// Triggers are pairs, not a pool of allowed workflows crossed with allowed
	// branches. `CI` on `dev` matches neither pair and must not deploy — every
	// push to dev would otherwise ship unpromoted code to the live box.
	for _, tc := range []struct {
		name   string
		mutate func(*hookPayload)
	}{
		{"CI on dev", func(p *hookPayload) { p.WorkflowRun.Name = "CI" }},
		{"promote on main", func(p *hookPayload) { p.WorkflowRun.HeadBranch = "main" }},
		{"promote that failed", func(p *hookPayload) { p.WorkflowRun.Conclusion = "failure" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := promote()
			tc.mutate(p)
			if reason := shouldSkip(p, target); reason == "" {
				t.Fatalf("%s should not deploy, but was allowed", tc.name)
			}
		})
	}
}

// The reason a declined event gives has to name what WOULD have matched. The
// outage this replaced was invisible because "branch is \"dev\", not \"main\""
// reads like correct behaviour when it is in fact the whole bug.
func TestSkipReasonNamesTheTriggers(t *testing.T) {
	target := &Target{Repo: "r", Triggers: []Trigger{
		{Workflow: "CI", Branch: "main"},
		{Workflow: "Promote dev to main", Branch: "dev"},
	}}
	var p hookPayload
	p.Action = "completed"
	p.WorkflowRun.Name = "Demo (GitHub Pages)"
	p.WorkflowRun.Conclusion = "success"
	p.WorkflowRun.HeadBranch = "main"
	p.WorkflowRun.Event = "push"

	reason := shouldSkip(&p, target)
	for _, want := range []string{"Demo (GitHub Pages)", "CI", "Promote dev to main", "dev", "main"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason %q does not mention %q", reason, want)
		}
	}
}

// The shorthand has to keep working: every config in existence uses it, and a
// silent behaviour change here is a box that stops deploying.
func TestLoadConfigFoldsShorthandIntoTriggers(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.json"
	cmd := absCommand(t)
	if err := writeFile(path, `{"targets":[{"repo":"a/b","workflow":"CI","branch":"main","secret":"s","command":"`+cmd+`"}]}`); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Targets[0].Triggers
	if len(got) != 1 || got[0].Workflow != "CI" || got[0].Branch != "main" {
		t.Fatalf("shorthand did not fold into one CI/main trigger: %+v", got)
	}
}

// findTarget returns the first target matching a repo, so a second one for the
// same repository is dead configuration that looks alive. It must not start.
func TestLoadConfigRejectsDuplicateRepo(t *testing.T) {
	path := t.TempDir() + "/config.json"
	cmd := absCommand(t)
	err := writeFile(path, `{"targets":[
		{"repo":"a/b","workflow":"CI","branch":"main","secret":"s","command":"`+cmd+`"},
		{"repo":"a/b","workflow":"Promote","branch":"dev","secret":"s","command":"`+cmd+`"}
	]}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("two targets for one repo were accepted; the second would never fire")
	}
}

// An empty Workflow means "any workflow", which is a real configuration and a
// dangerous default — the test pins the behaviour so it cannot change silently.
func TestShouldSkipAnyWorkflow(t *testing.T) {
	target := &Target{Repo: "r", Triggers: []Trigger{{Branch: "main"}}}
	var p hookPayload
	p.Action = "completed"
	p.WorkflowRun.Name = "anything at all"
	p.WorkflowRun.Conclusion = "success"
	p.WorkflowRun.HeadBranch = "main"
	p.WorkflowRun.Event = "push"
	if reason := shouldSkip(&p, target); reason != "" {
		t.Fatalf("empty Workflow should match any workflow, got %q", reason)
	}
}

func TestValidSignature(t *testing.T) {
	const secret = "s3cr3t"
	body := []byte(`{"repository":{"full_name":"a/b"}}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !validSignature(body, good, secret) {
		t.Fatal("a correctly signed body was rejected")
	}
	for _, tc := range []struct{ name, header, secret string }{
		{"wrong secret", good, "other"},
		{"no header", "", secret},
		{"missing prefix", hex.EncodeToString(mac.Sum(nil)), secret},
		{"not hex", "sha256=zzzz", secret},
		// The one that matters most: an unsigned request must never be treated as
		// authentic just because the body parses.
		{"empty signature", "sha256=", secret},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if validSignature(body, tc.header, tc.secret) {
				t.Fatalf("%s was accepted", tc.name)
			}
		})
	}

	// A body altered after signing must fail, or the signature proves nothing
	// about what is being acted on.
	if validSignature([]byte(`{"repository":{"full_name":"evil/repo"}}`), good, secret) {
		t.Fatal("a tampered body passed verification")
	}
}

// A target without a secret would be an unauthenticated deploy trigger, and one
// with a relative command path is a PATH-dependent surprise. Both must stop the
// process from starting rather than be discovered later.
func TestLoadConfigRejectsUnsafeTargets(t *testing.T) {
	for _, tc := range []struct{ name, json string }{
		{"no secret", `{"targets":[{"repo":"a/b","branch":"main","command":"/x/y.sh"}]}`},
		{"relative command", `{"targets":[{"repo":"a/b","branch":"main","secret":"s","command":"y.sh"}]}`},
		{"no branch", `{"targets":[{"repo":"a/b","secret":"s","command":"/x/y.sh"}]}`},
		{"no repo", `{"targets":[{"branch":"main","secret":"s","command":"/x/y.sh"}]}`},
		{"no targets", `{"targets":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := t.TempDir() + "/config.json"
			if err := writeFile(path, tc.json); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(path); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
		})
	}
}

// writeFile is a tiny helper so the table above stays readable.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

// absCommand returns an absolute deploy-command path that satisfies
// filepath.IsAbs on the host running the test, JSON-safe.
//
// The service only ever runs on Linux, but CI runs `go test` on Windows too,
// where "/x/y.sh" is NOT absolute — a hardcoded POSIX path makes these tests
// fail for a reason that has nothing to do with what they check. Forward slashes
// after the drive letter are absolute on both, and need no escaping in JSON.
func absCommand(t *testing.T) string {
	t.Helper()
	return filepath.ToSlash(t.TempDir()) + "/y.sh"
}
