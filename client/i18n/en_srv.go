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

		// --- account recovery (§7.2)
		//
		// One message for an unknown username, a wrong code, a spent code, an
		// expired token and a deactivated account alike. Same discipline as
		// badCredentials and a sharper reason: telling these apart would report
		// which accounts exist AND which have recovery configured, which is a map
		// of where an attacker should spend their time.
		//
		// It names both kinds of credential because one screen accepts either,
		// and a reader who pasted a reset link should not be told about codes.
		"badRecovery": "that recovery code or reset link is not valid",
		// The password IS changed and every session IS revoked; only the sign-in
		// afterwards failed. Saying so is the difference between a reader
		// retrying the recovery — which will now fail, because the code is spent
		// — and simply logging in with the password they just chose.
		"recoveredNoSession": "your password was reset, but signing you in failed; sign in with the new password",

		// --- generic
		"internal": "internal error",
		// The request id, appended to whatever the refusal said (§22.11).
		//
		// This is the one string here the SERVER never sends, because it is not
		// a refusal — it is the reference that makes a refusal reportable. A
		// reader who can quote it turns "it said internal error", which is
		// unactionable, into one grep of the log.
		//
		// Shown for server-side failures only. A permission error or a stale
		// cursor is a thing the reader can act on themselves, and a hex string
		// after it is noise.
		"reference":  "{message} (reference {id})",
		"notFound":   "not found",
		"saveFailed": "couldn't save that",
		// The paging cursor no longer matches the list. Never an empty page:
		// a client reading that as "the end" silently truncates (§20.7).
		"staleCursor": "this page cursor is out of date; reloading the list",
		// A timestamp field the server could not read. Names the format rather
		// than saying "invalid", because the caller here is a client author or
		// the sync API, and the thing they can DO about it is send RFC3339.
		"badTimestamp": "that is not a time this server can store — send an RFC3339 timestamp",

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
		// go looking for a setting to fix. Names Smart+ specifically: the
		// deterministic half — ranking, the lexicon, the hue tint, discovery
		// rungs 1-2 — is unaffected by an OpenAI outage and keeps working, and
		// "Smart features" would tell the reader everything derived has stopped.
		"smartUnavailable": "Smart+ features are paused while the provider recovers",
		"noEncryptionKey":  "this server cannot store credentials until an encryption key is configured",
		// §22.10. Never actually rendered from this key in the normal path —
		// the client classifies the sentinel in the message and shows its own
		// Reload prompt — but registered because the fallback path exists and a
		// reader must never see "srv.clientTooOld". The English is what a curl
		// and the sync API get.
		"clientTooOld": "refusing: articleflux:client-too-old",

		// --- migration (F1)
		//
		// Three refusals about the FILE rather than about the import, because
		// that is what the reader can act on: all three are fixed by picking a
		// different file, and none of them means the import is broken. Named
		// separately for the usual reason — "not an OPML list" sends somebody
		// back to their old reader's export screen, and "too large" does not.
		"opmlEmpty":   "that file was empty",
		"opmlTooBig":  "that file is too large to import — a subscription list is normally a few dozen kilobytes",
		"opmlNotOPML": "that file is not an OPML subscription list — export one from your old reader and pick that",

		// --- Smart+
		"badApiKeyShape":     "that does not look like an OpenAI API key — they begin with sk-",
		"saveModelFailed":    "couldn't save the model",
		"saveClassifyFailed": "couldn't save that setting",
		// A limit, not a balance: 0 is how you say "no limit", and the message
		// says so because the alternative reading — that 0 forbids everything —
		// is the one somebody reaches for when a negative number is refused.
		"badBudget":            "a spend limit cannot be negative — use 0 for no limit",
		"saveBudgetFailed":     "couldn't save the spend limit",
		"cannotStoreSecret":    "this server has no encryption key, so it will not store a credential — set OPENAI_API_KEY in the environment instead",
		"smartNoKey":           "Smart+ needs an OpenAI API key — add one in Settings › Smart+",
		"translateFailed":      "couldn't translate the interface just now",
		"translationTruncated": "the translation was cut off before it finished — try again",
		// {locale} is the code that was asked for, e.g. "fr".
		"unsupportedLanguage":   "“{locale}” is not one of the offered languages",
		"alreadySourceLanguage": "the interface is already in English",

		// --- theming (§20.16.3)
		//
		// Four refusals, and each one is here rather than folded into a generic
		// failure because the remedy differs. "Try again" genuinely works for a
		// truncated or malformed answer and is useless when there is no key; "pick a
		// theme again" is the fix for a base this server will not read and would
		// never be guessed from "invalid argument".
		"themeNoPrompt":    "describe the theme you want in a few words",
		"themeTruncated":   "the palette was cut off before it finished — try again",
		"themeNotAPalette": "that came back as something other than a palette — try again",
		"themeFailed":      "couldn't make a theme just now",
		"themeBadBase":     "the theme you are using could not be read",

		// §7.5's authorization map. "Not allowed" rather than "denied", and the
		// same words whether the method is unknown to the map or the role is too
		// low: a message that distinguishes them lets a caller map the server's
		// own policy one refusal at a time.
		"notAllowed": "your account is not allowed to do that",

		// --- first-run setup (§7.11)
		//
		// Byte-identical to the fallbacks in grpcsrv/setup.go, as the paired test
		// requires. alreadySetUp is a refusal somebody reaches by bookmarking the
		// setup URL or by racing a second browser, so it says what is true and
		// what to do rather than reporting a failure.
		"setupUsername": "choose a username of at least two characters",
		"setupEmail":    "that does not look like an email address",
		"alreadySetUp":  "this instance already has an account; sign in instead",

		// --- sudo mode (§7.3)
		//
		// A password confirmation in front of the few actions that change who can
		// get in. Each of these is a refusal the reader can act on, which is the
		// bar for having its own string: "sign in first" and "that password is
		// wrong" lead to different next moves, and one word for both would send
		// somebody to the wrong one.
		"sudoRequired":    "this needs your password again",
		"noSession":       "sign in first",
		"badPassword":     "that password is not right",
		"tooManyAttempts": "too many attempts; wait a minute and try again",
		// Said when a NEW password is refused rather than when an old one is
		// wrong, so it names the requirement instead of the failure.
		"weakPassword": "choose a longer password, or one that is not in the known-password list",

		// --- an instance built without the optional wiring
		"noObservability": "observability not wired",
		"noLogBuffer":     "log buffer not wired",
	})
}
