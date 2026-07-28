package i18n

// English copy for first-run setup (client/view/setup.go, §7.11).
//
// The screen a stranger sees before this instance is anybody's. Two things
// shape every string here.
//
// It has to say what the email is FOR, next to the field, because this server
// has no mailer: an address collected under a form that looks like every other
// signup implies a reset link that will never arrive, and the moment a reader
// needs it is the moment they discover that. Saying so costs one line and buys
// the one thing recovery has to have, which is being believed.
//
// And the recovery codes get their own step with its own words, because they
// exist in readable form exactly once. Copy that hurries somebody past them is
// copy that loses them.
func init() {
	text(DefaultLocale, "setup", map[string]string{
		"mark": "ArticleFlux",
		// Names what this is in one line: nobody owns this yet, and the next
		// thirty seconds decide who does.
		"lede": "Nobody has claimed this server yet. Set a password and it is yours.",

		"username": "Username",
		"email":    "Email (optional)",
		// The honest version. "For your records" rather than "for recovery",
		// because nothing here can send to it.
		"emailHint": "This server sends no mail — your address is stored to label the account, not to reset it. The codes on the next screen are how you get back in.",
		"password":  "Password",
		"confirm":   "Password again",

		"submit":  "Claim this server",
		"working": "Setting up…",

		"errEmpty":    "A username and a password, and this server is yours.",
		"errMismatch": "Those two passwords are not the same.",
		"errDial":     "Couldn't reach the server: {err}",

		// --- the recovery codes -------------------------------------------
		"codesMark": "Save these",
		"codesLede": "Each of these signs you in once if you lose your password. This is the only time they are shown — the server keeps only a hash of them.",
		"codesAria": "Your recovery codes",
		// The consequence, stated plainly rather than as a warning icon: there
		// is no support desk on a self-hosted reader, and the person who has to
		// live with a lost code is the person reading this sentence.
		"codesWarn": "Nobody can recover this account for you. Put them somewhere that is not this computer.",
		"codesCopy": "Copy",
		"codesDone": "I've saved them — open my reader",
	})
}
