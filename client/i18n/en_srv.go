package i18n

// Refusals the SERVER composes, translated on the client.
//
// The server cannot translate these itself: the interface language is a
// per-device choice held in the browser's localStorage, and the server never
// sees it. So it sends a key plus arguments in an `articleflux.v1.ErrorDetail`
// attached to the gRPC status, and this is where they resolve.
//
// The status message still carries the English, and that is not redundancy —
// it is what the two consumers with no catalog get: the Google Reader-compatible
// sync API (§20.7) and anyone reading the error off a curl. The values here are
// byte-identical to those fallbacks, so the two cannot say different things.
//
// Rule for adding one: a refusal names what the reader can DO about it, or says
// plainly that there is nothing. "Permission denied" sends someone hunting for
// a bug; "only an administrator can change this" sends them to whoever runs the
// server.
func init() {
	text(DefaultLocale, "srv", map[string]string{
		// --- auth
		"badCredentials":   "invalid username or password",
		"notAuthenticated": "not authenticated",
		"notSignedIn":      "not signed in",
		"adminOnly":        "only an administrator can change Smart+ settings",

		// --- generic
		"internal":   "internal error",
		"notFound":   "not found",
		"saveFailed": "couldn't save that",

		// --- Smart+
		"badApiKeyShape":       "that does not look like an OpenAI API key — they begin with sk-",
		"saveModelFailed":      "couldn't save the model",
		"cannotStoreSecret":    "this server has no encryption key, so it will not store a credential — set OPENAI_API_KEY in the environment instead",
		"smartNoKey":           "Smart+ needs an OpenAI API key — add one in Settings › Smart+",
		"translateFailed":      "couldn't translate the interface just now",
		"translationTruncated": "the translation was cut off before it finished — try again",
		// {locale} is the code that was asked for, e.g. "fr".
		"unsupportedLanguage":   "“{locale}” is not one of the offered languages",
		"alreadySourceLanguage": "the interface is already in English",

		// --- an instance built without the optional wiring
		"noObservability": "observability not wired",
		"noLogBuffer":     "log buffer not wired",
	})
}
