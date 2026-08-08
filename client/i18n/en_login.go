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

		// --- account recovery (§7.2)
		//
		// The entry point is phrased as the thing the reader has ("a recovery
		// code"), not as the thing they lost ("forgot your password?"). Somebody
		// who kept the sheet needs to recognise that this is the screen for it;
		// somebody who did not is told below, in one sentence, that their admin
		// is the remaining route rather than being left to guess.
		"recoverLink": "Use a recovery code",
		"backToLogin": "Back to sign in",

		"recoverTitle": "Get back in",
		// Says what will happen, because both consequences surprise people: the
		// code stops working, and every other signed-in device is signed out.
		// Someone recovering an account usually suspects a thief, so the second
		// is reassurance rather than a warning — but only if it is said first.
		"recoverLede": "Enter one code from your recovery sheet and choose a new password. " +
			"The code is used up, and every device signed in to this account is signed out.",

		// One field for both credentials. The reader pastes whatever they have
		// and the client routes on shape (authn.LooksLikeRecoveryCode) — asking
		// somebody already locked out to classify their own credential is asking
		// them to get it wrong for no reason.
		"recoverCode":     "Recovery code or reset link",
		"recoverCodeHint": "Dashes and capitals don't matter.",
		"recoverPassword": "New password",

		"recoverSubmit":  "Reset password and sign in",
		"recoverWorking": "Resetting…",

		"errRecoverEmpty": "Enter your recovery code and a new password.",
		// A username is needed for a code and not for a reset link, so this only
		// ever fires on the code path. Saying which one is missing beats a
		// generic "fill in the form" on a screen with three fields.
		"errRecoverNoUser": "Enter the username for the account you're recovering.",

		// The confirmation screen, which holds between a spent code and the
		// reader. It exists because the count below used to be written into a
		// card that was replaced in the same frame — nobody ever read it.
		//
		// "You're back in" rather than "Success": the reader arrived here
		// locked out, and the sentence that matters is the one about the state
		// they are in now, not about the operation that got them there.
		"recoveredTitle": "You're back in",
		// The second half of what just happened, and the half that is easy to
		// mistake for a fault: every other session was ended. Said here rather
		// than only in the lede before the fact, because a device that stops
		// working an hour from now is remembered as a bug unless this sentence
		// was read.
		"recoveredHint":     "Your password is changed and every other device has been signed out.",
		"recoveredContinue": "Continue",

		// Shown after a successful recovery, before the reader is handed to the
		// app. {n} is how many codes are left on the sheet.
		//
		// The number is the entire signal that it is time to print a new sheet.
		// "Recovery codes are configured" tells nobody anything; "2 left" is what
		// makes somebody act, and the moment they have just used one is the only
		// moment they are thinking about it.
		"recoverRemaining": "{n} recovery codes left. Generate a new sheet in Settings when you run low.",
		"recoverLastCode":  "That was your last recovery code. Generate a new sheet in Settings now.",
	})
}
