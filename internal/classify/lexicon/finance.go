package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// finance is the Finance & Markets category (plan.md §27.3d #8).
//
// `crypto` is anchored here rather than in software (§27.3d's own note):
// cryptocurrency price and regulation coverage outnumbers the underlying
// technology in a general feed by a wide margin, so this is the primary
// weight and software's copy of the term is deliberately the weaker,
// secondary one.
func finance() classify.Label {
	return classify.Label{
		Slug: "finance",
		Name: "Finance & Markets",
		Terms: []classify.Term{
			{Text: "interest rate", Weight: 2.4},
			{Text: "quantitative easing", Weight: 2.4},
			{Text: "consumer price index", Weight: 2.2},
			{Text: "rate hike", Weight: 2.2},
			{Text: "rate cut", Weight: 2.2},
			{Text: "crypto", Weight: 2.2},
			{Text: "margin call", Weight: 2.0},
			{Text: "junk bond", Weight: 2.0},
			{Text: "bear market", Weight: 2.0},
			{Text: "bull market", Weight: 2.0},
			{Text: "recession", Weight: 2.0},
			{Text: "gdp growth", Weight: 2.0},
			{Text: "unemployment rate", Weight: 2.0},
			{Text: "treasury yield", Weight: 2.0},
			{Text: "inflation", Weight: 2.0},
			{Text: "nasdaq", Weight: 2.0},
			{Text: "dow jones", Weight: 2.0},
			{Text: "cryptocurrency", Weight: 2.0},
			{Text: "hedge fund", Weight: 2.0},
			{Text: "capital gains", Weight: 1.8},
			{Text: "short selling", Weight: 1.8},
			{Text: "foreclosure", Weight: 1.8},
			{Text: "stock split", Weight: 1.8},
			{Text: "treasury bond", Weight: 1.8},
			{Text: "forex", Weight: 1.8},
			{Text: "credit rating", Weight: 1.8},
			{Text: "bitcoin", Weight: 1.8},
			{Text: "ethereum", Weight: 1.8},
			{Text: "stablecoin", Weight: 1.8},
			{Text: "s&p 500", Weight: 2.0},
			{Text: "earnings report", Weight: 1.6},
			{Text: "dividend", Weight: 1.6},
			{Text: "mutual fund", Weight: 1.6},
			{Text: "index fund", Weight: 1.6},
			{Text: "day trading", Weight: 1.6},
			{Text: "exchange rate", Weight: 1.6},
			{Text: "default risk", Weight: 1.6},
			{Text: "gold price", Weight: 1.6},
			{Text: "oil price", Weight: 1.6},
			{Text: "blockchain", Weight: 1.6},
			{Text: "wall street", Weight: 1.6},
			{Text: "mortgage rate", Weight: 1.8},
			{Text: "retirement fund", Weight: 1.6},
			{Text: "pension fund", Weight: 1.6},
			{Text: "equities", Weight: 1.4},
			{Text: "bond yield", Weight: 1.6},
			{Text: "stock market", Weight: 1.4},
			{Text: "currency exchange", Weight: 1.4},
			{Text: "banking sector", Weight: 1.4},
			{Text: "credit score", Weight: 1.4},
			{Text: "tax bracket", Weight: 1.4},
			{Text: "refinance", Weight: 1.4},
			{Text: "crude oil", Weight: 1.2},
			{Text: "fed", Weight: 1.2},
			{Text: "earnings", Weight: 1.0},
			{Text: "mortgage", Weight: 1.2},
			{Text: "volatility", Weight: 1.0},
			{Text: "commodity", Weight: 0.9},
			{Text: "portfolio", Weight: 0.7},
			{Text: "savings account", Weight: 0.8},
			{Text: "personal finance", Weight: 0.7},
			{Text: "bond", Weight: 0.7},
		},
		Exclude: []classify.Term{
			{Text: "personal bond", Weight: 1.0},
			{Text: "bond film", Weight: 2.0},
			{Text: "james bond", Weight: 2.5},
		},
		MinScore: 0,
		Prompt: "Assign for markets, currencies, rates and macro finance. Not for a single " +
			"company's earnings with no market angle (see Business & Companies), and not for " +
			"personal-budgeting lifestyle advice.",
	}
}
