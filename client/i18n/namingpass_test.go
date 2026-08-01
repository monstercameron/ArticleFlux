package i18n

import (
	"regexp"
	"strings"
	"testing"
)

// Guards for TODO.md "The naming pass — Smart vs Smart+" (2026-07-27), N1-N13.
//
// The rule (plan.md A43): Smart is on this machine, deterministic and free;
// Smart+ is a model asked, and the "+" is the egress-and-billing boundary —
// never a quality claim. These tests keep the catalog from drifting off that
// rule one string at a time, the way it had before this pass (N2).

// allValues walks every registered value — text and every plural form — so a
// guard below sees the same catalog a translator would.
func allValues() map[string]string {
	out := map[string]string{}
	for _, e := range Export(DefaultLocale) {
		out[e.Key] = e.Text
		for cat, v := range e.Plural {
			out[e.Key+"#"+string(cat)] = v
		}
	}
	return out
}

// smartBareExemptions are the values allowed to say "Smart" without a "+" —
// tier-explainer prose that has to name the free half to contrast it with the
// paid one (N2's "except the tier explainer" carve-out, read generously: the
// explainer appears wherever the app teaches this vocabulary, not only on the
// Smart+ tab itself).
var smartBareExemptions = map[string]bool{
	"home.edgeLede":          true, // "Smart is deterministic... Smart+ is the tier..."
	"classify.catGroupHint":  true, // "Smart categories: the 26 sections..."
	"appearance.attuneByHue": true, // "This is Smart colours: worked out..."
}

// TestSmartAlwaysMeansSmartPlus is N2: "Smart" bare is a false statement about
// what actually stopped whenever it is used to mean the paid half — the
// classifier, the ranker and the hue tint keep working through an OpenAI
// outage, and "Smart features are paused" told the reader otherwise.
func TestSmartAlwaysMeansSmartPlus(t *testing.T) {
	for key, val := range allValues() {
		if smartBareExemptions[key] {
			continue
		}
		i := 0
		for {
			idx := strings.Index(val[i:], "Smart")
			if idx < 0 {
				break
			}
			pos := i + idx
			end := pos + len("Smart")
			if end >= len(val) || val[end] != '+' {
				t.Errorf("%s: %q uses bare \"Smart\" at byte %d — Smart+ is what the "+
					"paid, model-backed half is called; bare \"Smart\" here reads as a "+
					"claim about the free half too", key, val, pos)
				break
			}
			i = end
		}
	}
}

// tierLabelRe matches a short, standalone control label built on the Smart+
// brand — at most two words after the prefix, no sentence punctuation. A full
// sentence that happens to contain "Smart+ voice" mid-clause does not match;
// only a value that IS the label does, which is what keeps this test from
// drowning in prose.
var tierLabelRe = regexp.MustCompile(`^Smart\+(?: [a-z]+){0,2}$`)

// canonicalTierLabels is N1's canonical list: every short Smart+ label the
// catalog is allowed to carry. A fifth two-tier capability, or any control
// renamed onto a fresh coinage, has to widen this list in the same commit —
// which is the review hook N1 asks for.
var canonicalTierLabels = map[string]bool{
	"Smart+":         true, // the bare brand, used as a group heading
	"Smart+ ranking": true, // smart.feedPlusLabel — My Feed's paid reorder
	"Smart+ colours": true, // appearance.attuneSmartLabel — the paid palette
	"Smart+ follow":  true, // addFeed.smartName — rung 5 of the discovery ladder
	"Smart+ voice":   true, // Smart+-only; never paired with a bare "Smart voice" (N4)
	"Smart+ file":    true, // addFeed.categorizeSmartName — suggests a folder
	"Smart+ model":   true, // smart.modelAria — the model picker, not a capability pair
	// discover.smartPlusToggle — the consent gate on the Discover page. It names
	// the "2 posts reviewed" check (a candidate's own posts read against what
	// the reader reads) and, with it on, rung 5's web search. A capability, not
	// a control: with the toggle off there is no Discover page at all, so this
	// label is the whole feature's name where the reader meets it.
	"Smart+ review": true,
}

func TestTierPrefixedLabelsMatchCanonicalList(t *testing.T) {
	seen := map[string]bool{}
	for key, val := range allValues() {
		if !tierLabelRe.MatchString(val) {
			continue
		}
		seen[val] = true
		if !canonicalTierLabels[val] {
			t.Errorf("%s: %q is a new Smart+ label not in canonicalTierLabels — "+
				"add it there deliberately, with a comment saying which capability it names", key, val)
		}
	}
	for want := range canonicalTierLabels {
		if !seen[want] {
			t.Errorf("canonicalTierLabels contains %q, but no catalog value matches it any more — "+
				"prune the stale entry", want)
		}
	}
}

