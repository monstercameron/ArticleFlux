package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// software is the Software & Development category (plan.md §27.3d #1).
//
// The hardest terms here are the ones that are also English words or other
// proper nouns: `go`, `rust`, `python`, `java`, `swift` all have a common
// non-software reading, and a lexicon that lets them score unguarded files
// every "Rust Belt manufacturing" piece and every zoo story under Software.
// `go` needs the strongest guard of the five because "go" unguarded is not a
// false positive, it is a stopword-adjacent verb that would fire on nearly
// every headline in the corpus.
func software() classify.Label {
	return classify.Label{
		Slug: "software",
		Name: "Software & Development",
		Terms: []classify.Term{
			{Text: "kubernetes", Weight: 2.6},
			{Text: "postgres", Weight: 2.2},
			{Text: "sqlite", Weight: 2.2},
			{Text: "typescript", Weight: 2.0},
			{Text: "golang", Weight: 2.0},
			{Text: "graphql", Weight: 2.0},
			{Text: "terraform", Weight: 2.0},
			{Text: "monorepo", Weight: 1.9},
			{Text: "open source", Weight: 1.8},
			{Text: "docker", Weight: 1.8},
			{Text: "kotlin", Weight: 1.8},
			{Text: "rust", Weight: 1.6, Requires: []string{
				"cargo", "crate", "borrow checker", "compiler", "memory safe",
				"programming language", "github", "code",
			}},
			{Text: "windows 11", Weight: 1.6},
			{Text: "memory leak", Weight: 1.6},
			{Text: "pull request", Weight: 1.6},
			{Text: "garbage collection", Weight: 1.5},
			{Text: "websocket", Weight: 1.5},
			{Text: "xcode", Weight: 1.5},
			{Text: "vue", Weight: 1.5},
			{Text: "compiler", Weight: 1.4},
			{Text: "javascript", Weight: 1.4},
			{Text: "react", Weight: 1.4},
			{Text: "microservice", Weight: 1.4},
			{Text: "npm", Weight: 1.4},
			{Text: "devops", Weight: 1.4},
			{Text: "github", Weight: 1.3},
			{Text: "swift", Weight: 1.3},
			{Text: "nosql", Weight: 1.3},
			{Text: "merge conflict", Weight: 1.3},
			{Text: "rest api", Weight: 1.3},
			{Text: "continuous integration", Weight: 1.3},
			{Text: "vscode", Weight: 1.3},
			{Text: "regex", Weight: 1.3},
			{Text: "android studio", Weight: 1.2},
			{Text: "refactor", Weight: 1.2},
			{Text: "unit test", Weight: 1.2},
			{Text: "test coverage", Weight: 1.2},
			{Text: "kernel", Weight: 1.2},
			{Text: "macos", Weight: 1.2},
			{Text: "ssh", Weight: 1.1},
			{Text: "git", Weight: 1.1},
			{Text: "java", Weight: 1.1, Requires: []string{
				"jvm", "spring", "kotlin", "android studio", "maven", "gradle",
				"bytecode", "compile", "spring boot",
			}},
			{Text: "linux", Weight: 1.1},
			{Text: "source code", Weight: 1.1},
			{Text: "package manager", Weight: 1.1},
			{Text: "changelog", Weight: 1.1},
			{Text: "concurrency", Weight: 1.0},
			{Text: "command line", Weight: 1.0},
			{Text: "python", Weight: 1.0, Requires: []string{
				"django", "flask", "pip", "numpy", "pandas", "interpreter",
				"programming language", "script", "compiler",
			}},
			{Text: "version control", Weight: 1.0},
			{Text: "programming language", Weight: 1.0},
			{Text: "webhook", Weight: 1.0},
			{Text: "beta release", Weight: 0.9},
			{Text: "dependency", Weight: 0.9},
			{Text: "algorithm", Weight: 0.9},
			{Text: "data structure", Weight: 0.9},
			{Text: "codebase", Weight: 0.9},
			{Text: "runtime", Weight: 0.9},
			{Text: "sql", Weight: 0.9},
			{Text: "yaml", Weight: 0.8},
			{Text: "container", Weight: 0.8},
			{Text: "ide", Weight: 0.8},
			{Text: "database", Weight: 0.8},
			{Text: "framework", Weight: 0.7},
			{Text: "api key", Weight: 0.7},
			{Text: "async", Weight: 0.7},
			{Text: "deploy", Weight: 0.7},
			{Text: "backend", Weight: 0.7},
			{Text: "frontend", Weight: 0.7},
			{Text: "bug fix", Weight: 0.7},
			{Text: "latency", Weight: 0.7},
			{Text: "api", Weight: 0.6},
			{Text: "sdk", Weight: 0.6},
			{Text: "json", Weight: 0.6},
			{Text: "software update", Weight: 0.6},
			{Text: "terminal", Weight: 0.5},

			// RFC numbers are unambiguous and worth more than any single word that
			// names the standard; matched title/summary/URL only per Term.Regex.
			{Text: `\brfc ?\d{3,5}\b`, Weight: 2.0, Regex: true},

			// `crypto` is finance's term first (§27.3d #8): cryptocurrency dominates
			// the word's use in a general news feed, so software gets it at a
			// secondary weight rather than contesting the primary read.
			{Text: "crypto", Weight: 0.7},

			// `go` alone is a stopword-adjacent verb ("go to the store", "here we
			// go") and would fire on a large fraction of any corpus. It only scores
			// alongside unambiguous companions from the same ecosystem.
			{Text: "go", Weight: 0.9, Requires: []string{
				"golang", "goroutine", "compiler", "kubernetes", "docker",
				"git", "package", "module", "api", "gopher",
			}},
		},
		Exclude: []classify.Term{
			// Rust Belt manufacturing and Midwest politics, not the language.
			{Text: "rust belt", Weight: 3.0},
			{Text: "rusted", Weight: 1.0},
			{Text: "rust stain", Weight: 1.5},
			// The snake, not the language.
			{Text: "python snake", Weight: 3.0},
			{Text: "ball python", Weight: 3.0},
			{Text: "burmese python", Weight: 3.0},
			// The island and its provinces, not the JVM language.
			{Text: "island of java", Weight: 3.0},
			{Text: "west java", Weight: 2.5},
			{Text: "east java", Weight: 2.5},
			{Text: "java sea", Weight: 2.5},
			// The singer, not the language.
			{Text: "taylor swift", Weight: 3.0},
		},
		MinScore: 0,
		Prompt: "Assign for the practice of building software: languages, tools, infrastructure, " +
			"version control, APIs. Not for a company's business results, nor for AI/ML research " +
			"specifically (see AI & Machine Learning).",
	}
}
