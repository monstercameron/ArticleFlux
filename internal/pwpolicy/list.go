package pwpolicy

// common is the bundled list, keyed on the FOLDED form (see Fold).
//
// # What is in it and what is not
//
// These are entries in folded form, so each one covers a family rather than a
// string. "password" refuses P@ssw0rd, Password1!, PASSWORD123 and pa$$word.
// That is why a few hundred entries is a defensible size for a self-hosted
// instance and a full Pwned dump is not: the folding does the work that volume
// would otherwise have to.
//
// Entries shorter than MinLength are still worth listing. A twelve-character
// minimum does not stop "password1234" — folding removes the digits and what is
// left is "password", which is exactly the entry that catches it. Refusing to
// list short words because they cannot be typed alone would defeat the point.
//
// # What this does not claim
//
// It covers the head of the distribution. It does not know that one particular
// person's passphrase appeared in one particular breach, and nothing offline
// can. See the package comment for why that trade was taken.
var common = map[string]bool{}

func init() {
	for _, w := range list {
		common[Fold(w)] = true
	}
}

// list is written in plain form and folded at init, so it can be read and
// argued with. An entry that folds to the same key as another is harmless.
var list = []string{
	// The perennial top of every leaked-credential analysis.
	"password", "passwd", "pass", "secret", "letmein", "welcome", "admin",
	"administrator", "root", "toor", "guest", "user", "test", "demo", "default",
	"changeme", "temp", "login", "master", "access", "trustno1", "iloveyou",
	"sunshine", "princess", "dragon", "monkey", "shadow", "superman", "batman",
	"football", "baseball", "soccer", "hockey", "basketball", "starwars",
	"pokemon", "minecraft", "computer", "internet", "freedom", "whatever",
	"nothing", "anything", "something",

	// Keyboard walks that are not straight runs, so isSequence misses them.
	"qwerty", "qwertz", "azerty", "qazwsx", "qwer", "asdf", "zxcv", "1qaz2wsx",
	"qweasd", "qwertyui", "asdfghjk", "1q2w3e4r", "zaq12wsx", "qazxsw",

	// Words people reach for when told to pick something memorable.
	"summer", "winter", "spring", "autumn", "january", "february", "march",
	"april", "june", "july", "august", "september", "october", "november",
	"december", "monday", "friday", "chocolate", "coffee", "banana", "orange",
	"purple", "silver", "golden", "flower", "butterfly", "rainbow", "unicorn",
	"cheese", "pizza", "cookie", "peanut", "ginger", "pepper", "hunter",
	"ranger", "phoenix", "eagle", "falcon", "tiger", "panther", "cowboy",

	// Names and terms of endearment, which is a very large slice in every dump.
	"michael", "jennifer", "jessica", "ashley", "matthew", "joshua", "amanda",
	"daniel", "andrew", "charlie", "thomas", "robert", "george", "william",
	"richard", "nicole", "hannah", "taylor", "jordan", "harley", "buster",
	"sophie", "oliver", "muffin", "snoopy", "tigger", "bailey", "maggie",
	"honey", "baby", "angel", "sweetie", "darling", "loveyou", "ilovethe",

	// Application- and instance-specific, which a generic list will not have.
	"articleflux", "articlefluxpassword", "rssreader", "feedreader", "reader",
	"newsreader", "myfeeds", "myreader",

	// Structural favourites.
	"abc123", "123abc", "password123", "adminadmin", "passwordpassword",
	"letmein123", "welcome123", "admin123", "root123", "test123", "qwerty123",
	"iloveyou123", "changeme123", "nopassword", "nopass", "blank", "empty",
	"none", "null", "undefined", "example", "sample", "placeholder",

	// The "this is definitely long enough" family.
	"correcthorsebatterystaple", "thisismypassword", "mypasswordis",
	"passwordislong", "longpassword", "averylongpassword", "supersecret",
	"topsecret", "keepitsecret", "donttellanyone", "iforgotmypassword",
	"newpassword", "oldpassword", "temporarypassword", "temppassword",
	"defaultpassword", "initialpassword", "firstpassword",
}
