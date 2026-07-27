// Package rules evaluates a user's filter rules against an item (TODO 4.9,
// plan.md §13).
//
// # Pure, and that is the whole design
//
// `(item, []Rule) → []Action`. No database, no clock, no side effects, no
// logging. The caller decides what to do with the actions and the caller owns
// the transaction.
//
// The reason is §13.4: a rule you cannot dry-run is a rule you are afraid to
// write, and the preview must be *the same code* as the apply. If evaluation
// touched storage, the preview would have to be a second implementation that
// promises to behave identically — and the first time they diverge, the preview
// lies about what the rule will do, which is worse than having no preview at all.
//
// # Where this runs, and where it must not
//
// §13.2: rules are per-user and items are global (A14), so evaluation happens
// **once per subscriber**, never once at ingest. Evaluating at ingest would let
// one user's mute filter hide an item from every other subscriber — a silent
// cross-user bug that looks like a missing article.
//
// And not inline with the poll: fan-out is O(items × subscribers), so a
// popular source returning 50 items to 200 subscribers is 10,000 evaluations
// standing between the fetcher and the next feed. 6.7 runs this from a queue.
package rules

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Field names a property of an item that a condition can test.
type Field string

const (
	FieldTitle     Field = "title"
	FieldAuthor    Field = "author"
	FieldContent   Field = "content"
	FieldURL       Field = "url"
	FieldSource    Field = "source"
	FieldFolder    Field = "folder"
	FieldTag       Field = "tag"
	FieldWordCount Field = "word_count"
	FieldAge       Field = "age"
	FieldLang      Field = "lang"
)

// Op is a comparison.
type Op string

const (
	OpContains    Op = "contains"
	OpNotContains Op = "not_contains"
	OpEquals      Op = "equals"
	OpStartsWith  Op = "starts_with"
	OpRegex       Op = "regex"
	OpGT          Op = "gt"
	OpLT          Op = "lt"
)

// ActionKind is what a matching rule does.
type ActionKind string

const (
	ActionMarkRead       ActionKind = "mark_read"
	ActionStar           ActionKind = "star"
	ActionTag            ActionKind = "tag"
	ActionMute           ActionKind = "mute"
	ActionSetHomeWeight  ActionKind = "set_home_weight"
	ActionMoveToFolder   ActionKind = "move_to_folder"
	ActionStopProcessing ActionKind = "stop_processing"
	// Schema-reserved and UI-gated until NotifyService (M12) and WebhookService
	// (M22) exist. They evaluate here so a rule authored against a future
	// version does not fail to load; 6.7 drops them until the services are real.
	ActionNotify  ActionKind = "notify"
	ActionWebhook ActionKind = "webhook"
)

// Condition is one test.
type Condition struct {
	Field Field  `json:"field"`
	Op    Op     `json:"op"`
	Value string `json:"value"`
}

// Match is a rule's condition set. All conditions must hold unless Any is set.
type Match struct {
	Any        bool        `json:"any,omitempty"`
	Conditions []Condition `json:"conditions"`
}

// Action is one effect.
type Action struct {
	Kind  ActionKind `json:"kind"`
	Value string     `json:"value,omitempty"`
	// RuleID is filled in by Evaluate so the caller can write rule_hits and,
	// later, undo exactly what one rule did.
	RuleID string `json:"-"`
}

// Rule is one user-authored filter.
type Rule struct {
	ID             string
	Name           string
	Enabled        bool
	Position       int
	Match          Match
	Actions        []Action
	StopProcessing bool
}

// Item is the subset of an article a rule can see.
//
// A struct rather than an interface because rules are data-driven and the field
// set is closed by §13.1 — an interface would invite a caller to compute a field
// lazily, and a rule engine that can run arbitrary caller code is no longer
// pure.
type Item struct {
	Title       string
	Author      string
	Content     string
	URL         string
	SourceTitle string
	FolderName  string
	Tags        []string
	WordCount   int
	PublishedAt time.Time
	Lang        string
}

// Hit records that one rule matched, for rule_hits and the §13.4 preview.
type Hit struct {
	RuleID  string
	Name    string
	Actions []Action
}

