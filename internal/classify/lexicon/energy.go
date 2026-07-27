package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// energy is the Energy & Industry category (plan.md §27.3d #14).
//
// The energy transition sits at the seam between this category and Climate
// (§27.3d #12): a wind farm or battery plant is industrial infrastructure
// first and a climate story second, which is why the strongest terms here
// are physical-plant vocabulary rather than the emissions framing that
// climate.go already owns.
func energy() classify.Label {
	return classify.Label{
		Slug: "energy",
		Name: "Energy & Industry",
		Terms: []classify.Term{
			{Text: "nuclear fusion", Weight: 2.6},
			{Text: "nuclear power plant", Weight: 2.4},
			{Text: "oil spill", Weight: 2.2},
			{Text: "fracking", Weight: 2.2},
			{Text: "nuclear reactor", Weight: 2.2},
			{Text: "rare earth mineral", Weight: 2.2},
			{Text: "grid failure", Weight: 2.0},
			{Text: "energy crisis", Weight: 2.0},
			{Text: "enrichment facility", Weight: 2.0},
			{Text: "cobalt mining", Weight: 2.0},
			{Text: "offshore drilling", Weight: 2.0},
			{Text: "offshore wind", Weight: 2.0},
			{Text: "lithium mining", Weight: 2.0},
			{Text: "battery plant", Weight: 2.0},
			{Text: "gigafactory", Weight: 2.0},
			{Text: "oil refinery", Weight: 2.0},
			{Text: "pipeline leak", Weight: 2.0},
			{Text: "opec", Weight: 2.0},
			{Text: "nuclear waste", Weight: 2.0},
			{Text: "energy transition", Weight: 1.8},
			{Text: "energy independence", Weight: 1.8},
			{Text: "solar farm", Weight: 1.8},
			{Text: "wind turbine", Weight: 1.8},
			{Text: "energy storage", Weight: 1.8},
			{Text: "hydropower", Weight: 1.8},
			{Text: "geothermal", Weight: 1.8},
			{Text: "smart grid", Weight: 1.8},
			{Text: "battery storage", Weight: 1.8},
			{Text: "lithium battery", Weight: 1.8},
			{Text: "coal plant", Weight: 1.8},
			{Text: "drilling rig", Weight: 1.8},
			{Text: "barrel of oil", Weight: 1.8},
			{Text: "uranium", Weight: 1.8},
			{Text: "power grid", Weight: 1.8},
			{Text: "power outage", Weight: 1.6},
			{Text: "blackout", Weight: 1.6},
			{Text: "solar panel", Weight: 1.6},
			{Text: "natural gas", Weight: 1.6},
			{Text: "transmission line", Weight: 1.6},
			{Text: "utility-scale", Weight: 1.6},
			{Text: "decommissioning", Weight: 1.6},
			{Text: "electricity price", Weight: 1.6},
			{Text: "capacity factor", Weight: 1.6},
			{Text: "power plant", Weight: 1.4},
			{Text: "gas prices", Weight: 1.4},
			{Text: "energy demand", Weight: 1.4},
			{Text: "energy policy", Weight: 1.4},
			{Text: "utility company", Weight: 1.4},
			{Text: "refinery", Weight: 1.4},
			{Text: "pipeline", Weight: 1.2},
			{Text: "energy sector", Weight: 1.2},
			{Text: "power company", Weight: 1.2},
			{Text: "power line", Weight: 1.2},
			{Text: "mining operation", Weight: 1.1},
			{Text: "turbine", Weight: 1.0},
			{Text: "grid", Weight: 0.8},
		},
		MinScore: 0,
		Prompt: "Assign for energy production and industrial infrastructure: power plants, grids, " +
			"mining, drilling. Not for climate policy or emissions framing with no plant or " +
			"infrastructure angle (see Climate & Environment).",
	}
}
