package i18n

// English copy for the credential screen (client/view/login.go).
//
// Values are byte-identical to the English they replaced, so the e2e text
// matchers that were written against the literals keep matching.
func init() {
	text(DefaultLocale, "login", map[string]string{
		// The wordmark is in the catalog rather than exempted as a brand
		// string. It costs one key, and it keeps the guard's answer for
		// client/view at zero rather than "zero except the ones we argued
		// about" — an allowlist with one entry acquires a second.
		"mark": "ArticleFlux",
		"lede": "Sign in to your reader.",

		"username": "Username",
		"password": "Password",

		"submit":  "Sign in",
		"working": "Signing in…",

		// {cmd} is the adduser command, rendered in a <code> chip. It is a
		// command line, not prose, so it stays out of the catalog and is
		// interpolated — a translator must not be able to change it.
		"footPrefix": "No account? Whoever runs this server creates one with ",

		"errEmpty": "Enter a username and a password.",
		// {err} is the transport's own text, appended when the dial itself
		// fails and there is no gRPC status to read.
		"errDial": "Can't reach the server: {err}",
		// Two failures that read identically to a person but arrive by
		// different routes: no socket at all, and a socket that died. Kept as
		// separate keys because a translator should be free to distinguish
		// them even where English does not.
		"errUnreachable": "Can't reach the server. Check it's running, then try again.",
		"errGeneric":     "Couldn't sign in. Check the server is running and try again.",
	})
}
