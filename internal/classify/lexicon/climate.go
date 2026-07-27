package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// climate is the Climate & Environment category (plan.md §27.3d #12).
//
// `amazon` needs the opposite guard from hardware's and business's: here the
// unambiguous reading is the rainforest, and it only scores alongside a term
// that could not plausibly describe the company. `amazon rainforest` is left
// as its own high-weight phrase for the common case where the guard would be
// redundant.
func climate() classify.Label {
	return classify.Label{
		Slug: "climate",
		Name: "Climate & Environment",
		Terms: []classify.Term{
			{Text: "cop30", Weight: 2.6},
			{Text: "ocean acidification", Weight: 2.4},
			{Text: "ipcc report", Weight: 2.4},
			{Text: "sea level rise", Weight: 2.4},
			{Text: "amazon rainforest", Weight: 2.4},
			{Text: "species extinction", Weight: 2.2},
			{Text: "carbon capture", Weight: 2.2},
			{Text: "glacier melt", Weight: 2.2},
			{Text: "deforestation", Weight: 2.2},
			{Text: "permafrost", Weight: 2.2},
			{Text: "methane emissions", Weight: 2.2},
			{Text: "paris agreement", Weight: 2.2},
			{Text: "climate change", Weight: 2.0},
			{Text: "global warming", Weight: 2.0},
			{Text: "greenhouse gas", Weight: 2.0},
			{Text: "net zero", Weight: 2.0},
			{Text: "carbon tax", Weight: 2.0},
			{Text: "carbon offset", Weight: 2.0},
			{Text: "carbon neutral", Weight: 2.0},
			{Text: "carbon sink", Weight: 2.0},
			{Text: "wildfire", Weight: 2.0},
			{Text: "coral reef", Weight: 2.0},
			{Text: "heatwave", Weight: 2.0},
			{Text: "arctic ice", Weight: 2.0},
			{Text: "polar ice", Weight: 2.0},
			{Text: "climate summit", Weight: 2.0},
			{Text: "climate crisis", Weight: 2.0},
			{Text: "biodiversity loss", Weight: 2.0},
			{Text: "habitat loss", Weight: 2.0},
			{Text: "rewilding", Weight: 2.0},
			{Text: "climate denial", Weight: 2.0},
			{Text: "ecosystem collapse", Weight: 2.0},
			{Text: "extreme weather", Weight: 1.8},
			{Text: "endangered species", Weight: 1.8},
			{Text: "climate resilience", Weight: 1.8},
			{Text: "biodiversity", Weight: 1.8},
			{Text: "drought", Weight: 1.8},
			{Text: "greenhouse effect", Weight: 1.8},
			{Text: "wildlife conservation", Weight: 1.8},
			{Text: "emissions target", Weight: 1.8},
			{Text: "carbon footprint", Weight: 1.8},
			{Text: "environmental policy", Weight: 1.6},
			{Text: "environmental regulation", Weight: 1.6},
			{Text: "climate activist", Weight: 1.6},
			{Text: "flood risk", Weight: 1.6},
			{Text: "drought relief", Weight: 1.6},
			{Text: "climate policy", Weight: 1.6},
			{Text: "fossil fuel", Weight: 1.6},
			{Text: "climate migration", Weight: 2.0},
			{Text: "climate finance", Weight: 1.8},
			{Text: "invasive species", Weight: 1.8},
			{Text: "climate adaptation", Weight: 1.6},
			{Text: "renewable energy", Weight: 1.4},
			{Text: "tipping point", Weight: 1.4},
			{Text: "conservation", Weight: 1.2},
			{Text: "climate report", Weight: 1.2},
			{Text: "emissions", Weight: 1.4},
			{Text: "sustainability", Weight: 0.8},
			{Text: "renewable", Weight: 0.6},
			{Text: "amazon", Weight: 1.0, Requires: []string{
				"rainforest", "deforestation", "basin", "indigenous", "brazil",
				"peru", "logging", "biodiversity", "carbon sink",
			}},
		},
		MinScore: 0,
		Prompt: "Assign for the climate and natural environment: warming, emissions, ecosystems, " +
			"conservation. Not for a single weather event with no climate-trend angle (see World " +
			"News), and not for sustainability marketing with no environmental substance.",
	}
}
