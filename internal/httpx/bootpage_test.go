package httpx

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/buildstatus"
)

func TestInlineMD(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Does `ncruces/go-sqlite3` FTS5 work in *our* build?",
			"Does <code>ncruces/go-sqlite3</code> FTS5 work in <em>our</em> build?"},
		{"plain text", "plain text"},
		// An unpaired marker is literal text, not a broken tag.
		{"5 * 3 items", "5 * 3 items"},
		{"a `b", "a `b"},
		// The one that matters: markup must not be a route to injecting a tag.
		{"`<script>x</script>`", "<code>&lt;script&gt;x&lt;/script&gt;</code>"},
		{"<b>bold</b>", "&lt;b&gt;bold&lt;/b&gt;"},
		{"*<img src=x>*", "<em>&lt;img src=x&gt;</em>"},
	}
	for _, c := range cases {
		if got := inlineMD(c.in); got != c.want {
			t.Errorf("inlineMD(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// TestBootPageRenders is a smoke test with teeth: it asserts the page reports the
// status it was given, rather than that it returned 200. A status page that
// renders beautifully and says the wrong thing has failed at its only job.
func TestBootPageRenders(t *testing.T) {
	dir := t.TempDir()
	todo := filepath.Join(dir, "TODO.md")
	md := "" +
		"| **G1** | Tier 0 | Does FTS5 work? | D2 |\n" +
		"| **G9** | Tier 4 | Something unrun? | D9 |\n" +
		"\n## Tier 0 — Unblock\n\n" +
		"- [x] **G1** done\n" +
		"- [x] also done\n" +
		"\n## Tier 1 — Repo skeleton\n\n" +
		"- [x] one\n- [ ] two\n"
	if err := os.WriteFile(todo, []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	p := &BootPage{
		Build:    Build{Version: "articleflux test", Commit: "abc123", Addr: "127.0.0.1:9000"},
		TODOPath: todo,
	}
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()

	for _, want := range []string{
		`class="row read"`,   // Tier 0 is complete
		`class="row unread"`, // Tier 1 is not
		`class="gate passed"`,
		`class="gate open"`,
		"<style", // A26: the sheet is inlined, not linked to a .css file
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// Exactly one tier is unfinished, so the header count must say 1.
	if !strings.Contains(body, `<b>1</b> tiers to go`) {
		t.Error("header count is wrong")
	}
	// No application JS on this page — the poll is a meta refresh.
	if strings.Contains(body, "<script") {
		t.Error("A26 violated: the boot page contains a <script>")
	}
}

// TestBootPageReportsAMissingTODO pins the failure path. An unreadable TODO.md
// must render as a stated problem, not as an empty list — an empty list reads as
// "nothing is built" when the truth is "I could not tell".
func TestBootPageReportsAMissingTODO(t *testing.T) {
	p := &BootPage{TODOPath: filepath.Join(t.TempDir(), "absent.md")}
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	body := rr.Body.String()
	if !strings.Contains(body, "Couldn't read") {
		t.Error("a missing TODO.md should be reported, not swallowed")
	}
	if strings.Contains(body, "tiers to go") {
		t.Error("no count should be shown when the status could not be read")
	}
}

func TestStatusParsesRealTODO(t *testing.T) {
	// The real file, so a format change in TODO.md fails here rather than
	// silently emptying the page.
	st := buildstatus.Read("../../TODO.md")
	if st.Err != nil {
		t.Fatalf("read: %v", st.Err)
	}
	if len(st.Tiers) < 9 {
		t.Errorf("parsed %d tiers, expected the full set", len(st.Tiers))
	}
	if len(st.Gates) != 6 {
		// Tier 11 (FluxCast, TODO.md §"Tier 11") added G6 — the FluxCast model-id
		// decision — alongside the existing G1-G5. This count tracks TODO.md's
		// real gate table rather than a number frozen at whatever tier last
		// touched this test; the next tier that adds a gate updates this too.
		t.Errorf("parsed %d gates, expected G1-G6", len(st.Gates))
	}
	var g1 bool
	for _, g := range st.Gates {
		if g.ID == "G1" {
			g1 = g.Passed
		}
	}
	if !g1 {
		t.Error("G1 passed on 2026-07-26 and the page should say so")
	}
}