// Result is the outcome of evaluating a rule set against one item.
type Result struct {
	// Actions is the merged, de-conflicted action list, in rule order.
	Actions []Action
	// Hits records every rule that matched, which is what rule_hits stores and
	// what the preview screen shows.
	Hits []Hit
	// Stopped names the rule that ended evaluation, if one did.
	Stopped string
}

// Muted reports whether the result mutes the item.
//
// §13.3: mute is not delete. The item is stored and flagged; lists and counts
// exclude it. Unmuting has to work, an over-broad rule has to be recoverable,
// and a MUTED view has to be able to show what a rule is actually eating.
func (r Result) Muted() bool {
	for _, a := range r.Actions {
		if a.Kind == ActionMute {
			return true
		}
	}
	return false
}

// Evaluate runs the rule set against one item.
//
// `now` is a parameter rather than a call to time.Now, because FieldAge is
// relative and a pure function may not read a clock — and because the §13.4
// preview must be able to ask "what would this rule have done last Tuesday".
func Evaluate(item Item, ruleSet []Rule, now time.Time) Result {
	// Sorted by position, then id. Order is user-visible and load-bearing:
	// stop_processing only means anything if the order is stable, and insertion
	// order is invisible in the UI and unstable across a restore.
	//
	// The copy and the sort are skipped when the caller already handed them over
	// in that order, which in this application is always: store.RulesFor selects
	// `ORDER BY position ASC, id ASC`, the same order as below. Evaluate is
	// called once per ITEM, and fanout hands it the same slice for every item of
	// every poll — so the version without this check copied and re-sorted an
	// identical rule set sixty times for a twenty-item poll across three
	// subscribers, and threw away sixty identical results.
	//
	// The check is a linear scan against the same comparator, so an unsorted
	// caller pays one extra pass and is otherwise unaffected. Skipping is exactly
	// equivalent rather than approximately so: a stable sort of an
	// already-sorted slice is the identity, and nothing below writes through
	// `ordered` — the action loop mutates its own copy of each Action, not the
	// rule's.
	ordered := ruleSet
	if !sort.SliceIsSorted(ordered, func(i, j int) bool { return ruleBefore(ordered[i], ordered[j]) }) {
		ordered = make([]Rule, len(ruleSet))
		copy(ordered, ruleSet)
		sort.SliceStable(ordered, func(i, j int) bool { return ruleBefore(ordered[i], ordered[j]) })
	}

	var out Result
	for _, rule := range ordered {
		if !rule.Enabled {
			continue
		}
		if !rule.Match.matches(item, now) {
			continue
		}

		acts := make([]Action, 0, len(rule.Actions))
		for _, a := range rule.Actions {
			a.RuleID = rule.ID
			acts = append(acts, a)
		}
		out.Hits = append(out.Hits, Hit{RuleID: rule.ID, Name: rule.Name, Actions: acts})
		out.Actions = append(out.Actions, acts...)

		// stop_processing is honoured as a rule flag AND as an action, because
		// §13.1 lists it in both places and a rule set that sets one but not the
		// other should behave the way the author obviously meant.
		if rule.StopProcessing || hasStop(rule.Actions) {
			out.Stopped = rule.ID
			break
		}
	}

	out.Actions = dedupe(out.Actions)
	return out
}

// ruleBefore is the evaluation order, in one place.
//
// One function rather than two identical closures, because the sortedness check
// and the sort MUST agree: a check that is stricter than the sort would do the
// work anyway, and one that is looser would skip a sort the order needed.
func ruleBefore(a, b Rule) bool {
	if a.Position != b.Position {
		return a.Position < b.Position
	}
	return a.ID < b.ID
}

func hasStop(actions []Action) bool {
	for _, a := range actions {
		if a.Kind == ActionStopProcessing {
			return true
		}
	}
	return false
}

// dedupe collapses repeated actions, keeping the FIRST occurrence.
//
// First rather than last, and this is a real decision: rules are ordered, and
// the ordering exists so that an earlier rule wins. If a later rule could
// overwrite `set_home_weight`, moving a rule up the list would stop changing its
// precedence, which is the one thing the ordering is for.
//
// Actions that carry distinct values — tag, move_to_folder — dedupe on the pair,
// so two rules adding two different tags both apply.
func dedupe(actions []Action) []Action {
	seen := map[string]bool{}
	out := actions[:0:0]
	for _, a := range actions {
		key := string(a.Kind)
		switch a.Kind {
		case ActionTag, ActionMoveToFolder, ActionWebhook:
			key += "\x00" + a.Value
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a)
	}
	return out
}

