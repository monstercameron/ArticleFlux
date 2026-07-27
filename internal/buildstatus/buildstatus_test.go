package buildstatus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "TODO.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// --- graceful degradation ----------------------------------------------------

func TestRead_MissingFile(t *testing.T) {
	s := Read(filepath.Join(t.TempDir(), "does-not-exist.md"))
	if s.Err == nil {
		t.Fatal("Read() of a missing file: Err = nil, want a read error")
	}
	if len(s.Tiers) != 0 || len(s.Gates) != 0 {
		t.Errorf("Read() of a missing file returned data: %+v", s)
	}
	if s.Pending() != 0 {
		t.Errorf("Pending() = %d on an errored Status, want 0 (callers must check Err first)", s.Pending())
	}
}

func TestRead_EmptyFile(t *testing.T) {
	s := Read(writeTemp(t, ""))
	if s.Err != nil {
		t.Fatalf("Read() of an empty file: Err = %v, want nil", s.Err)
	}
	if len(s.Tiers) != 0 || len(s.Gates) != 0 {
		t.Errorf("Read() of an empty file returned data: %+v", s)
	}
}

func TestRead_NoCheckboxesTierIsNotComplete(t *testing.T) {
	content := "## Tier 0 — Unblock\nSome prose, no checkboxes at all.\n"
	s := Read(writeTemp(t, content))
	if s.Err != nil {
		t.Fatalf("Read() err = %v", s.Err)
	}
	if len(s.Tiers) != 1 {
		t.Fatalf("Tiers = %+v, want exactly 1", s.Tiers)
	}
	tier := s.Tiers[0]
	if tier.Total != 0 || tier.Done != 0 {
		t.Fatalf("tier = %+v, want Total=0 Done=0", tier)
	}
	// The documented rule: a tier with no checkboxes is unstarted, not
	// complete. Total==0 && Done==0 must NOT satisfy Complete().
	if tier.Complete() {
		t.Error("Tier.Complete() = true for a tier with zero checkboxes; an empty section must read as unstarted, not finished")
	}
	if s.Pending() != 1 {
		t.Errorf("Pending() = %d, want 1 (the checkbox-less tier counts as pending)", s.Pending())
	}
}

func TestTierComplete(t *testing.T) {
	cases := []struct {
		tier Tier
		want bool
	}{
		{Tier{Total: 0, Done: 0}, false},
		{Tier{Total: 3, Done: 0}, false},
		{Tier{Total: 3, Done: 2}, false},
		{Tier{Total: 3, Done: 3}, true},
	}
	for _, c := range cases {
		if got := c.tier.Complete(); got != c.want {
			t.Errorf("Tier%+v.Complete() = %v, want %v", c.tier, got, c.want)
		}
	}
}

func TestStatusPending(t *testing.T) {
	s := Status{Tiers: []Tier{
		{Total: 3, Done: 3}, // complete
		{Total: 3, Done: 1}, // pending
		{Total: 0, Done: 0}, // unstarted -> pending
	}}
	if got := s.Pending(); got != 2 {
		t.Errorf("Pending() = %d, want 2", got)
	}
}

// --- checklist / heading parsing ---------------------------------------------

func TestRead_BasicTierAndCheckboxCounting(t *testing.T) {
	content := `## Tier 0 — Unblock
- [x] done one
- [X] done two (capital X)
- [ ] not done

## Tier 1 — Repo skeleton
- [x] only item
`
	s := Read(writeTemp(t, content))
	if s.Err != nil {
		t.Fatalf("Read() err = %v", s.Err)
	}
	if len(s.Tiers) != 2 {
		t.Fatalf("Tiers = %+v, want 2", s.Tiers)
	}
	t0, t1 := s.Tiers[0], s.Tiers[1]
	if t0.ID != "tier-0" || t0.Label != "Tier 0" || t0.Name != "Unblock" {
		t.Errorf("tier 0 identity = %+v", t0)
	}
	if t0.Total != 3 || t0.Done != 2 {
		t.Errorf("tier 0 counts = %+v, want Total=3 Done=2", t0)
	}
	if !t1.Complete() {
		t.Errorf("tier 1 = %+v, want Complete()==true", t1)
	}
}

