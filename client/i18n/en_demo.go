package i18n

// The demo build's one piece of extra copy (client/view/demo.go).
//
// It is in the catalog rather than inline for the reason everything else is:
// guard 5 does not have an exemption for "this string is only in one build", and
// it should not — a catalog with a hole in it is how the extraction work gets
// deferred forever (§22.16). The keys are cheap, and a translator who never
// sees the demo simply translates four short strings nobody asked them not to.
//
// Note what these do NOT do: apologise. The demo is not a broken version of the
// application, it is the application with an invented instance behind it, and
// the note says exactly that so somebody evaluating it knows which parts they
// are allowed to believe.
func init() {
	text(DefaultLocale, "demo", map[string]string{
		"title": "Demo",
		// Three claims, in the order somebody would ask them: what the data is,
		// where the server is, and what happens to what they do here.
		"blurb": "Invented articles, and the server is this browser tab. " +
			"Everything works — reading, marking, tagging, searching — and none of it is saved.",
		"source":  "Source code",
		"dismiss": "Hide this",
	})
}
