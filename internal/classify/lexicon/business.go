package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// business is the Business & Companies category (plan.md §27.3d #7).
//
// `meta`, `amazon` and `tesla` are all company names that collide with an
// older, more common reading — a philosophical aside, a rainforest, a
// nineteenth-century inventor. Each is guarded on the product and executive
// vocabulary that actually distinguishes a corporate story, rather than on
// the company name's synonyms, because the synonyms are exactly what the
// competing sense also uses.
func business() classify.Label {
	return classify.Label{
		Slug: "business",
		Name: "Business & Companies",
		Terms: []classify.Term{
			{Text: "hostile takeover", Weight: 2.4},
			{Text: "antitrust", Weight: 2.2},
			{Text: "ipo", Weight: 2.2},
			{Text: "acquisition", Weight: 2.0},
			{Text: "funding round", Weight: 2.0},
			{Text: "layoffs", Weight: 2.0},
			{Text: "bankruptcy", Weight: 2.0},
			{Text: "series a", Weight: 2.0},
			{Text: "seed funding", Weight: 2.0},
			{Text: "unicorn startup", Weight: 2.0},
			{Text: "merger", Weight: 1.9},
			{Text: "going public", Weight: 1.9},
			{Text: "class action lawsuit", Weight: 1.6},
			{Text: "venture capital", Weight: 1.8},
			{Text: "stock buyback", Weight: 1.8},
			{Text: "valuation", Weight: 1.6},
			{Text: "monopoly", Weight: 1.6},
			{Text: "private equity", Weight: 1.8},
			{Text: "spin-off", Weight: 1.6},
			{Text: "ftc", Weight: 1.6},
			{Text: "sec filing", Weight: 1.8},
			{Text: "corporate restructuring", Weight: 1.6},
			{Text: "headcount", Weight: 1.4},
			{Text: "earnings call", Weight: 1.6},
			{Text: "quarterly earnings", Weight: 1.6},
			{Text: "regulatory scrutiny", Weight: 1.6},
			{Text: "workforce reduction", Weight: 1.6},
			{Text: "shareholder", Weight: 1.2},
			{Text: "board of directors", Weight: 1.2},
			{Text: "startup", Weight: 1.2},
			{Text: "founder", Weight: 1.0},
			{Text: "entrepreneur", Weight: 1.0},
			{Text: "ceo", Weight: 1.0},
			{Text: "ceo departure", Weight: 1.8},
			{Text: "chief executive", Weight: 1.0},
			{Text: "conglomerate", Weight: 1.4},
			{Text: "multinational", Weight: 1.2},
			{Text: "franchise", Weight: 0.8},
			{Text: "brand loyalty", Weight: 1.2},
			{Text: "market cap", Weight: 1.4},
			{Text: "market share", Weight: 1.2},
			{Text: "profit margin", Weight: 1.2},
			{Text: "gross margin", Weight: 1.2},
			{Text: "operating income", Weight: 1.2},
			{Text: "fiscal year", Weight: 1.0},
			{Text: "quarterly report", Weight: 1.2},
			{Text: "business model", Weight: 1.0},
			{Text: "subscription revenue", Weight: 1.2},
			{Text: "consumer spending", Weight: 1.2},
			{Text: "e-commerce", Weight: 1.2},
			{Text: "warehouse", Weight: 0.7},
			{Text: "logistics", Weight: 0.8},
			{Text: "supply chain", Weight: 1.0},
			{Text: "revenue", Weight: 0.8},
			{Text: "executive", Weight: 0.6},
			{Text: "corporate", Weight: 0.5},
			{Text: "workforce", Weight: 0.6},
			{Text: "stock price", Weight: 0.7},
			{Text: "brand", Weight: 0.5},
			{Text: "retailer", Weight: 0.7},
			{Text: "tariff", Weight: 1.1},
			{Text: "tariffs", Weight: 1.1},
			{Text: "data center", Weight: 1.0},
			{Text: "business", Weight: 0.6},
			{Text: "meta", Weight: 1.2, Requires: []string{
				"facebook", "instagram", "whatsapp", "zuckerberg", "oculus",
				"threads", "quest", "reality labs", "meta platforms",
			}},
			{Text: "amazon", Weight: 1.4, Requires: []string{
				"aws", "bezos", "prime", "marketplace", "e-commerce",
				"warehouse", "logistics", "amazon web services",
			}},
			{Text: "tesla", Weight: 1.4, Requires: []string{
				"musk", "ev", "model 3", "model y", "gigafactory",
				"cybertruck", "autopilot", "electric vehicle",
			}},
		},
		Exclude: []classify.Term{
			{Text: "rainforest", Weight: 2.0},
			{Text: "nikola tesla", Weight: 3.0},
			{Text: "tesla coil", Weight: 3.0},
			{Text: "tesla's patents", Weight: 2.5},
		},
		MinScore: 0,
		Prompt: "Assign for corporate news: deals, funding, leadership, earnings, layoffs. Not " +
			"for market-wide finance (see Finance & Markets) and not for a product review with no " +
			"business angle.",
	}
}
