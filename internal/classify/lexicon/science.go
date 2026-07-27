package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// science is the Science & Research category (plan.md §27.3d #5).
//
// The bare word `study` is deliberately absent (§27.3d's own note on the row)
// — "a study found" is the connective tissue of health, food and psychology
// reporting as much as it is science's, and an unguarded "study" would fire
// on half the corpus. `study finds` survives as a phrase because a headline
// that leads with it is reporting original research rather than citing one.
func science() classify.Label {
	return classify.Label{
		Slug: "science",
		Name: "Science & Research",
		Terms: []classify.Term{
			{Text: "arxiv", Weight: 2.6},
			{Text: "crispr", Weight: 2.6},
			{Text: "gravitational wave", Weight: 2.4},
			{Text: "particle accelerator", Weight: 2.4},
			{Text: "quantum entanglement", Weight: 2.4},
			{Text: "supercollider", Weight: 2.2},
			{Text: "peer-reviewed", Weight: 2.2},
			{Text: "nobel prize", Weight: 2.2},
			{Text: "dark matter", Weight: 2.2},
			{Text: "genome sequencing", Weight: 2.2},
			{Text: "particle physics", Weight: 2.2},
			{Text: "quantum computing", Weight: 2.0},
			{Text: "gene editing", Weight: 2.0},
			{Text: "paleontology", Weight: 2.0},
			{Text: "radiocarbon dating", Weight: 2.0},
			{Text: "quantum mechanics", Weight: 2.0},
			{Text: "scientific breakthrough", Weight: 1.8},
			{Text: "replication crisis", Weight: 2.0},
			{Text: "double-blind", Weight: 2.0},
			{Text: "statistically significant", Weight: 2.0},
			{Text: "peer review", Weight: 2.0},
			{Text: "dna sequencing", Weight: 1.8},
			{Text: "molecular biology", Weight: 1.8},
			{Text: "stem cell", Weight: 1.8},
			{Text: "fossil discovery", Weight: 1.8},
			{Text: "evolutionary biology", Weight: 1.8},
			{Text: "meta-analysis", Weight: 1.8},
			{Text: "neuroscience", Weight: 1.8},
			{Text: "astrobiology", Weight: 1.8},
			{Text: "astrophysics", Weight: 1.8},
			{Text: "spectrometer", Weight: 1.6},
			{Text: "scientific journal", Weight: 1.6},
			{Text: "genome", Weight: 1.6},
			{Text: "cognitive science", Weight: 1.6},
			{Text: "genetics", Weight: 1.4},
			{Text: "biotechnology", Weight: 1.4},
			{Text: "microbiology", Weight: 1.4},
			{Text: "seismology", Weight: 1.6},
			{Text: "isotope", Weight: 1.4},
			{Text: "hypothesis", Weight: 1.4},
			{Text: "control group", Weight: 1.4},
			{Text: "research paper", Weight: 1.4},
			{Text: "cern", Weight: 1.6},
			{Text: "study finds", Weight: 1.6},
			{Text: "sample size", Weight: 1.2},
			{Text: "physicist", Weight: 1.2},
			{Text: "biologist", Weight: 1.2},
			{Text: "quantum", Weight: 1.1},
			{Text: "physics", Weight: 1.1},
			{Text: "archaeology", Weight: 1.1},
			{Text: "geology", Weight: 1.0},
			{Text: "chemist", Weight: 1.0},
			{Text: "chemistry", Weight: 0.9},
			{Text: "biology", Weight: 0.9},
			{Text: "particle", Weight: 0.9},
			{Text: "genome project", Weight: 1.6},
			{Text: "research team", Weight: 0.8},
			{Text: "microscope", Weight: 0.9},
			{Text: "laboratory", Weight: 0.8},
			{Text: "experiment", Weight: 0.7},
			{Text: "psychology", Weight: 0.7},
			{Text: "theory", Weight: 0.5},
			{Text: "replicate", Weight: 0.6},
		},
		Exclude: []classify.Term{
			{Text: "conspiracy theory", Weight: 2.0},
			{Text: "acting theory", Weight: 1.0},
		},
		MinScore: 0,
		Prompt: "Assign for scientific research and discovery: physics, biology, genetics, " +
			"published studies. Not for health/medical treatment (see Health & Medicine) or " +
			"climate policy (see Climate & Environment).",
	}
}
