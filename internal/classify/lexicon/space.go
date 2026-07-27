package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// space is the Space category (plan.md §27.3d #13).
//
// `probe` is guarded because its dominant sense in a general news feed is an
// investigation, not a spacecraft. `mercury` is guarded against the far more
// common astrology idiom ("Mercury in retrograde") and the chemical element.
// `black hole` is present at a secondary weight — science.go carries the
// primary weight for it, since most black-hole coverage is a physics or
// astronomy finding rather than a mission story.
func space() classify.Label {
	return classify.Label{
		Slug: "space",
		Name: "Space",
		Terms: []classify.Term{
			{Text: "james webb telescope", Weight: 2.6},
			{Text: "mars rover", Weight: 2.4},
			{Text: "falcon 9", Weight: 2.4},
			{Text: "artemis mission", Weight: 2.2},
			{Text: "lunar lander", Weight: 2.2},
			{Text: "moon landing", Weight: 2.2},
			{Text: "spacex", Weight: 2.2},
			{Text: "spacewalk", Weight: 2.2},
			{Text: "exoplanet", Weight: 2.2},
			{Text: "international space station", Weight: 2.2},
			{Text: "reusable rocket", Weight: 2.0},
			{Text: "satellite constellation", Weight: 2.0},
			{Text: "starlink", Weight: 2.0},
			{Text: "space station", Weight: 2.0},
			{Text: "supernova", Weight: 2.0},
			{Text: "lunar mission", Weight: 2.0},
			{Text: "mars mission", Weight: 2.0},
			{Text: "blue origin", Weight: 2.0},
			{Text: "hubble", Weight: 2.0},
			{Text: "starship", Weight: 1.8},
			{Text: "launch pad", Weight: 1.8},
			{Text: "booster rocket", Weight: 1.8},
			{Text: "space debris", Weight: 1.8},
			{Text: "cosmonaut", Weight: 1.8},
			{Text: "zero gravity", Weight: 1.8},
			{Text: "microgravity", Weight: 1.8},
			{Text: "meteor shower", Weight: 1.8},
			{Text: "cosmic ray", Weight: 1.8},
			{Text: "docking maneuver", Weight: 1.8},
			{Text: "space tourism", Weight: 1.8},
			{Text: "lunar surface", Weight: 1.8},
			{Text: "moon base", Weight: 1.8},
			{Text: "mars colony", Weight: 1.8},
			{Text: "orbital mechanics", Weight: 1.8},
			{Text: "black hole", Weight: 1.6},
			{Text: "nasa", Weight: 1.8},
			{Text: "astronaut", Weight: 1.6},
			{Text: "deep space", Weight: 1.6},
			{Text: "interstellar", Weight: 1.6},
			{Text: "spacecraft", Weight: 1.6},
			{Text: "mission control", Weight: 1.6},
			{Text: "asteroid", Weight: 1.6},
			{Text: "comet", Weight: 1.6},
			{Text: "esa", Weight: 1.6},
			{Text: "iss", Weight: 1.6},
			{Text: "space exploration", Weight: 1.6},
			{Text: "rocket engine", Weight: 1.6},
			{Text: "planetary science", Weight: 1.6},
			{Text: "space race", Weight: 1.6},
			{Text: "space agency", Weight: 1.4},
			{Text: "satellite", Weight: 1.4},
			{Text: "martian", Weight: 1.4},
			{Text: "solar system", Weight: 1.4},
			{Text: "space launch system", Weight: 2.0},
			{Text: "space telescope", Weight: 1.8},
			{Text: "planetary defense", Weight: 1.8},
			{Text: "solar flare", Weight: 1.8},
			{Text: "orbit", Weight: 1.2},
			{Text: "payload", Weight: 1.2},
			{Text: "probe", Weight: 0.9, Requires: []string{
				"space", "nasa", "spacecraft", "mission", "orbit", "mars", "asteroid", "rover",
			}},
			// The planet, not the retrograde-astrology sense or the element.
			{Text: "mercury", Weight: 1.1, Requires: []string{
				"planet", "orbit", "nasa", "solar system", "messenger",
				"spacecraft", "probe", "perihelion",
			}},
		},
		Exclude: []classify.Term{
			{Text: "mercury in retrograde", Weight: 3.0},
			{Text: "mercury retrograde", Weight: 3.0},
			{Text: "mercury poisoning", Weight: 2.0},
			{Text: "mercury thermometer", Weight: 2.0},
		},
		MinScore: 0,
		Prompt: "Assign for spaceflight and astronomy missions: launches, probes, telescopes, " +
			"orbital hardware. Not for a general physics finding with no mission or observatory " +
			"attached (see Science & Research).",
	}
}
