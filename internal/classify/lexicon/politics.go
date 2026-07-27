package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// politics is the Politics & Policy category (plan.md §27.3d #9).
//
// Domestic government process — elections, legislatures, campaigns — as
// distinct from World News (§27.3d #10), which is international relations
// and conflict. The two share enough vocabulary (referendum, coalition) that
// the split has to be made on the article's actual subject rather than on
// disjoint word lists, which is what the prompt states explicitly.
func politics() classify.Label {
	return classify.Label{
		Slug: "politics",
		Name: "Politics & Policy",
		Terms: []classify.Term{
			{Text: "impeachment", Weight: 2.6},
			{Text: "gerrymandering", Weight: 2.4},
			{Text: "electoral college", Weight: 2.2},
			{Text: "filibuster", Weight: 2.2},
			{Text: "midterm election", Weight: 2.2},
			{Text: "no-confidence vote", Weight: 2.2},
			{Text: "constitutional amendment", Weight: 2.2},
			{Text: "swing state", Weight: 2.0},
			{Text: "referendum", Weight: 2.0},
			{Text: "campaign finance", Weight: 2.0},
			{Text: "super pac", Weight: 2.0},
			{Text: "cabinet reshuffle", Weight: 2.0},
			{Text: "executive order", Weight: 2.0},
			{Text: "primary election", Weight: 2.0},
			{Text: "coalition government", Weight: 2.0},
			{Text: "presidential debate", Weight: 2.0},
			{Text: "presidential election", Weight: 2.0},
			{Text: "house of representatives", Weight: 2.0},
			{Text: "congressional hearing", Weight: 2.0},
			{Text: "ballot measure", Weight: 1.8},
			{Text: "campaign donation", Weight: 1.8},
			{Text: "running mate", Weight: 1.8},
			{Text: "political convention", Weight: 1.8},
			{Text: "campaign trail", Weight: 1.8},
			{Text: "gubernatorial", Weight: 1.8},
			{Text: "voter turnout", Weight: 1.8},
			{Text: "parliament", Weight: 1.8},
			{Text: "lobbying", Weight: 1.6},
			{Text: "veto", Weight: 1.6},
			{Text: "opposition party", Weight: 1.6},
			{Text: "voter registration", Weight: 1.6},
			{Text: "opinion poll", Weight: 1.6},
			{Text: "approval rating", Weight: 1.6},
			{Text: "prime minister", Weight: 1.6},
			{Text: "term limit", Weight: 1.6},
			{Text: "party platform", Weight: 1.6},
			{Text: "immigration policy", Weight: 1.6},
			{Text: "senate", Weight: 1.6},
			{Text: "legislation", Weight: 1.6},
			{Text: "election", Weight: 1.4},
			{Text: "bipartisan", Weight: 1.4},
			{Text: "white house", Weight: 1.4},
			{Text: "vice president", Weight: 1.4},
			{Text: "cabinet member", Weight: 1.4},
			{Text: "constituency", Weight: 1.4},
			{Text: "political ad", Weight: 1.4},
			{Text: "ruling party", Weight: 1.4},
			{Text: "legislature", Weight: 1.4},
			{Text: "healthcare policy", Weight: 1.4},
			{Text: "ballot", Weight: 1.2},
			{Text: "lawmaker", Weight: 1.2},
			{Text: "incumbent", Weight: 1.2},
			{Text: "political party", Weight: 1.2},
			{Text: "partisan", Weight: 1.2},
			{Text: "senator", Weight: 1.2},
			{Text: "congress", Weight: 1.2},
			{Text: "town hall", Weight: 1.0},
			{Text: "tax policy", Weight: 1.0},
			{Text: "policy proposal", Weight: 1.0},
			{Text: "coalition", Weight: 1.0},
			{Text: "campaign", Weight: 0.9},
			{Text: "political rally", Weight: 1.2},
			{Text: "president", Weight: 0.6},
			{Text: "representative", Weight: 0.5},
		},
		MinScore: 0,
		Prompt: "Assign for domestic government process: elections, legislatures, campaigns, " +
			"policy debate. Not for international conflict or diplomacy (see World News), and not " +
			"for court rulings (see Law & Courts).",
	}
}
