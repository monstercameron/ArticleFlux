package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// run is the function that actually changes production, and these are the first
// tests it has ever had. What they check is not the deploy script — that is the
// box's business — but everything around it that decides whether a human ever
// finds out what happened:
//
//   - the per-target lock is RELEASED, however the deploy ends. A lock left held
//     means every later green build is silently declined and the pipeline stops
//     with nothing looking broken.
//   - the transcript is written, and says which commit and which outcome.
//   - a timeout KILLS the child rather than leaving it holding the lock.
//
// The child process is this test binary re-invoked (TestHelperProcess), the
// standard way to get a real process with predictable behaviour and no
// dependency on the platform having a shell, `sudo`, or /bin/true — none of
// which Windows has, and Windows is where this suite runs.

// TestHelperProcess is not a real test. It is the program `execCommand` starts
// when a test has redirected it here; GO_HELPER_MODE says how it should behave.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("GO_HELPER_MODE")
	if mode == "" {
		return
	}
	// Proving the deploy's environment reaches the script: run sets
	// AF_DEPLOY_SHA, and a deploy script that cannot see which commit it is
	// deploying is one that can only ever build "latest".
	os.Stdout.WriteString("helper running for sha=" + os.Getenv("AF_DEPLOY_SHA") + "\n")
	switch mode {
	case "ok":
		os.Exit(0)
	case "fail":
		os.Stderr.WriteString("the deploy script failed\n")
		os.Exit(3)
	case "hang":
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
	os.Exit(0)
}

// helperMode points execCommand at TestHelperProcess for the duration of a test.
//
// The mode travels through the PARENT's environment rather than through
// cmd.Env, and that is not a style choice: run assigns `cmd.Env = append(
// os.Environ(), …)` outright, so anything this function put on the command
// would be discarded a few lines later. Which is also the reason the deploy
// script sees AF_DEPLOY_SHA and nothing else a caller might have wanted to add
// — worth knowing before somebody tries to pass a second variable that way.
func helperMode(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("GO_HELPER_MODE", mode)
	saved := execCommand
	execCommand = func(_ string, _ ...string) *exec.Cmd {
		return exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	}
	t.Cleanup(func() { execCommand = saved })
}

// transcript returns the single log file run wrote, which is how a test reads
// the account of what happened without knowing the timestamp in its name.
func transcript(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one deploy log, found %d", len(entries))
	}
	b, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(b)
}

func TestRunRecordsASuccessfulDeploy(t *testing.T) {
	helperMode(t, "ok")
	r := newRunner(t)
	target := &Target{Repo: "monstercameron/ArticleFlux", Command: "/usr/local/bin/deploy.sh"}

	r.run(target, "0123456789abcdef0123456789abcdef01234567", "https://github.com/x/y/runs/1")

	if r.running[target.Repo] {
		t.Error("the per-target lock was not released; every later deploy would be declined")
	}
	last := r.last[target.Repo]
	if !strings.Contains(last, "ok in") {
		t.Errorf("the outcome does not read as a success: %q", last)
	}

	log := transcript(t, r.logDir)
	for _, want := range []string{"deploy monstercameron/ArticleFlux", "0123456789abcdef", "OK in"} {
		if !strings.Contains(log, want) {
			t.Errorf("the transcript is missing %q:\n%s", want, log)
		}
	}
}

// A failed deploy has to be recorded as failed. This is the case the commit
// status exists for, and the case where a silent success is worst: the box is
// serving the old build and GitHub says everything is fine.
func TestRunRecordsAFailedDeploy(t *testing.T) {
	helperMode(t, "fail")
	r := newRunner(t)
	target := &Target{Repo: "x/y", Command: "/usr/local/bin/deploy.sh"}

	r.run(target, "abcdef0123456789", "https://github.com/x/y/runs/2")

	if r.running[target.Repo] {
		t.Error("the lock was not released after a failed deploy")
	}
	if last := r.last[target.Repo]; !strings.Contains(last, "failed") {
		t.Errorf("a failed deploy was recorded as %q", last)
	}

	log := transcript(t, r.logDir)
	if !strings.Contains(log, "FAILED") {
		t.Errorf("the transcript does not record the failure:\n%s", log)
	}
	// The script's own output is the only diagnosis anybody gets.
	if !strings.Contains(log, "the deploy script failed") {
		t.Errorf("the script's output was not captured:\n%s", log)
	}
}

// A build hung past the timeout holds the per-target lock, and a held lock means
// every later green build is silently declined — the pipeline stops without
// anything looking broken. So the timeout must kill the child, not just stop
// waiting for it.
func TestRunKillsADeployThatHangs(t *testing.T) {
	saved := deployTimeout
	deployTimeout = 300 * time.Millisecond
	t.Cleanup(func() { deployTimeout = saved })

	helperMode(t, "hang")
	r := newRunner(t)
	target := &Target{Repo: "x/y", Command: "/usr/local/bin/deploy.sh"}

	done := make(chan struct{})
	go func() { r.run(target, "abcdef0123456789", "url"); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("run did not return after the timeout; the lock would be held forever")
	}

	if r.running[target.Repo] {
		t.Error("a timed-out deploy left the target locked")
	}
	if last := r.last[target.Repo]; !strings.Contains(last, "timed out") {
		t.Errorf("a timed-out deploy was recorded as %q", last)
	}
	if log := transcript(t, r.logDir); !strings.Contains(log, "TIMED OUT") {
		t.Errorf("the transcript does not record the timeout:\n%s", log)
	}
}

// The deploy script is told which commit it is deploying. Without it a script
// can only ever build whatever the remote's tip happens to be at the moment it
// runs, which is not necessarily the commit that passed CI.
func TestRunPassesTheCommitToTheDeployScript(t *testing.T) {
	helperMode(t, "ok")
	r := newRunner(t)
	r.run(&Target{Repo: "x/y", Command: "/deploy.sh"}, "cafebabe12345678", "url")

	if log := transcript(t, r.logDir); !strings.Contains(log, "sha=cafebabe12345678") {
		t.Errorf("AF_DEPLOY_SHA did not reach the script:\n%s", log)
	}
}

// A log directory that does not exist is a misconfiguration, and it must not
// take the lock down with it — the service has to survive to accept the next
// deploy once somebody fixes the path.
func TestRunReleasesTheLockWhenItCannotOpenItsLog(t *testing.T) {
	helperMode(t, "ok")
	r := newRunner(t)
	r.logDir = filepath.Join(r.logDir, "does", "not", "exist")
	target := &Target{Repo: "x/y", Command: "/deploy.sh"}
	r.running[target.Repo] = true

	r.run(target, "abc", "url")

	if r.running[target.Repo] {
		t.Error("an unopenable log left the target locked forever")
	}
}

// start is the half of the pair that decides whether run is called at all.
func TestStartRunsTheDeployAndReleasesTheLock(t *testing.T) {
	helperMode(t, "ok")
	r := newRunner(t)
	target := &Target{Repo: "x/y", Command: "/deploy.sh"}

	if !r.start(target, "abcdef0123456789", "url") {
		t.Fatal("start declined a deploy with nothing running")
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		r.mu.Lock()
		busy := r.running[target.Repo]
		r.mu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the deploy never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if last := r.last[target.Repo]; !strings.Contains(last, "ok in") {
		t.Errorf("start's deploy recorded %q", last)
	}
}
