package i18n

// English copy for first-run setup (client/view/setup.go, §7.11).
//
// The screen a stranger sees before this instance is anybody's. Two things
// shape every string here.
//
// It has to say what the username IS, next to the field. It is an email
// address (internal/username has the rule), and a box labelled "Username" that
// then refuses anything without an @ is a form that hides its own requirement
// until it has been broken. And it has to say what the address is FOR, because
// this server has no mailer: an address collected under a form that looks like
// every other signup implies a reset link that will never arrive, and the
// moment a reader needs it is the moment they discover that. Saying so costs one
// line and buys the one thing recovery has to have, which is being believed.
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

		// The label carries the rule, so it is read with the field and not only
		// after the field has been refused.
		"username":            "Username (your email address)",
		"usernamePlaceholder": "you@example.com",
		// The honest version. It names the address as the sign-in name, and says
		// plainly that nothing is ever sent to it — "this server sends no mail" —
		// because an address on a signup form implies a reset link that will never
		// arrive. The codes on the next screen are the way back in.
		"usernameHint": "You sign in with this address. This server sends no mail, so it names the account and cannot reset it — the codes on the next screen are how you get back in.",
		"password":     "Password",
		// The rule, BEFORE it is broken. The server refuses anything under twelve
		// characters, and a requirement a person only meets by failing is a
		// requirement stated too late. "Length beats symbols" because it is true
		// and because it stops the reflex of adding a ! to a short one.
		"passwordHint": "At least 12 characters. Length beats symbols — a short phrase works.",
		"confirm":      "Password again",

		"submit":  "Claim this server",
		"working": "Setting up…",

		// One sentence per field that can be missing or wrong, each saying what to
		// type. They are shown beside the field they name (setup.go's faultAria),
		// so none of them repeats the field's own name at the front.
		"errUsernameEmpty": "Enter your email address — it is your username.",
		"errUsernameMail":  "A username has to be an email address, like you@example.com.",
		"errUsernameBare":  "Type just the address, with no name or spaces around it.",
		"errUsernameLong":  "That address is too long to be a username.",
		"errPasswordEmpty": "Choose a password for this server.",
		"errMismatch":      "Those two passwords are not the same.",
		// A refusal the server made, said with the name of the field it was
		// about: the sentence lands under a form of three boxes, and "That is too
		// common" does not say which one. {msg} is the server's own explanation.
		"errAt":      "{field}: {msg}",
		"atUsername": "Username",
		"atPassword": "Password",
		"errDial":    "Couldn't reach the server: {err}",
		// Two fallbacks, and both are about SETUP rather than signing in. The
		// first version of this screen borrowed the login screen's, so a password
		// the server refused for a stated reason came back as "Couldn't sign in.
		// Check the server is running" — which sent somebody to check a server
		// that was fine, about an action they were not taking.
		"errUnreachable": "The server stopped answering. It may still be starting up — try again in a moment.",
		"errGeneric":     "Couldn't set up this server. Try again, and if it keeps happening the server log will say why.",

		// --- the recovery codes -------------------------------------------
		"codesMark": "Save these",
		"codesLede": "Each of these signs you in once if you lose your password. This is the only time they are shown — the server keeps only a hash of them.",
		"codesAria": "Your recovery codes",
		// The consequence, stated plainly rather than as a warning icon: there
		// is no support desk on a self-hosted reader, and the person who has to
		// live with a lost code is the person reading this sentence.
		"codesWarn": "Nobody can recover this account for you. Put them somewhere that is not this computer.",
		"codesCopy": "Copy",
		// The button's word once the copy worked, and the sentence under it.
		"codesCopied":     "Copied",
		"codesCopiedNote": "On your clipboard. Paste them somewhere safe before you go on.",
		// The browser refused — usually a server reached over plain http,
		// where browsers withhold the clipboard. The remedy, not the cause.
		"codesCopyFailed": "This browser wouldn't copy them. Select the codes above and copy them yourself, or print them.",
		"codesDone":       "I've saved them — open my reader",
	})
}