func TestRead_MalformedHeadingsAreIgnoredNotCrashed(t *testing.T) {
	content := `### Tier 1 — too many hashes, not a tier heading
## Tier — missing the number
##Tier 2 — missing the space after ##
- [x] orphan checkbox before any recognised tier heading
## Tier 3 — a real one
- [x] real item
`
	s := Read(writeTemp(t, content))
	if s.Err != nil {
		t.Fatalf("Read() err = %v", s.Err)
	}
	if len(s.Tiers) != 1 {
		t.Fatalf("Tiers = %+v, want exactly 1 (only the well-formed heading)", s.Tiers)
	}
	if s.Tiers[0].Label != "Tier 3" {
		t.Errorf("Tiers[0] = %+v, want Tier 3", s.Tiers[0])
	}
	// The orphan checkbox (before any recognised heading, cur == -1) must be
	// dropped, not attributed to tier 3 or panic on a negative index.
	if s.Tiers[0].Total != 1 {
		t.Errorf("Tier 3 Total = %d, want 1 (only its own checkbox, not the orphan)", s.Tiers[0].Total)
	}
}

// TestRead_NonNumericTierSuffixMergesIntoPrecedingTier pins a real parsing
// gap in reTier (buildstatus.go:65):
//
//	reTier = regexp.MustCompile(`^##\s+Tier\s+(\d+)\s*[—-]\s*(.+?)\s*$`)
//
// (\d+) only matches a pure integer, so a heading like "## Tier 8b — ..." —
// which exists verbatim in this repo's own TODO.md ("Tier 8b — shipped, but
// never planned") — does NOT match reTier at all. Read() does not treat an
// unrecognised heading as a section boundary; `cur` (the current tier index)
// simply stays pointed at whatever tier heading matched last. The result is
// that every checkbox under "Tier 8b" is silently counted into "Tier 8"'s
// Done/Total, and "Tier 8b" never appears in s.Tiers as its own entry.
//
// This directly contradicts the package's own claim that the status page
// "cannot rot into a lie": the numbers for Tier 8 are inflated with
// unrelated items, and an entire section vanishes from the list, while the
// page renders looking completely normal (no Err, a plausible-looking tier
// list). This is pinned as CURRENT behaviour, not desired behaviour — flagged
// to the coordinator as a real product bug, not fixed here.
func TestRead_NonNumericTierSuffixMergesIntoPrecedingTier(t *testing.T) {
	content := `## Tier 8 — Client
- [x] built the client
- [ ] not yet

## Tier 8b — shipped, but never planned
- [x] extra thing
- [x] another extra thing
- [ ] pending extra

## Tier 9 — Systems
- [x] something
`
	s := Read(writeTemp(t, content))
	if s.Err != nil {
		t.Fatalf("Read() err = %v", s.Err)
	}

	var tier8, tier9 *Tier
	for i := range s.Tiers {
		switch s.Tiers[i].Label {
		case "Tier 8":
			tier8 = &s.Tiers[i]
		case "Tier 9":
			tier9 = &s.Tiers[i]
		}
	}
	if tier8 == nil || tier9 == nil {
		t.Fatalf("Tiers = %+v, want to find both 'Tier 8' and 'Tier 9'", s.Tiers)
	}

	// BUG, pinned as-is: "Tier 8b" is entirely absent from s.Tiers.
	for _, tier := range s.Tiers {
		if tier.Label == "Tier 8b" {
			t.Fatalf("BUG APPEARS FIXED: 'Tier 8b' now appears as its own tier (%+v) — "+
				"reTier must have been extended to accept alphanumeric tier suffixes; "+
				"this test's expectations (and the merged-into-Tier-8 assertions below) "+
				"need to be updated to match, not treated as a new failure", tier)
		}
	}

	// BUG, pinned as-is: Tier 8b's 3 checkboxes (2 done, 1 pending) were
	// silently folded into Tier 8's own 2 (1 done, 1 pending), giving
	// Total=5, Done=3 instead of the correct Total=2, Done=1.
	if tier8.Total != 5 || tier8.Done != 3 {
		t.Fatalf("Tier 8 = %+v, want Total=5 Done=3 (documenting the merge bug — "+
			"Tier 8b's checkboxes bleeding into Tier 8, buildstatus.go reTier line 65)", *tier8)
	}

	// Tier 9 must be unaffected by the mis-attributed section that precedes it.
	if tier9.Total != 1 || tier9.Done != 1 {
		t.Errorf("Tier 9 = %+v, want Total=1 Done=1", *tier9)
	}
}

// --- gates --------------------------------------------------------------