func (m Match) matches(item Item, now time.Time) bool {
	if len(m.Conditions) == 0 {
		// A rule with no conditions matches nothing, rather than everything.
		//
		// Everything is the more "logical" reading of an empty AND, and it is the
		// wrong default here: the way an empty condition set comes into
		// existence is a half-finished rule or a bad import, and the cost of
		// guessing wrong is a rule that silently mutes the entire feed.
		return false
	}
	for _, c := range m.Conditions {
		ok := c.holds(item, now)
		if m.Any && ok {
			return true
		}
		if !m.Any && !ok {
			return false
		}
	}
	return !m.Any
}

func (c Condition) holds(item Item, now time.Time) bool {
	switch c.Field {
	case FieldWordCount:
		return compareNumbers(float64(item.WordCount), c)
	case FieldAge:
		// Age in hours, because that is the unit a person writing "older than 48"
		// means. A zero PublishedAt would otherwise read as an age of two
		// thousand years and match every "older than" rule ever written.
		if item.PublishedAt.IsZero() {
			return false
		}
		return compareNumbers(now.Sub(item.PublishedAt).Hours(), c)
	case FieldTag:
		return matchesAny(item.Tags, c)
	default:
		return compareStrings(c.subject(item), c)
	}
}

func (c Condition) subject(item Item) string {
	switch c.Field {
	case FieldTitle:
		return item.Title
	case FieldAuthor:
		return item.Author
	case FieldContent:
		return item.Content
	case FieldURL:
		return item.URL
	case FieldSource:
		return item.SourceTitle
	case FieldFolder:
		return item.FolderName
	case FieldLang:
		return item.Lang
	}
	return ""
}

// matchesAny applies a string comparison across a list, for the tag field.
//
// not_contains is inverted deliberately: "not_contains rust" over a tag list
// must mean "none of the tags is rust", not "at least one tag is not rust" —
// the second is true of almost every tagged item and would make the operator
// useless.
func matchesAny(values []string, c Condition) bool {
	if c.Op == OpNotContains {
		for _, v := range values {
			if compareStrings(v, Condition{Field: c.Field, Op: OpContains, Value: c.Value}) {
				return false
			}
		}
		return true
	}
	for _, v := range values {
		if compareStrings(v, c) {
			return true
		}
	}
	return false
}

// compareStrings applies a text operator, case-insensitively.
//
// Case-insensitive throughout, with no option to change it. A person writing a
// rule for "Rust" means the language whether or not a headline shouts it, and
// every case-sensitive filter UI in existence generates support questions rather
// than precision. Someone who genuinely needs case can use a regex.
func compareStrings(subject string, c Condition) bool {
	s := strings.ToLower(subject)
	v := strings.ToLower(strings.TrimSpace(c.Value))

	switch c.Op {
	case OpContains:
		return strings.Contains(s, v)
	case OpNotContains:
		return !strings.Contains(s, v)
	case OpEquals:
		return s == v
	case OpStartsWith:
		return strings.HasPrefix(s, v)
	case OpRegex:
		re, err := compile(c.Value)
		if err != nil {
			// An invalid regex matches nothing. It cannot return an error from
			// here without making every caller handle it per item, and the
			// alternative — matching everything — turns a typo into a rule that
			// mutes the whole feed. Validate() is where an author is told.
			return false
		}
		return re.MatchString(subject)
	case OpGT, OpLT:
		// Numeric operators on a text field: compare lexically rather than
		// refusing. "title gt m" is a strange rule but it is a coherent one, and
		// silently never matching would be harder to debug than an odd result.
		return compareLex(s, v, c.Op)
	}
	return false
}

func compareLex(a, b string, op Op) bool {
	if op == OpGT {
		return a > b
	}
	return a < b
}

