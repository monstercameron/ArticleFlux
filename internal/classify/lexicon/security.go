package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// security is the Security & Privacy category (plan.md §27.3d #4).
//
// `security` on its own is the least useful word in this file — "national
// security", "job security", "social security" and "security guard" all
// outnumber the cybersecurity sense in a general feed, which is why the
// category is never anchored on the bare word and why those four phrases are
// excluded outright rather than merely under-weighted. `patch` is guarded
// against gaming's `patch notes` (§27.3d #16), which needs to win that
// collision on its own higher, unguarded weight rather than on this guard.
func security() classify.Label {
	return classify.Label{
		Slug: "security",
		Name: "Security & Privacy",
		Terms: []classify.Term{
			{Text: "ransomware", Weight: 2.8},
			{Text: "patch tuesday", Weight: 2.6},
			{Text: "zero-day", Weight: 2.6},
			{Text: "keylogger", Weight: 2.4},
			{Text: "rootkit", Weight: 2.4},
			{Text: "ddos", Weight: 2.4},
			{Text: "sql injection", Weight: 2.4},
			{Text: "supply chain attack", Weight: 2.4},
			{Text: "malware", Weight: 2.2},
			{Text: "phishing", Weight: 2.2},
			{Text: "spyware", Weight: 2.2},
			{Text: "botnet", Weight: 2.2},
			{Text: "credential stuffing", Weight: 2.2},
			{Text: "cross-site scripting", Weight: 2.2},
			{Text: "mitm", Weight: 2.0},
			{Text: "on path attack", Weight: 1.8},
			{Text: "session hijacking", Weight: 2.0},
			{Text: "backdoor", Weight: 2.0},
			{Text: "penetration testing", Weight: 2.0},
			{Text: "threat actor", Weight: 2.0},
			{Text: "identity theft", Weight: 2.0},
			{Text: "e2e encryption", Weight: 2.0},
			{Text: "cyberattack", Weight: 2.0},
			{Text: "data breach", Weight: 2.0},
			{Text: "breach", Weight: 1.9},
			{Text: "vulnerability", Weight: 1.8},
			{Text: "encryption", Weight: 1.8},
			{Text: "cryptography", Weight: 1.8},
			{Text: "dark web", Weight: 1.8},
			{Text: "brute force", Weight: 1.8},
			{Text: "social engineering", Weight: 1.8},
			{Text: "bug bounty", Weight: 1.8},
			{Text: "insider threat", Weight: 2.0},
			{Text: "zero trust", Weight: 2.0},
			{Text: "attack surface", Weight: 1.8},
			{Text: "exploit", Weight: 1.7},
			{Text: "spoofing", Weight: 1.6},
			{Text: "firewall", Weight: 1.6},
			{Text: "cybersecurity", Weight: 1.6},
			{Text: "password manager", Weight: 1.6},
			{Text: "mfa", Weight: 1.6},
			{Text: "incident response", Weight: 1.6},
			{Text: "nation-state", Weight: 1.5},
			{Text: "leaked credentials", Weight: 1.8},
			{Text: "exposed database", Weight: 1.8},
			{Text: "unauthorized access", Weight: 1.8},
			{Text: "two-factor authentication", Weight: 1.6},
			{Text: "tls", Weight: 1.5},
			{Text: "vpn", Weight: 1.4},
			{Text: "data privacy", Weight: 1.4},
			{Text: "security researcher", Weight: 1.4},
			{Text: "malicious actor", Weight: 1.4},
			{Text: "security patch", Weight: 1.4},
			{Text: "biometric", Weight: 1.2},
			{Text: "privacy policy", Weight: 1.1},
			{Text: "gdpr", Weight: 1.0},
			{Text: "hacker", Weight: 1.0},
			{Text: "hack", Weight: 0.7},
			{Text: "cve", Weight: 1.3},

			// CVE identifiers are unambiguous and worth more than the bare word.
			{Text: `\bcve-\d{4}-\d{4,}\b`, Weight: 2.6, Regex: true},

			// Bare `patch` is one of the most collision-prone words in the whole
			// taxonomy — gaming's "patch notes" (§27.3d #16) is a stronger,
			// unguarded signal and must win that fight on weight alone, so this
			// only fires alongside unambiguous security vocabulary.
			{Text: "patch", Weight: 1.0, Requires: []string{
				"cve", "vulnerability", "exploit", "security", "firmware",
				"critical", "zero-day", "hacker", "tuesday", "advisory",
			}},
		},
		Exclude: []classify.Term{
			// "Security" outside the cyber sense dwarfs the cyber sense in a
			// general feed. See the package comment.
			{Text: "national security", Weight: 3.0},
			{Text: "physical security", Weight: 3.0},
			{Text: "job security", Weight: 3.0},
			{Text: "social security", Weight: 3.0},
			{Text: "food security", Weight: 3.0},
			{Text: "security guard", Weight: 3.0},
			{Text: "security deposit", Weight: 3.0},
			{Text: "security clearance", Weight: 2.5},
			// Labor and human-rights sense of "exploit", not a software exploit.
			{Text: "worker exploitation", Weight: 3.0},
			{Text: "labor exploitation", Weight: 3.0},
			{Text: "exploit workers", Weight: 3.0},
		},
		MinScore: 0,
		Prompt: "Assign when the article is about the security OF systems: vulnerabilities, " +
			"breaches, defensive tooling, cryptography. Not for physical security, national " +
			"security policy, or job security.",
	}
}
