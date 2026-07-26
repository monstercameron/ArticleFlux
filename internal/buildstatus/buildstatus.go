// Package buildstatus reads TODO.md and reports how much of the build exists.
//
// It parses the checklist rather than keeping its own copy of it. A hand-written
// list of "what's done" on a status page is a list that goes stale in about a
// week and then confidently reports fiction — which is the one thing this page
// must not do. TODO.md is the tracker; this reads it.
//
// Scope note: this is dev instrumentation for the boot page, not a product
// feature. It disappears the moment the real client lands (TODO 8.1).
package buildstatus

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// Tier is one "## Tier N — Name" section of TODO.md and its checkboxes.
type Tier struct {
	ID    string // "tier-0" — the key handed to design.HueFor
	Label string // "Tier 0"
	Name  string // "Unblock"
	Done  int
	Total int
}

// Complete reports whether every checkbox in the tier is ticked. A tier with no
// checkboxes at all is not complete — it is unstarted, and saying otherwise would
// make an empty section look finished.
func (t Tier) Complete() bool { return t.Total > 0 && t.Done == t.Total }

// Gate is one of the five G-gates. Gates are tracked separately from tiers
// because they are the points where the plan admits it cannot know something
// without running it — the honest part of a waterfall.
type Gate struct {
	ID       string
	Question string
	Passed   bool
}

// Status is the whole picture.
type Status struct {
	Tiers []Tier
	Gates []Gate
	// Err is set when TODO.md could not be read. The page renders the failure
	// instead of an empty list, because an empty list looks like "nothing is
	// built" rather than "I could not tell".
	Err error
}

// Pending counts tiers that are not finished. This is the number the boot page
// shows as an unread count.
func (s Status) Pending() int {
	n := 0
	for _, t := range s.Tiers {
		if !t.Complete() {
			n++
		}
	}
	return n
}

var (
	reTier  = regexp.MustCompile(`^##\s+Tier\s+(\d+)\s*[—-]\s*(.+?)\s*$`)
	reCheck = regexp.MustCompile(`^\s*-\s*\[( |x|X)\]`)
	reGate  = regexp.MustCompile(`^\|\s*\*\*(G\d)\*\*\s*\|[^|]*\|\s*([^|]+?)\s*\|`)
	reGateD = regexp.MustCompile(`\*\*(G\d)\b`)
)

// Read parses TODO.md at path.
//
// Two passes, because the gate table appears before the checklist that records
// whether a gate actually ran: pass one collects tiers and the gate roster, pass
// two marks a gate passed if a ticked checkbox anywhere in the file names it.
func Read(path string) Status {
	var s Status

	f, err := os.Open(path)
	if err != nil {
		s.Err = err
		return s
	}
	defer f.Close()

	passed := map[string]bool{}
	cur := -1

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()

		if m := reTier.FindStringSubmatch(line); m != nil {
			s.Tiers = append(s.Tiers, Tier{
				ID:    "tier-" + m[1],
				Label: "Tier " + m[1],
				Name:  m[2],
			})
			cur = len(s.Tiers) - 1
			continue
		}

		// The gate roster lives in a table above the first tier heading.
		if m := reGate.FindStringSubmatch(line); m != nil {
			s.Gates = append(s.Gates, Gate{ID: m[1], Question: m[2]})
			continue
		}

		if m := reCheck.FindStringSubmatch(line); m != nil {
			ticked := m[1] != " "
			if cur >= 0 {
				s.Tiers[cur].Total++
				if ticked {
					s.Tiers[cur].Done++
				}
			}
			if ticked {
				// "- [x] **G1 · D2 · FTS5 spike.**" marks G1 passed.
				for _, g := range reGateD.FindAllStringSubmatch(line, -1) {
					passed[g[1]] = true
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		s.Err = err
		return s
	}

	for i := range s.Gates {
		s.Gates[i].Passed = passed[s.Gates[i].ID]
	}
	return s
}

// FindTODO locates TODO.md by walking up from the working directory, so the
// server finds it whether it was started from the repo root or from bin/.
func FindTODO() string {
	dir, err := os.Getwd()
	if err != nil {
		return "TODO.md"
	}
	for i := 0; i < 5; i++ {
		p := dir + string(os.PathSeparator) + "TODO.md"
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := parentDir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "TODO.md"
}

func parentDir(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i > 0 {
		return p[:i]
	}
	return p
}
