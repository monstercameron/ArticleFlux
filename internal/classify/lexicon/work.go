package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// work is the Work & Careers category (plan.md §27.3d #25).
//
// `interview` is guarded because its dominant sense elsewhere in the corpus
// is the genre word — an interview with a director, a scientist, an
// athlete — not a hiring process. `resume` is guarded for a different
// reason: unguarded it is mostly the verb "to resume", not the noun.
func work() classify.Label {
	interviewCompanions := []string{
		"job", "hiring", "candidate", "hr", "panel", "offer",
		"interviewer", "screening", "onboarding", "resume",
	}
	return classify.Label{
		Slug: "work",
		Name: "Work & Careers",
		Terms: []classify.Term{
			{Text: "collective bargaining", Weight: 2.4},
			{Text: "quiet quitting", Weight: 2.2},
			{Text: "four-day workweek", Weight: 2.2},
			{Text: "noncompete clause", Weight: 2.2},
			{Text: "exit interview", Weight: 2.2},
			{Text: "union vote", Weight: 2.0},
			{Text: "return to office", Weight: 2.0},
			{Text: "worker strike", Weight: 2.0},
			{Text: "picket line", Weight: 2.0},
			{Text: "hiring freeze", Weight: 2.0},
			{Text: "severance package", Weight: 2.0},
			{Text: "performance review", Weight: 2.0},
			{Text: "salary negotiation", Weight: 2.0},
			{Text: "workplace harassment", Weight: 2.0},
			{Text: "gig economy", Weight: 2.0},
			{Text: "remote work", Weight: 2.0},
			{Text: "job interview", Weight: 1.9},
			{Text: "career ladder", Weight: 1.8},
			{Text: "cover letter", Weight: 1.8},
			{Text: "hybrid work", Weight: 1.8},
			{Text: "burnout", Weight: 1.8},
			{Text: "labor union", Weight: 1.8},
			{Text: "parental leave", Weight: 1.8},
			{Text: "pay gap", Weight: 1.8},
			{Text: "talent acquisition", Weight: 1.8},
			{Text: "skills gap", Weight: 1.8},
			{Text: "office politics", Weight: 1.6},
			{Text: "job posting", Weight: 1.6},
			{Text: "job market", Weight: 1.6},
			{Text: "workplace culture", Weight: 1.6},
			{Text: "career change", Weight: 1.6},
			{Text: "employee benefits", Weight: 1.6},
			{Text: "paid leave", Weight: 1.6},
			{Text: "work-life balance", Weight: 1.6},
			{Text: "freelance work", Weight: 1.6},
			{Text: "hiring manager", Weight: 1.6},
			{Text: "job hunt", Weight: 1.6},
			{Text: "resume", Weight: 1.4, Requires: []string{
				"job", "hiring", "candidate", "cv", "application", "cover letter",
			}},
			{Text: "human resources", Weight: 1.4},
			{Text: "onboarding", Weight: 1.4},
			{Text: "promotion", Weight: 1.4},
			{Text: "job offer", Weight: 1.4},
			{Text: "professional development", Weight: 1.4},
			{Text: "minimum wage", Weight: 2.0},
			{Text: "job security", Weight: 1.8},
			{Text: "labor shortage", Weight: 1.8},
			{Text: "employee turnover", Weight: 1.8},
			{Text: "notice period", Weight: 1.6},
			{Text: "labor market", Weight: 1.6},
			{Text: "workplace safety", Weight: 1.6},
			{Text: "internship program", Weight: 1.6},
			{Text: "contract worker", Weight: 1.4},
			{Text: "pay transparency", Weight: 1.6},
			{Text: "candidate", Weight: 1.0},
			{Text: "layoff", Weight: 1.2},
			{Text: "workplace", Weight: 0.8},
			{Text: "employer", Weight: 0.6},
			{Text: "employee", Weight: 0.6},
			{Text: "manager", Weight: 0.6},
			{Text: "interview", Weight: 1.2, Requires: interviewCompanions},
		},
		Exclude: []classify.Term{
			{Text: "interview with director", Weight: 2.5},
			{Text: "in an interview", Weight: 1.0},
		},
		MinScore: 0,
		Prompt: "Assign for employment and the workplace: hiring, careers, labor conditions, " +
			"management. Not for a Q&A-style interview as a journalistic format (that is a genre, " +
			"not a topic), and not for macro unemployment data (see Finance & Markets).",
	}
}