func compareNumbers(subject float64, c Condition) bool {
	want, err := strconv.ParseFloat(strings.TrimSpace(c.Value), 64)
	if err != nil {
		return false
	}
	switch c.Op {
	case OpGT:
		return subject > want
	case OpLT:
		return subject < want
	case OpEquals:
		return subject == want
	case OpContains, OpStartsWith:
		return strings.Contains(strconv.FormatFloat(subject, 'f', -1, 64), c.Value)
	case OpNotContains:
		return !strings.Contains(strconv.FormatFloat(subject, 'f', -1, 64), c.Value)
	}
	return false
}

// regexCache avoids recompiling a rule's pattern once per item. Fan-out
// evaluates the same rule set against every new item for every subscriber, so a
// single-feed poll can hit the same pattern thousands of times.
//
// Unbounded because the key space is bounded by the number of rules a person has
// written, which is small and does not grow with traffic. A cache keyed by
// anything item-derived would need eviction; this one does not.
var regexCache = newCache()

func compile(pattern string) (*regexp.Regexp, error) {
	if re, ok := regexCache.get(pattern); ok {
		return re, nil
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, err
	}
	regexCache.put(pattern, re)
	return re, nil
}

// Validate reports why a rule cannot be saved.
//
// Separate from Evaluate because the two have opposite obligations: evaluation
// must never fail on one bad item out of a thousand, and authoring must fail
// loudly on the one bad rule. Returning errors from Evaluate would push that
// choice onto every caller, and they would all choose "ignore".
func Validate(r Rule) error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("rules: a rule needs a name")
	}
	if len(r.Match.Conditions) == 0 {
		return fmt.Errorf("rules: %q has no conditions, so it would never match", r.Name)
	}
	if len(r.Actions) == 0 {
		return fmt.Errorf("rules: %q has no actions, so it would do nothing", r.Name)
	}
	for i, c := range r.Match.Conditions {
		if !knownField(c.Field) {
			return fmt.Errorf("rules: %q condition %d: unknown field %q", r.Name, i+1, c.Field)
		}
		if !knownOp(c.Op) {
			return fmt.Errorf("rules: %q condition %d: unknown operator %q", r.Name, i+1, c.Op)
		}
		if c.Op == OpRegex {
			if _, err := regexp.Compile(c.Value); err != nil {
				return fmt.Errorf("rules: %q condition %d: %w", r.Name, i+1, err)
			}
		}
		if c.Field == FieldWordCount || c.Field == FieldAge {
			if _, err := strconv.ParseFloat(strings.TrimSpace(c.Value), 64); err != nil {
				return fmt.Errorf("rules: %q condition %d: %s needs a number, got %q",
					r.Name, i+1, c.Field, c.Value)
			}
		}
	}
	for i, a := range r.Actions {
		if !knownAction(a.Kind) {
			return fmt.Errorf("rules: %q action %d: unknown action %q", r.Name, i+1, a.Kind)
		}
		switch a.Kind {
		case ActionTag, ActionMoveToFolder:
			if strings.TrimSpace(a.Value) == "" {
				return fmt.Errorf("rules: %q action %d: %s needs a value", r.Name, i+1, a.Kind)
			}
		case ActionSetHomeWeight:
			if _, err := strconv.ParseFloat(strings.TrimSpace(a.Value), 64); err != nil {
				return fmt.Errorf("rules: %q action %d: set_home_weight needs a number, got %q",
					r.Name, i+1, a.Value)
			}
		}
	}
	return nil
}

func knownField(f Field) bool {
	switch f {
	case FieldTitle, FieldAuthor, FieldContent, FieldURL, FieldSource,
		FieldFolder, FieldTag, FieldWordCount, FieldAge, FieldLang:
		return true
	}
	return false
}

func knownOp(o Op) bool {
	switch o {
	case OpContains, OpNotContains, OpEquals, OpStartsWith, OpRegex, OpGT, OpLT:
		return true
	}
	return false
}

func knownAction(a ActionKind) bool {
	switch a {
	case ActionMarkRead, ActionStar, ActionTag, ActionMute, ActionSetHomeWeight,
		ActionMoveToFolder, ActionStopProcessing, ActionNotify, ActionWebhook:
		return true
	}
	return false
}