// TestNoHomepage is N9: the app has no page called "homepage" — the flagship
// stream is My Feed, named consistently everywhere it is mentioned.
func TestNoHomepage(t *testing.T) {
	for key, val := range allValues() {
		if strings.Contains(strings.ToLower(val), "homepage") {
			t.Errorf("%s: %q says \"homepage\", which does not exist in this app — it means My Feed", key, val)
		}
	}
}

// railWordRe matches "rail" as its own word, so "trail", "railway" etc. (none
// exist today, but the point is not to false-positive if one ever does) don't
// trip the guard.
var railWordRe = regexp.MustCompile(`(?i)\brail\b`)

// TestNoRailInCopy is N10: "rail" is the identifier for the sidebar column in
// client/view and client/i18n's own namespace names — never the word shown to
// a reader, who is told "sidebar" everywhere else this column is named.
func TestNoRailInCopy(t *testing.T) {
	for key, val := range allValues() {
		if railWordRe.MatchString(val) {
			t.Errorf("%s: %q says \"rail\" — the reader-facing word for this column is \"sidebar\"", key, val)
		}
	}
}

// categorySenseAllow is N8: "category"/"categories" is reserved for the 26
// article subjects (internal/classify's taxonomy). Every value below is
// allowed to use the word because it is in that sense; a folder is named
// "folder" everywhere else. {placeholder} tokens are stripped before this
// runs, so a template arg literally named {category} (it substitutes a
// folder's own name, e.g. "File under Tech?") does not need listing here.
var categorySenseAllow = map[string]bool{
	"category.software": true, "category.ai": true, "category.hardware": true,
	"category.security": true, "category.science": true, "category.health": true,
	"category.business": true, "category.finance": true, "category.politics": true,
	"category.world": true, "category.law": true, "category.climate": true,
	"category.space": true, "category.energy": true, "category.transport": true,
	"category.gaming": true, "category.filmtv": true, "category.music": true,
	"category.culture": true, "category.books": true, "category.sport": true,
	"category.food": true, "category.travel": true, "category.design": true,
	"category.work": true, "category.education": true,
	"classify.catGroup": true, "classify.catGroupHint": true, "classify.catShowHint": true,
	"classify.smartGroupHint": true, "classify.smartOwnHint": true,
	"home.edgeLede": true, "home.findClassH": true, "home.findClassP": true,
	// The tab that edits the article axis is itself named "Categories" (D23).
	"settings.tab.classify": true,
	// The rail's third band, and the leftover pile inside it (2026-08-01).
	//
	// D23 resolved to "Folders" for the rail on 2026-07-31, and this is the
	// other half of that decision arriving: the band now holds BOTH the
	// reader's own folders and the classifier's labels, divided by a "By topic"
	// rule, so it is not the folder sense wearing the article axis's word — it
	// is a container for both, and "Folders" made it a generic box holding the
	// thing the reader was looking for. `rail.uncategorised` is unambiguously
	// the article axis: the pile with no topic label, deliberately not
	// "Unfiled", which is the row above it and means a FEED with no folder.
	//
	// This narrows D23 rather than reversing it — every string that names ONLY
	// the folder sense still says folder, which is what the rest of this test
	// keeps in force. Flagged for Cam: plan.md §27.0a records D23 as resolved
	// the other way, and the register should say which of the two this is.
	"rail.bandCategories": true,
	"rail.uncategorised":  true,
	// Contrasts OUR word against the OPML standard's own attribute name for a
	// reader arriving from another reader's export — both words are doing
	// necessary, disambiguated work in the one sentence that says so.
	"data.inGroupHint": true,
}

var placeholderRe = regexp.MustCompile(`\{[^}]*\}`)

func TestCategoryMeansOnlyArticleSubjects(t *testing.T) {
	for key, val := range allValues() {
		if categorySenseAllow[key] {
			continue
		}
		stripped := placeholderRe.ReplaceAllString(val, "")
		if strings.Contains(strings.ToLower(stripped), "categor") {
			t.Errorf("%s: %q uses \"category\"/\"categorize\" outside the article-subject sense — "+
				"the folder sense is spelled \"folder\" (D23, plan.md §27.0a)", key, val)
		}
	}
}
