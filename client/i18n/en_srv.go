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
		// The paging cursor no longer matches the list. Never an empty page:
		// a client reading that as "the end" silently truncates (§20.7).
		"staleCursor": "this page cursor is out of date; reloading the list",

		// --- §20.7's taxonomy (internal/apierr, TODO 7.3a)
		//
		// These are the refusals the shared taxonomy composes, so they are the
		// ones every transport sends. Note what is NOT here and never will be:
		// a message for a cross-tenant access. That resolves to `notFound`,
		// identical to a genuine miss, because a distinct message is the
		// tenant leak arriving through the translation layer.
		"permissionDenied": "you do not have permission to do that",
		// A limit that waiting fixes...
		"rateLimited": "too many requests; please slow down",
		// ...and one it does not. Same code on the wire, different remedy, so
		// the reader is told which of "wait" and "this is as much as you get"
		// applies rather than being left to guess from a retry that never works.
		"quotaExceeded": "you have reached a limit on this account",
		// A25: somebody else changed this between the read and the write. Named
		// as a fact about the data rather than as an error, because it is not
		// one — the client reloads and the reader carries on.
		"revConflict":         "this changed somewhere else; reloading",
		"idempotencyConflict": "this request was already used for something else",
		// Expired, spent and never-existed are deliberately one message: the
		// difference tells somebody holding a guess whether it named something
		// real.
		"inviteInvalid":     "that invitation is not valid",
		"resetTokenInvalid": "that reset link is not valid",
		// D12. An outage rather than a leak, and phrased so the reader knows it
		// is the server's problem and not their password.
		"ambiguousUser": "this account name exists in more than one workspace",
		// §22.8's breaker is open. "Paused" and "recovers" rather than "failed",
		// because it resumes on its own and a reader told something failed will
		// go looking for a setting to fix.
		"smartUnavailable": "Smart features are paused while the provider recovers",
		"noEncryptionKey":  "this server cannot store credentials until an encryption key is configured",

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