func TestRead_GateRosterAndPassedMarking(t *testing.T) {
	content := "| **G1** | Tier 0 | Does the spike work? | D2 |\n" +
		"| **G2** | 3.7 | Can a repo leak across tenants? | T1 |\n" +
		"## Tier 0 — Unblock\n" +
		"- [x] **G1 · D2 · spike.** passed, see notes\n" +
		"- [ ] **G2 · not yet run**\n"
	s := Read(writeTemp(t, content))
	if s.Err != nil {
		t.Fatalf("Read() err = %v", s.Err)
	}
	if len(s.Gates) != 2 {
		t.Fatalf("Gates = %+v, want 2", s.Gates)
	}
	byID := map[string]Gate{}
	for _, g := range s.Gates {
		byID[g.ID] = g
	}
	if !byID["G1"].Passed {
		t.Error("G1 should be Passed: a ticked checkbox names it")
	}
	if byID["G2"].Passed {
		t.Error("G2 should NOT be Passed: its checkbox is unticked")
	}
	if !strings.Contains(byID["G1"].Question, "spike") {
		t.Errorf("G1.Question = %q, want it to contain the roster text", byID["G1"].Question)
	}
}

func TestRead_GateNeverMarkedPassedStaysFalse(t *testing.T) {
	content := "| **G3** | 5.4 | What do the hot queries cost? | R2 |\n## Tier 0 — X\n- [ ] unrelated\n"
	s := Read(writeTemp(t, content))
	if len(s.Gates) != 1 || s.Gates[0].Passed {
		t.Fatalf("Gates = %+v, want one gate, Passed=false", s.Gates)
	}
}

// --- enormous / pathological input -------------------------------------------

func TestRead_LineOverScannerLimitReportsErrInsteadOfCrashing(t *testing.T) {
	// bufio.Scanner here is configured with a 1 MiB max token size
	// (buildstatus.go: sc.Buffer(..., 1024*1024)). A single line longer than
	// that must surface as Status.Err, not a panic and not silent truncation
	// that fabricates a plausible-looking but wrong Status.
	huge := strings.Repeat("x", 2<<20) // 2 MiB, one line, no newline
	s := Read(writeTemp(t, huge))
	if s.Err == nil {
		t.Fatal("Read() of a 2MiB single line: Err = nil, want a scanner error")
	}
}

func TestRead_ManyTiersAndCheckboxes(t *testing.T) {
	var b strings.Builder
	const nTiers = 50
	const perTier = 200
	for i := 0; i < nTiers; i++ {
		fmt.Fprintf(&b, "## Tier %d — Section %d\n", i, i)
		for j := 0; j < perTier; j++ {
			if j%2 == 0 {
				b.WriteString("- [x] done\n")
			} else {
				b.WriteString("- [ ] pending\n")
			}
		}
	}
	s := Read(writeTemp(t, b.String()))
	if s.Err != nil {
		t.Fatalf("Read() err = %v", s.Err)
	}
	if len(s.Tiers) != nTiers {
		t.Fatalf("len(Tiers) = %d, want %d", len(s.Tiers), nTiers)
	}
	for _, tier := range s.Tiers {
		if tier.Total != perTier || tier.Done != perTier/2 {
			t.Fatalf("tier %+v, want Total=%d Done=%d", tier, perTier, perTier/2)
		}
	}
}

// --- FindTODO -----------------------------------------------------------

func TestFindTODO_WalksUpFromWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	todo := filepath.Join(root, "TODO.md")
	if err := os.WriteFile(todo, []byte("## Tier 0 — X\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(deep); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("failed to restore working directory: %v", err)
		}
	}()

	got := FindTODO()
	gotAbs, _ := filepath.Abs(got)
	wantAbs, _ := filepath.Abs(todo)
	if gotAbs != wantAbs {
		t.Errorf("FindTODO() = %q (abs %q), want %q", got, gotAbs, wantAbs)
	}
}

func TestFindTODO_FallsBackWhenNotFound(t *testing.T) {
	// A temp dir nested a few levels deep with no TODO.md anywhere above it
	// within the 5-level search bound (and TempDir roots don't have one).
	deep := filepath.Join(t.TempDir(), "x", "y", "z")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(deep); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("failed to restore working directory: %v", err)
		}
	}()

	if got := FindTODO(); got != "TODO.md" {
		t.Errorf("FindTODO() = %q, want the literal fallback %q", got, "TODO.md")
	}
}
