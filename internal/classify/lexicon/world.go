package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// world is the World News category (plan.md §27.3d #10).
//
// International conflict, diplomacy and disaster, as distinct from domestic
// Politics (§27.3d #9). Natural-disaster vocabulary lives here rather than
// in Climate & Environment because a single earthquake or cyclone is an
// event, not a climate trend — Climate owns the pattern (§27.3d #12), World
// owns the incident.
func world() classify.Label {
	return classify.Label{
		Slug: "world",
		Name: "World News",
		Terms: []classify.Term{
			{Text: "genocide", Weight: 2.8},
			{Text: "war crimes", Weight: 2.6},
			{Text: "civil war", Weight: 2.4},
			{Text: "coup", Weight: 2.4},
			{Text: "ceasefire", Weight: 2.4},
			{Text: "martial law", Weight: 2.2},
			{Text: "military junta", Weight: 2.2},
			{Text: "ethnic conflict", Weight: 2.2},
			{Text: "insurgency", Weight: 2.2},
			{Text: "airstrike", Weight: 2.2},
			{Text: "un security council", Weight: 2.2},
			{Text: "un peacekeepers", Weight: 2.2},
			{Text: "arms embargo", Weight: 2.2},
			{Text: "humanitarian crisis", Weight: 2.2},
			{Text: "refugee crisis", Weight: 2.2},
			{Text: "military offensive", Weight: 2.2},
			{Text: "tsunami", Weight: 2.2},
			{Text: "volcano eruption", Weight: 2.0},
			{Text: "cyclone", Weight: 2.0},
			{Text: "famine", Weight: 2.0},
			{Text: "asylum seeker", Weight: 2.0},
			{Text: "displaced persons", Weight: 2.0},
			{Text: "humanitarian aid", Weight: 2.0},
			{Text: "territorial dispute", Weight: 2.0},
			{Text: "refugee camp", Weight: 2.0},
			{Text: "mass protest", Weight: 1.8},
			{Text: "uprising", Weight: 1.8},
			{Text: "state of emergency", Weight: 2.0},
			{Text: "un resolution", Weight: 2.0},
			{Text: "earthquake", Weight: 2.0},
			{Text: "peace talks", Weight: 2.0},
			{Text: "bilateral talks", Weight: 1.8},
			{Text: "trade sanctions", Weight: 1.8},
			{Text: "diplomatic relations", Weight: 1.8},
			{Text: "war zone", Weight: 1.8},
			{Text: "disaster relief", Weight: 1.6},
			{Text: "hurricane", Weight: 1.6},
			{Text: "dictator", Weight: 1.6},
			{Text: "nato", Weight: 1.6},
			{Text: "sanctions", Weight: 1.6},
			{Text: "geopolitical", Weight: 1.6},
			{Text: "foreign ministry", Weight: 1.6},
			{Text: "border crossing", Weight: 1.6},
			{Text: "conflict zone", Weight: 1.6},
			{Text: "protest movement", Weight: 1.6},
			{Text: "death toll", Weight: 1.6},
			{Text: "opposition leader", Weight: 1.4},
			{Text: "regime", Weight: 1.4},
			{Text: "foreign policy", Weight: 1.4},
			{Text: "international relations", Weight: 1.4},
			{Text: "treaty", Weight: 1.4},
			{Text: "occupation", Weight: 1.4},
			{Text: "evacuation", Weight: 1.4},
			{Text: "refugee", Weight: 1.4},
			{Text: "embassy", Weight: 1.2},
			{Text: "ambassador", Weight: 1.2},
			{Text: "diplomacy", Weight: 1.2},
			{Text: "natural disaster", Weight: 1.2},
			{Text: "summit", Weight: 1.0},
			{Text: "border", Weight: 0.8},
			{Text: "humanitarian", Weight: 0.9},
			{Text: "protest", Weight: 0.9},
			{Text: "flooding", Weight: 0.7},
		},
		MinScore: 0,
		Prompt: "Assign for international conflict, diplomacy and disaster. Not for domestic " +
			"legislative process (see Politics & Policy), and not for a slow climate trend rather " +
			"than a single event (see Climate & Environment).",
	}
}
