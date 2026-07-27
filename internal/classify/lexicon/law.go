package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// law is the Law & Courts category (plan.md §27.3d #11).
//
// `gdpr fine` is anchored here rather than in security: a regulator's
// enforcement action is a legal proceeding, and security's own copy of GDPR
// vocabulary (`gdpr`, `data privacy` in security.go) is deliberately about
// the technical practice, not the ruling.
func law() classify.Label {
	return classify.Label{
		Slug: "law",
		Name: "Law & Courts",
		Terms: []classify.Term{
			{Text: "indictment", Weight: 2.6},
			{Text: "gdpr fine", Weight: 2.4},
			{Text: "grand jury", Weight: 2.4},
			{Text: "cease and desist", Weight: 2.2},
			{Text: "plea deal", Weight: 2.2},
			{Text: "subpoena", Weight: 2.2},
			{Text: "injunction", Weight: 2.2},
			{Text: "mistrial", Weight: 2.2},
			{Text: "acquittal", Weight: 2.2},
			{Text: "extradition", Weight: 2.0},
			{Text: "supreme court ruling", Weight: 2.2},
			{Text: "wrongful termination", Weight: 2.0},
			{Text: "defamation", Weight: 2.0},
			{Text: "libel", Weight: 2.0},
			{Text: "jury trial", Weight: 2.0},
			{Text: "restraining order", Weight: 2.0},
			{Text: "class action", Weight: 2.0},
			{Text: "breach of contract", Weight: 2.0},
			{Text: "copyright infringement", Weight: 2.0},
			{Text: "patent lawsuit", Weight: 2.0},
			{Text: "trademark dispute", Weight: 2.0},
			{Text: "plea bargain", Weight: 2.0},
			{Text: "appellate court", Weight: 2.0},
			{Text: "supreme court", Weight: 1.8},
			{Text: "district court", Weight: 1.8},
			{Text: "deposition", Weight: 1.8},
			{Text: "arbitration", Weight: 1.8},
			{Text: "felony", Weight: 1.8},
			{Text: "misdemeanor", Weight: 1.8},
			{Text: "conviction", Weight: 1.8},
			{Text: "bail hearing", Weight: 1.8},
			{Text: "antitrust case", Weight: 1.6},
			{Text: "constitutional law", Weight: 1.8},
			{Text: "legal precedent", Weight: 1.8},
			{Text: "cross-examination", Weight: 2.0},
			{Text: "verdict", Weight: 1.8},
			{Text: "lawsuit", Weight: 1.6},
			{Text: "data protection law", Weight: 1.6},
			{Text: "litigation", Weight: 1.6},
			{Text: "defense attorney", Weight: 1.6},
			{Text: "plaintiff", Weight: 1.6},
			{Text: "sentencing", Weight: 1.6},
			{Text: "federal court", Weight: 1.6},
			{Text: "whistleblower", Weight: 1.6},
			{Text: "parole", Weight: 1.6},
			{Text: "tort", Weight: 1.6},
			{Text: "court order", Weight: 1.6},
			{Text: "intellectual property", Weight: 1.4},
			{Text: "testimony", Weight: 1.4},
			{Text: "defendant", Weight: 1.4},
			{Text: "warrant", Weight: 1.4},
			{Text: "legal battle", Weight: 1.4},
			{Text: "settlement", Weight: 1.4},
			{Text: "prosecution", Weight: 1.4},
			{Text: "gdpr", Weight: 1.2},
			{Text: "legal filing", Weight: 1.2},
			{Text: "courtroom", Weight: 1.2},
			{Text: "statute", Weight: 1.2},
			{Text: "damages", Weight: 1.0},
			{Text: "appeal", Weight: 1.0},
			{Text: "plea", Weight: 0.9},
			{Text: "judge", Weight: 0.7},
			{Text: "ruling", Weight: 0.8},
			{Text: "hearing", Weight: 0.5},
		},
		MinScore: 0,
		Prompt: "Assign for court proceedings and legal disputes: lawsuits, rulings, prosecutions, " +
			"regulatory enforcement. Not for legislation being written or debated (see Politics & " +
			"Policy).",
	}
}
