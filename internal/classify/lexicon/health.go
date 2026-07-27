package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// health is the Health & Medicine category (plan.md §27.3d #6).
//
// `stroke` and `depression` both have a common non-medical reading — a
// swimming or golf stroke, an economic depression — that the excludes below
// exist to catch. `who` (World Health Organization) is left out entirely
// rather than guarded: it is a stopword-adjacent pronoun, and no guard list
// is worth writing for a term that fires on nearly every sentence in the
// corpus.
func health() classify.Label {
	return classify.Label{
		Slug: "health",
		Name: "Health & Medicine",
		Terms: []classify.Term{
			{Text: "clinical trial", Weight: 2.4},
			{Text: "fda approval", Weight: 2.4},
			{Text: "chemotherapy", Weight: 2.2},
			{Text: "immunotherapy", Weight: 2.2},
			{Text: "antibiotic resistance", Weight: 2.2},
			{Text: "gene therapy", Weight: 2.2},
			{Text: "opioid crisis", Weight: 2.2},
			{Text: "epidemiology", Weight: 2.0},
			{Text: "outbreak", Weight: 2.0},
			{Text: "pandemic", Weight: 2.0},
			{Text: "biopsy", Weight: 2.0},
			{Text: "alzheimer's", Weight: 1.9},
			{Text: "vaccine", Weight: 1.9},
			{Text: "chronic illness", Weight: 1.8},
			{Text: "mental health", Weight: 1.8},
			{Text: "cdc", Weight: 1.8},
			{Text: "fda", Weight: 1.8},
			{Text: "insulin", Weight: 1.8},
			{Text: "antidepressant", Weight: 1.8},
			{Text: "overdose", Weight: 1.8},
			{Text: "mental illness", Weight: 1.6},
			{Text: "anxiety disorder", Weight: 1.6},
			{Text: "clinical study", Weight: 1.6},
			{Text: "mri", Weight: 1.6},
			{Text: "ct scan", Weight: 1.6},
			{Text: "cardiovascular", Weight: 1.6},
			{Text: "diabetes", Weight: 1.6},
			{Text: "dementia", Weight: 1.6},
			{Text: "ptsd", Weight: 1.6},
			{Text: "telehealth", Weight: 1.6},
			{Text: "nih", Weight: 1.6},
			{Text: "cancer", Weight: 1.5},
			{Text: "covid-19", Weight: 1.5},
			{Text: "diagnosis", Weight: 1.4},
			{Text: "psychiatrist", Weight: 1.4},
			{Text: "public health", Weight: 1.4},
			{Text: "clinical guidelines", Weight: 1.4},
			{Text: "blood pressure", Weight: 1.4},
			{Text: "obesity", Weight: 1.4},
			{Text: "tumor", Weight: 1.4},
			{Text: "autism", Weight: 1.4},
			{Text: "adhd", Weight: 1.4},
			{Text: "antibody", Weight: 1.4},
			{Text: "medical device", Weight: 1.4},
			{Text: "coronavirus", Weight: 1.4},
			{Text: "immune system", Weight: 1.2},
			{Text: "cholesterol", Weight: 1.2},
			{Text: "flu season", Weight: 1.2},
			{Text: "emergency room", Weight: 1.2},
			{Text: "patient care", Weight: 1.2},
			{Text: "symptom", Weight: 1.1},
			{Text: "surgeon", Weight: 1.0},
			{Text: "surgery", Weight: 1.0},
			{Text: "physician", Weight: 1.0},
			{Text: "therapy", Weight: 1.0},
			{Text: "medication", Weight: 1.0},
			{Text: "prescription", Weight: 1.0},
			{Text: "dosage", Weight: 1.0},
			{Text: "side effect", Weight: 1.0},
			{Text: "healthcare", Weight: 1.0},
			{Text: "nutrition", Weight: 0.9},
			{Text: "hospital", Weight: 0.8},
			{Text: "nurse", Weight: 0.8},
			{Text: "wellness", Weight: 0.7},
			{Text: "supplement", Weight: 0.6},
			{Text: "stroke", Weight: 1.3, Requires: []string{
				"patient", "brain", "hospital", "symptom", "artery", "blood clot", "cardiac",
			}},
			{Text: "depression", Weight: 1.1, Requires: []string{
				"mental health", "therapy", "antidepressant", "symptom", "diagnosis", "psychiatrist",
			}},
		},
		Exclude: []classify.Term{
			{Text: "master stroke", Weight: 2.0},
			{Text: "brush stroke", Weight: 2.0},
			{Text: "swimming stroke", Weight: 1.5},
			{Text: "golf stroke", Weight: 1.5},
			{Text: "great depression", Weight: 2.5},
			{Text: "economic depression", Weight: 2.5},
		},
		MinScore: 0,
		Prompt: "Assign for medicine, treatment, disease and public health. Not for wellness " +
			"lifestyle content with no medical claim (see Food & Drink or Sport), and not for " +
			"economic \"depression\" or a golf/swimming \"stroke\".",
	}
}
