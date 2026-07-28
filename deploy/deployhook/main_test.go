package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

// The gate is the reason this program exists, so the cases that must NOT deploy
// are tested individually. Each one is a distinct way a naive "deploy on
// workflow_run" would ship something nobody approved.
func TestShouldSkip(t *testing.T) {
	target := &Target{Repo: "monstercameron/ArticleFlux", Workflow: "CI", Branch: "main"}

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

// An empty Workflow means "any workflow", which is a real configuration and a
// dangerous default — the test pins the behaviour so it cannot change silently.
func TestShouldSkipAnyWorkflow(t *testing.T) {
	target := &Target{Repo: "r", Branch: "main"}
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
