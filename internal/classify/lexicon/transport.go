package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// transport is the Transport & Mobility category (plan.md §27.3d #15).
//
// Deliberately holds the vehicle-maker's own vocabulary (`electric vehicle`,
// `ev charging`, `self-driving car`) rather than a specific brand — Tesla the
// company is business.go's guarded term, and a person following the industry
// wants EV coverage regardless of which manufacturer the article is about.
func transport() classify.Label {
	return classify.Label{
		Slug: "transport",
		Name: "Transport & Mobility",
		Terms: []classify.Term{
			{Text: "self-driving car", Weight: 2.4},
			{Text: "high-speed rail", Weight: 2.4},
			{Text: "hyperloop", Weight: 2.2},
			{Text: "maglev train", Weight: 2.2},
			{Text: "bullet train", Weight: 2.2},
			{Text: "air traffic control", Weight: 2.2},
			{Text: "robotaxi", Weight: 2.2},
			{Text: "autonomous vehicle", Weight: 2.2},
			{Text: "aviation safety", Weight: 2.0},
			{Text: "electric vehicle", Weight: 2.0},
			// The plural is a separate term because the scanner lowercases but
			// does not stem, and headlines use it at least as often as the
			// singular. It was found by mining the live corpus, added, and then
			// REVERTED by the agent that found it — because raising transport's
			// recall past the weakRecall floor fails the ratchet until the entry
			// is deleted, and it could not edit the test. The ratchet was working
			// as designed and the design punished an improvement; the fix is this
			// term plus the deletion in precision_test.go, not a smaller lexicon.
			{Text: "electric vehicles", Weight: 2.0},
			{Text: "freight rail", Weight: 2.0},
			{Text: "light rail", Weight: 2.0},
			{Text: "commuter rail", Weight: 2.0},
			{Text: "cycling infrastructure", Weight: 2.0},
			{Text: "micromobility", Weight: 2.0},
			{Text: "congestion pricing", Weight: 2.0},
			{Text: "vehicle recall", Weight: 2.0},
			{Text: "last-mile delivery", Weight: 2.0},
			{Text: "flight delay", Weight: 1.8},
			{Text: "flight cancellation", Weight: 1.8},
			{Text: "subway system", Weight: 1.8},
			{Text: "public transit", Weight: 1.8},
			{Text: "mass transit", Weight: 1.8},
			{Text: "ev charging", Weight: 1.8},
			{Text: "bike lane", Weight: 1.8},
			{Text: "e-scooter", Weight: 1.8},
			{Text: "ride-hailing", Weight: 1.8},
			{Text: "port authority", Weight: 1.8},
			{Text: "cargo ship", Weight: 1.8},
			{Text: "electric bus", Weight: 1.8},
			{Text: "freight logistics", Weight: 1.6},
			{Text: "shipping container", Weight: 1.6},
			{Text: "boeing", Weight: 1.6},
			{Text: "airbus", Weight: 1.6},
			{Text: "faa", Weight: 1.8},
			{Text: "traffic congestion", Weight: 1.6},
			{Text: "pedestrian safety", Weight: 1.6},
			{Text: "rideshare", Weight: 1.6},
			{Text: "rail network", Weight: 1.6},
			{Text: "port congestion", Weight: 1.8},
			{Text: "hybrid vehicle", Weight: 1.6},
			{Text: "toll road", Weight: 1.2},
			{Text: "drone delivery", Weight: 1.6},
			{Text: "metro system", Weight: 1.6},
			{Text: "transportation department", Weight: 1.6},
			{Text: "ev range", Weight: 1.6},
			{Text: "airline industry", Weight: 1.4},
			{Text: "road safety", Weight: 1.4},
			{Text: "traffic accident", Weight: 1.4},
			{Text: "fuel efficiency", Weight: 1.4},
			{Text: "runway", Weight: 1.2},
			{Text: "airline", Weight: 1.0},
			{Text: "infrastructure bill", Weight: 1.2},
			{Text: "emissions standards", Weight: 1.0},
			{Text: "freight", Weight: 1.0},
			{Text: "mpg", Weight: 1.0},
			{Text: "traffic jam", Weight: 0.9},
			{Text: "car crash", Weight: 0.8},
			{Text: "ev", Weight: 0.8},
			{Text: "commute", Weight: 0.6},
			{Text: "taxi", Weight: 0.5},

			// Chinese EV makers: this feed carries a dedicated slice of
			// China-EV coverage, and none of these brand names have any
			// competing common-word reading, so they need no guard.
			{Text: "xpeng", Weight: 1.4},
			{Text: "byd", Weight: 1.4},
			{Text: "li auto", Weight: 1.4},
			{Text: "leapmotor", Weight: 1.4},
			{Text: "nio", Weight: 1.3},
			{Text: "avatr", Weight: 1.3},
		},
		MinScore: 0,
		Prompt: "Assign for how people and goods move: vehicles, transit systems, aviation, " +
			"shipping. Not for a single automaker's business results (see Business & Companies), " +
			"and not for climate-emissions framing with no vehicle or system in view.",
	}
}
