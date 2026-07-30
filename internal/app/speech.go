package app

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// ttsPrefKey is the per-user opt-in. Absent or anything but "true" means off.
//
// Default-off is the whole point: this endpoint sends the article you are
// reading to a third party, and a feature like that must never become active
// because of a default someone forgot to change.
const ttsPrefKey = "tts.smartPlus"

// digestPrefKey turns a long article into about a minute of spoken summary
// instead of the whole thing (§10.7).
//
// A SECOND opt-in rather than a mode of the first, and default off like it, for
// the same reason: it is a second egress and a second bill. Smart+ voice sends
// the article to have it spoken; this also sends it to have it read and
// rewritten. Someone who consented to the first has not thereby consented to
// the second, and a reader watching their usage should be able to tell the two
// apart on the settings screen.
const digestPrefKey = "tts.digest"

// podcastPrefKey rewrites each article as one slot of a continuous broadcast,
// handing over from whatever was played before it (§19).
//
// A THIRD opt-in, default off, for the reason the second one is: it is a
// separate egress and a separate bill. It also OUTRANKS the digest rather than
// combining with it — see speechScript — because both replace the article text
// and there is no meaningful "a digest of a broadcast segment".
const podcastPrefKey = "tts.podcast"

// prevItemParam names the story that was just played, so a broadcast segment can
// hand over from it.
//
// A query parameter rather than something inside the sealed ticket, which is the
// obvious alternative and is wrong: the ticket is minted by GetItem, at a moment
// when nobody knows what the reader will play this after. The order is decided by
// the client, at play time, and can differ between two listens of the same feed.
//
// It is not a security hole for it to be caller-supplied. The id is resolved
// through the SAME SCOPE as the item being spoken (see serveSpeech), so a caller
// can only name a story they were already allowed to read, and the worst a forged
// value achieves is a handover from an article of their own choosing — which is
// indistinguishable from having played it.
const prevItemParam = "p"

// podcastVibePrefKey is how the narrator sounds: calm, brisk, dry or warm.
//
// A preference rather than a mode, because it changes nothing about what is
// sent, what is spent or what is claimed — only the manner. An unknown value
// resolves to the default inside smart.VibeFor rather than reaching the prompt.
const podcastVibePrefKey = "tts.podcastVibe"

// The opening's parameters, sent by the client on the FIRST segment of a session
// and absent on every other.
//
// `now` carries the LISTENER'S clock, with its offset, and that is the point:
// this server may be three timezones from the person listening to it, and being
// wished good morning at ten at night is the single most obviously wrong thing
// this feature could say. `n` is how many stories are queued, which is what
// turns "here's what's happening" into "eleven stories this morning" — the
// version that tells someone whether to settle in.
//
// Both are hints, not credentials. A forged `now` changes a greeting; a forged
// `n` changes a number in one sentence. Neither reaches anything but the prompt.
const (
	openNowParam     = "now"
	openStoriesParam = "n"
	// openLineupParam is the first few story IDs, comma-separated, for the
	// headline run-through the broadcast opens with.
	//
	// IDs rather than the headlines themselves, and that is a privacy decision
	// rather than a size one. A GET's query string lands in the access log, in
	// the browser's history and in any referrer; the reader's article titles are
	// their reading, and §22.11 keeps request content out of shared logs. An
	// opaque id says nothing to anyone who cannot already resolve it, and the
	// server resolves it through the SAME SCOPE as everything else here — so
	// this can only ever name stories the reader was already allowed to read.
	openLineupParam = "q"
	// introParam splits the opening off into its own recording.
	//
	// Three states, and the third is the one that matters:
	//
	//	"1"      this request IS the opening, and covers no story
	//	"0"      the opening has already been recorded; do not attach one here
	//	absent   the old behaviour — the opening rides on the first segment
	//
	// The split exists for the sound rather than the words. The broadcast opens
	// over music which has to swell and clear before the first story, and that
	// can only be timed against the END of a file — so the greeting has to be a
	// file. The "0" state is what stops the listener being greeted twice.
	introParam = "i"
	introOnly  = "1"
	introDone  = "0"
	// introClose asks for the SIGN-OFF alone: the programme is over, this
	// recording ends it, and it covers no story (§19).
	//
	// A third value on the existing parameter rather than a parameter of its
	// own, because it answers the same question every other value here answers —
	// where in the programme this request sits — and a second flag would make
	// "i=1&c=1" expressible, which is a request with no meaning.
	introClose = "2"
)

// wantsOpening decides whether this request gets a greeting attached.
//
// A pure function of the two things that decide it, because the rule reads as
// two negatives in a row at the call site and the failure it prevents — the
// listener being greeted twice in one broadcast — is silent in every test that
// does not know to listen for it.
//
// A story with something before it is never the top of the show. A request that
// says the greeting has already been recorded is never the top of the show
// either, and that is the whole point of introDone: in a split broadcast the
// first story genuinely has no predecessor, so nothing else about it would give
// the server the hint.
func wantsOpening(intro string, hasPrev bool) bool {
	return !hasPrev && strings.TrimSpace(intro) != introDone
}

// isIntroRequest is true when the caller asked for the greeting ALONE.
//
// Broadcast mode gates it: without the writer there is no greeting to record,
// and a flag that meant something in one mode and nothing in another would be a
// request that quietly returns the article when the client thinks it asked for
// an opening.
func isIntroRequest(intro string, podcast bool) bool {
	return podcast && strings.TrimSpace(intro) == introOnly
}

// isCloseRequest is true when the caller asked for the SIGN-OFF alone.
//
// Gated on broadcast mode for the reason isIntroRequest is: without the writer
// there is no programme to end, and a flag that meant something in one mode and
// nothing in another would be a request that quietly returns the article when
// the client thinks it asked for a goodbye.
func isCloseRequest(intro string, podcast bool) bool {
	return podcast && strings.TrimSpace(intro) == introClose
}

// openingFrom builds the top-of-broadcast greeting, or nil when this is not the
// first segment.
//
// Nil for anything but a first segment, and that is decided by the CALLER having
// no previous story — the parameters are only read when there is nothing to hand
// over from, so a client that sends them on every request still gets one opening
// per broadcast.
//
// A missing or unparseable `now` falls back to the server's own clock rather
// than dropping the greeting. The greeting is the shape of the thing; being an
// hour out on "afternoon" is a small wrongness, and having no opening at all is
// a missing feature.
func (a *App) openingFrom(ctx context.Context, sc store.Scope, r *http.Request,
	now time.Time) *smart.Opening {
	if v := strings.TrimSpace(r.URL.Query().Get(openNowParam)); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			now = t
		}
	}
	o := &smart.Opening{PartOfDay: partOfDay(now.Hour()), Date: now.Format("Monday, 2 January 2006")}
	if n, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(openStoriesParam))); err == nil &&
		n > 0 && n <= 10000 {
		o.Stories = n
	}
	o.Lineup = a.lineupFrom(ctx, sc, r.URL.Query().Get(openLineupParam))
	return o
}

// lineupFrom resolves the headline run-through from a list of item ids.
//
// Every one goes through the reader's own scope, so this can only ever name
// stories they were already allowed to read — which is what makes a
// caller-supplied list safe, and it is the same argument prevItemParam makes.
//
// An id that does not resolve is SKIPPED rather than failing the request. A
// run-through is a nicety on top of a greeting; refusing to speak because one
// story in a list of five had been swept away would be a silence caused by
// something that is not the point.
func (a *App) lineupFrom(ctx context.Context, sc store.Scope, raw string) []smart.Headline {
	raw = strings.TrimSpace(raw)
	if raw == "" || a.repo == nil {
		return nil
	}
	out := make([]smart.Headline, 0, smart.MaxLineup)
	for _, id := range strings.Split(raw, ",") {
		if len(out) >= smart.MaxLineup {
			break
		}
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 64 {
			continue
		}
		it, err := a.repo.GetItem(ctx, sc, id)
		if err != nil {
			continue
		}
		if strings.TrimSpace(it.Title) == "" {
			// A story with no headline cannot be run through, and "and something
			// from Hacker News" is not a headline.
			continue
		}
		out = append(out, smart.Headline{Source: it.SourceTitle, Title: it.Title})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// partOfDay is the word a greeting uses.
//
// Three, not four: "good night" is a farewell in English rather than a greeting,
// so a broadcast starting at one in the morning says "good evening" — which is
// what a person on air actually says at that hour, and what someone listening at
// that hour expects to hear.
//
// The boundaries are the conventional ones and deliberately not clever: noon and
// six. A listener at 5:59pm hearing "good afternoon" is not wrong, and a
// sunset-aware greeting would be a geolocation lookup for a single word.
func partOfDay(hour int) string {
	switch {
	case hour < 12:
		return "morning"
	case hour < 18:
		return "afternoon"
	default:
		return "evening"
	}
}

// speaker is everything the listening path needs from the voice.
//
// An interface rather than *tts.Client, and the reason is that the interesting
// half of this file was untestable without it. `/speech` is four gates, three
// script modes, a fallback chain and a cache key, and every one of those is
// reached only by a request that ends in a paid call to OpenAI — so the tests
// stopped at the gates and the part that actually produces sound was covered by
// nothing. A two-method seam turns "does broadcast mode return audio" into a
// question a test can ask.
//
// Deliberately narrow: it is what serveSpeech calls, not what tts.Client offers.
// The spend meter still takes the concrete client (see the wiring in app.go),
// because reporting usage is a different job from making it.
type speaker interface {
	Configured(ctx context.Context) bool
	Speak(ctx context.Context, key, text, model, voice string) ([]byte, error)
}

// digestFor returns the spoken summary of an item.
//
// Split out of the handler so the fallback logic there stays legible: every
// error from here is recoverable by reading the article itself, and the handler
// says so in one place.
func (a *App) digestFor(ctx context.Context, it store.Item) (string, error) {
	if a.digest == nil {
		return "", smart.ErrNothingToSummarise
	}
	// The article's own text, already stripped of markup — the same thing the
	// voice would otherwise read. Sending the raw HTML instead would bill for
	// `<div class="wp-block-group">` at the summariser as well as at the voice.
	body := speechBody(it)
	return a.digest.Speakable(ctx, it.ID, it.SourceTitle, it.Title, body)
}

// podcastFor returns this item's slot in the broadcast, handing over from prev.
//
// prev is the zero Item when there is nothing before this one — the top of a
// session, or a story the reader jumped to — and that is a supported case rather
// than a degraded one: the opening segment of a broadcast is a different piece of
// writing, and smart.Segment says so explicitly rather than leaving it to be
// inferred from an absence.
//
// Split out beside digestFor for the same reason that one is: every error from
// here is recoverable by reading the article, and the handler says so in one
// place.
func (a *App) podcastFor(ctx context.Context, it store.Item, prev store.Item,
	vibe string, open *smart.Opening, opened bool) (string, error) {
	if a.podcast == nil {
		return "", smart.ErrNothingToSummarise
	}
	return a.podcast.Segment(ctx, smart.Segment{
		ItemID: it.ID,
		Source: it.SourceTitle,
		Title:  it.Title,
		Vibe:   vibe,
		Open:   open,
		Opened: opened,
		// The article stripped of markup, like the digest gets: the model is
		// being asked to retell it, not to read our HTML, and the provider bills
		// per character either way.
		Body:       speechBody(it),
		PrevID:     prev.ID,
		PrevSource: prev.SourceTitle,
		PrevTitle:  prev.Title,
	})
}

// podcastIntro writes the top of the broadcast on its own: the greeting, the
// date, and the run-through of what is coming.
//
// It takes the first item only to be keyed against something stable — the
// opening covers no story, and the item's own text is never sent.
func (a *App) podcastIntro(ctx context.Context, it store.Item, vibe string,
	open *smart.Opening) (string, error) {
	if a.podcast == nil {
		return "", smart.ErrNothingToSummarise
	}
	return a.podcast.Segment(ctx, smart.Segment{
		ItemID:   it.ID,
		Source:   it.SourceTitle,
		Title:    it.Title,
		Vibe:     vibe,
		Open:     open,
		OpenOnly: true,
	})
}

// podcastOutro writes the sign-off on its own: the programme is over.
//
// `it` is the LAST story covered, and it is here to be two things — the cache
// key, and the headline the close can land on. Its body is never sent: there is
// nothing left to cover, and a model handed an article will cover it.
//
// The opening is passed through for its story COUNT alone (see writeClosing).
// Nil is fine and ordinary — a listener who joined mid-programme was never
// greeted, and the broadcast still has to be allowed to end.
func (a *App) podcastOutro(ctx context.Context, it store.Item, vibe string,
	open *smart.Opening) (string, error) {
	if a.podcast == nil {
		return "", smart.ErrNothingToSummarise
	}
	return a.podcast.Segment(ctx, smart.Segment{
		ItemID: it.ID,
		Vibe:   vibe,
		Open:   open,
		// The last story goes in the PREV fields rather than the current ones,
		// which is not a quirk: from the sign-off's point of view every story is
		// behind it, and the prompt's vocabulary for "the story you have just
		// finished covering" is already exactly that.
		PrevID:     it.ID,
		PrevSource: it.SourceTitle,
		PrevTitle:  it.Title,
		CloseOnly:  true,
	})
}

// podcastKey names an audio recording by the PAIR of articles it covers, not by
// the article.
//
// The same story after a different story is a different recording, because the
// segment opens by handing over from what came before it. Keying the audio on
// the item alone would serve one for the other — and the result does not sound
// like a bug, it sounds like the narrator misremembering what was just said,
// which is far worse.
//
// A function rather than a concatenation at the call site because it is the
// half of the contract that lives HERE: smart.Podcast keys its text cache on the
// same pair, and the two have to agree about what "the same segment" means.
// The vibe and the opening are in the key for the same reason they are in
// smart.Podcast's own: they change the words, so they change the recording. A
// reader who switches from calm to brisk and hears yesterday's calm audio would
// conclude, reasonably, that the setting does nothing.
//
// The opening contributes its DATE rather than its whole shape, because that is
// what makes it a different broadcast — the same show restarted an hour later
// should cost nothing, and tomorrow's should be new.
func podcastKey(itemID, prevID, vibe string, open *smart.Opening) string {
	key := itemID + "#podcast:" + prevID + ":" + vibe
	if open != nil {
		key += ":open:" + open.PartOfDay + ":" + open.Date + ":" + strconv.Itoa(open.Stories)
	}
	return key
}

// speechScript decides WHAT gets read aloud, and what the audio is cached under.
//
// The two answers travel together because they cannot be allowed to disagree:
// the audio cache is keyed by the second, so a mode that changed the text and not
// the key would serve yesterday's rendering of a different script — which looks
// exactly like the preference silently not working, and is the bug the digest's
// `#digest` suffix already exists to prevent.
//
// Three modes, in a strict order of precedence rather than in combination:
//
//	podcast   the article as one slot of a running broadcast (§19)
//	digest    about a minute of spoken summary (§10.7)
//	neither   the article itself
//
// Podcast outranks digest because both REPLACE the article text and there is no
// coherent "summary of a broadcast segment" — the segment is already the short
// form. A reader with both switched on gets the one that subsumes the other.
//
// Every failure below falls through to the next-simplest thing rather than
// reporting. That is the same policy the digest has always had and it matters
// more here: a listener whose narrator falls over should hear the article, which
// is less pleasant than what they asked for and infinitely better than silence.
func (a *App) speechScript(ctx context.Context, prefs map[string]string,
	it store.Item, prev store.Item, open *smart.Opening, intro, opened, closing bool) (text, cacheKey string) {
	text, cacheKey = speechText(it), it.ID

	// The sign-off, on its own, after every story. Checked FIRST because it is
	// the one mode that is not a rendering of the item at all — the item is
	// here to be a cache key and a headline, and falling through to any of the
	// paths below would read out an article the listener has already heard.
	//
	// A failure DOES NOT fall through. That is the opposite of every other
	// branch in this function and it is deliberate: everywhere else the fallback
	// is "read the article", which is worse than what was asked for and better
	// than silence. Here the fallback would be reading the last story a second
	// time, which is not a degraded goodbye — it is a bug with a voice. An
	// empty script ends the programme quietly, which is what a broadcast with no
	// sign-off has always done.
	if closing {
		vibe := smart.VibeFor(prefs[podcastVibePrefKey])
		txt, err := a.podcastOutro(ctx, it, vibe, open)
		if err != nil {
			a.cfg.Log.Warn("broadcast sign-off failed, ending without one",
				"item", it.ID, "err", err)
			return "", ""
		}
		return txt, podcastKey(it.ID, "", vibe, open) + "#outro"
	}

	// The opening, on its own, before any story. It is a separate recording
	// rather than the first paragraph of the first segment for a reason that is
	// entirely about sound: the broadcast opens over music, and the music can
	// only swell and clear at a moment the client can SEE coming — which means
	// the end of a file. See smart.Segment.OpenOnly.
	//
	// A failure here falls through to the ordinary path, which is the right
	// answer: the reader loses the greeting and still gets the news.
	if intro && open != nil {
		vibe := smart.VibeFor(prefs[podcastVibePrefKey])
		if txt, err := a.podcastIntro(ctx, it, vibe, open); err == nil {
			return txt, podcastKey(it.ID, "", vibe, open) + "#intro"
		} else if !errors.Is(err, smart.ErrNothingToSummarise) {
			a.cfg.Log.Warn("broadcast opening failed, starting on the story",
				"item", it.ID, "err", err)
		}
	}

	if prefs[podcastPrefKey] == "true" {
		vibe := smart.VibeFor(prefs[podcastVibePrefKey])
		seg, err := a.podcastFor(ctx, it, prev, vibe, open, opened)
		switch {
		case err == nil:
			key := podcastKey(it.ID, prev.ID, vibe, open)
			if opened {
				// A first story that greets and one that does not are different
				// recordings. Sharing a key would serve one for the other, and
				// the audible half of that is the date read out twice.
				key += "#opened"
			}
			return seg, key
		case errors.Is(err, smart.ErrNothingToSummarise):
			// A two-line link post is its own segment. Read it.
			a.cfg.Log.Debug("broadcast segment skipped, reading the article", "item", it.ID)
		default:
			a.cfg.Log.Warn("broadcast segment failed, falling back",
				"item", it.ID, "err", err)
		}
	}

	if prefs[digestPrefKey] == "true" {
		d, err := a.digestFor(ctx, it)
		switch {
		case err == nil:
			return d, it.ID + "#digest"
		case errors.Is(err, smart.ErrNothingToSummarise):
			// Nothing to condense is not a failure — an item with two lines of
			// body IS its own summary. Read it.
			a.cfg.Log.Debug("digest skipped, reading the article", "item", it.ID)
		default:
			// A summariser that is down must not take listening down with it.
			// The reader asked to hear the article; they get the article, which
			// is longer than they wanted and infinitely better than silence.
			a.cfg.Log.Warn("digest failed, falling back to the full article",
				"item", it.ID, "err", err)
		}
	}
	return text, cacheKey
}

// speechTTL is how long a minted listening ticket stays valid.
//
// Between the asset proxy's twelve hours and the page proxy's two, and the
// reasoning is the shape of listening rather than a security number: you start
// an article, you pause it, you come back after lunch and press play — and the
// browser refetches, because an <audio> element does not hold a decoded stream
// forever. Two hours would expire mid-afternoon on an article opened at noon;
// twelve is longer than this needs to be for a capability that carries who the
// listener is.
const speechTTL = 6 * time.Hour

// SpeechURL mints a listening ticket for one item.
//
// Returns "" when this instance cannot synthesise anything, which is the signal
// the client uses to leave the Smart+ control off the article entirely rather
// than offering a button that answers 501.
//
// # Why the ticket is sealed and not signed
//
// /asset and /p sign a plaintext target: the URL says what it fetches, the HMAC
// says we minted it, and neither carries identity — which is exactly why they
// can be shared, logged and cached without telling anyone anything.
//
// This one cannot work that way. Reading an item requires a scope, so the
// capability has to carry one, and a scope is the reader's identity. Signing it
// would put a tenant and user id in every <audio src> — and therefore in browser
// history, in the referrer, and in every access log between here and the
// listener. So the payload is SEALED with AES-256-GCM instead: authenticated
// like a signature, and opaque as well. Tampering fails to open rather than
// verifying against a different message, which is the property that matters.
func (a *App) SpeechURL(ctx context.Context, sc store.Scope, itemID string) string {
	if a.speak == nil || len(a.speechKey) != 32 || itemID == "" || !sc.Valid() {
		return ""
	}
	// Checked against the key that exists NOW. An instance whose key is pasted
	// in at runtime starts minting tickets from that moment, and one whose key
	// is cleared stops — without a restart, and without handing out tickets for
	// a feature that would answer 501.
	if !a.speak.Configured(ctx) {
		return ""
	}
	exp := time.Now().Add(speechTTL).Unix()
	sealed, err := secret.Encrypt(a.speechKey, []byte(speechTicket(sc, itemID, exp)))
	if err != nil {
		return ""
	}
	// QueryEscape because Encrypt returns standard base64, which contains `+`
	// and `/`. A raw `+` in a query string decodes to a space and the ticket
	// silently fails to open.
	return "/speech?t=" + url.QueryEscape(sealed)
}

// speechTicket is what gets sealed.
//
// The "speech" prefix is the same domain separation /p uses against /asset: a
// ciphertext minted for one purpose must not open as another if these keys ever
// get shared. The expiry is inside the sealed payload rather than beside it,
// which is the whole point — a client that could edit the expiry without
// invalidating the ticket would hold a permanent one.
func speechTicket(sc store.Scope, itemID string, exp int64) string {
	return "speech\n" + sc.TenantID + "\n" + sc.UserID + "\n" + sc.Role + "\n" +
		itemID + "\n" + strconv.FormatInt(exp, 10)
}

// openSpeechTicket returns the scope and item a ticket authorises.
//
// Every failure is the same error on purpose. A caller holding a corrupt,
// forged, expired or foreign ticket learns only that it did not work — telling
// them which would turn this into an oracle for probing what the key accepts.
func (a *App) openSpeechTicket(tok string) (store.Scope, string, error) {
	if len(a.speechKey) != 32 || tok == "" {
		return store.Scope{}, "", errBadTicket
	}
	raw, err := secret.Decrypt(a.speechKey, tok)
	if err != nil {
		return store.Scope{}, "", errBadTicket
	}
	parts := strings.Split(string(raw), "\n")
	if len(parts) != 6 || parts[0] != "speech" {
		return store.Scope{}, "", errBadTicket
	}
	exp, err := strconv.ParseInt(parts[5], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return store.Scope{}, "", errBadTicket
	}
	sc := store.Scope{TenantID: parts[1], UserID: parts[2], Role: parts[3]}
	if !sc.Valid() || parts[4] == "" {
		return store.Scope{}, "", errBadTicket
	}
	return sc, parts[4], nil
}

// errBadTicket is unexported and never reaches the reader: the handler turns it
// into a 403 with a fixed message.
var errBadTicket = errors.New("speech: ticket will not open")

// serveSpeech returns spoken audio for one item.
//
//	GET /speech?t=<sealed ticket>      — how the client actually calls it
//	GET /speech?item=<id>              — header-authenticated, for tools and tests
//	          &p=<previous item id>    — optional; the story to hand over FROM,
//	                                     read only in broadcast mode (§19)
//
// A plain HTTPS endpoint rather than an RPC over the tunnel, because the client
// is an <audio> element: giving the browser a URL lets it stream, seek, buffer
// and cache the audio itself, none of which we would get for free by shipping
// megabytes of MP3 through a WebSocket and back out through a blob URL.
//
// That same fact is why the ticket exists. An <audio src> cannot send an
// Authorization header, so the header path below can only ever identify a
// caller through the DevMode fallback — which made this whole feature work on a
// laptop and answer 401 on any instance with real login. The ticket carries the
// scope instead, sealed, minted by GetItem for a reader who was already allowed
// to see the article. See SpeechURL.
//
// Four gates, in order, and each one is a different failure the reader needs
// told apart:
//
//  1. Authenticated — 401, or 403 for a ticket that will not open. Article
//     text is private.
//  2. Item visible to this scope — 404, never 403 (§20.7): a permission error
//     would confirm the item exists.
//  3. The instance has a key at all — 501. Nothing the reader can do.
//  4. This user opted in — 403. Something the reader CAN do, in settings.
func (a *App) serveSpeech(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	var (
		sc  store.Scope
		id  string
		err error
	)
	if tok := strings.TrimSpace(r.URL.Query().Get("t")); tok != "" {
		// The ticket names its own item. Deliberately not cross-checked against
		// an `item` parameter: honouring both would let a caller pair a valid
		// ticket with someone else's id, and the resulting ambiguity is exactly
		// the kind a capability is supposed to remove.
		sc, id, err = a.openSpeechTicket(tok)
		if err != nil {
			// 403 rather than 401: the caller presented a credential and it was
			// refused. 401 invites a client to retry with the login it already
			// has, which would loop.
			http.Error(w, "this listening link is not valid or has expired", http.StatusForbidden)
			return
		}
	} else {
		id = strings.TrimSpace(r.URL.Query().Get("item"))
		if id == "" || len(id) > 64 {
			http.Error(w, "bad item", http.StatusBadRequest)
			return
		}
		sc, err = a.speechScope(r)
		if err != nil {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
	}

	if a.speak == nil || !a.speak.Configured(r.Context()) {
		// 501 rather than 500: the server is working correctly and simply has no
		// key. "Not implemented on this instance" is exactly what that means.
		http.Error(w, "speech is not configured on this server", http.StatusNotImplemented)
		return
	}

	prefs, err := a.repo.GetPrefs(r.Context(), sc)
	if err != nil || prefs[ttsPrefKey] != "true" {
		http.Error(w, "Smart+ speech is off for this account", http.StatusForbidden)
		return
	}

	it, err := a.repo.GetItem(r.Context(), sc, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Whatever this reader last heard, so a broadcast segment can hand over from
	// it. Resolved through the SAME SCOPE as the item above, which is what makes
	// a caller-supplied id safe (see prevItemParam) — and it is only looked up at
	// all when the preference that uses it is on, so the ordinary listen still
	// costs one query.
	var prev store.Item
	var open *smart.Opening
	// The opening asked for ON ITS OWN, as its own recording. Only meaningful in
	// broadcast mode, and only ever at the top: a request carrying both an intro
	// flag and a predecessor is a client that has lost track of where it is, and
	// the flag loses.
	intro := isIntroRequest(r.URL.Query().Get(introParam), prefs[podcastPrefKey] == "true")
	// The sign-off, asked for on its own after the last story. Like the opening
	// it is its own recording, and for a related reason: the music has to come
	// back up under it and then end, which can only be timed against a file.
	closing := isCloseRequest(r.URL.Query().Get(introParam), prefs[podcastPrefKey] == "true")
	// The other half of the split: the greeting has already been recorded, so
	// this story must not open the show a second time.
	opened := prefs[podcastPrefKey] == "true" &&
		strings.TrimSpace(r.URL.Query().Get(introParam)) == introDone
	if prefs[podcastPrefKey] == "true" {
		if pid := strings.TrimSpace(r.URL.Query().Get(prevItemParam)); pid != "" && len(pid) <= 64 && pid != id {
			// An unreadable or unknown predecessor is simply no predecessor. It
			// is not an error the listener can act on, and refusing to speak
			// because the story BEFORE this one could not be found would be a
			// silence caused by something that is not the point.
			if p, perr := a.repo.GetItem(r.Context(), sc, pid); perr == nil {
				prev = p
			}
		}
		// The greeting belongs to the segment with nothing before it, and that
		// test is made HERE rather than trusted to the client: a client that
		// sends the opening parameters on every request still gets exactly one
		// opening per broadcast, because from the second story onward there is a
		// predecessor and the top of the show has already happened.
		//
		// Except when the client says the opening has already been recorded on
		// its own — see introParam. Without that, a split broadcast would greet
		// the listener twice: once in the intro file and again at the top of the
		// first story, which is the exact sound of software repeating itself.
		if wantsOpening(r.URL.Query().Get(introParam), prev.ID != "") {
			open = a.openingFrom(r.Context(), sc, r, time.Now())
		}
	}

	// Four things this could be — the article, its digest, its slot in a
	// broadcast, or the sign-off that ends one — and they are different
	// artifacts, not renderings of one. `cacheKey` carries the difference,
	// because the audio cache is keyed by it: without that, turning a mode on
	// would serve yesterday's rendering of a different script, which looks
	// exactly like the toggle not working.
	//
	// `closing` is NOT conditioned on `prev.ID == ""` the way the other two are.
	// The opening flags mean "this is the top", so a predecessor contradicts
	// them; a sign-off has every story behind it by definition, and the one it
	// names travels as the item rather than as `p`.
	text, cacheKey := a.speechScript(r.Context(), prefs, it, prev, open,
		intro && prev.ID == "", opened && prev.ID == "", closing)
	if text == "" {
		if closing {
			// 204 rather than 422: the request was understood and correct, and
			// the answer is that this programme ends without a goodbye. The
			// client treats it as the end either way, and a 4xx here would put a
			// console error under a session that finished perfectly well.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "nothing to read aloud", http.StatusUnprocessableEntity)
		return
	}

	voice := strings.TrimSpace(prefs["tts.voice"])
	audio, err := a.speak.Speak(r.Context(), cacheKey, text, prefs["tts.model"], voice)
	if err != nil {
		// The provider's own message can echo request content, and request
		// content here is the user's article — so it goes to the log and never
		// to the response (§22.11).
		a.cfg.Log.Warn("speech synthesis failed", "item", id, "err", err)
		http.Error(w, "couldn't synthesise that", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(audio)))
	// Private, not public: this is one user's article read aloud, and a shared
	// cache holding it would serve it to whoever asked next.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Accept-Ranges", "none")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(audio)
}

// speechScope resolves the caller for a plain HTTP request.
//
// The gRPC path reads its token from metadata; here it comes from the
// Authorization header, or from the dev account when the server is running
// without login. A query-string token is deliberately NOT accepted: this URL
// ends up in an <audio src>, which means the browser history, the referrer and
// every access log.
func (a *App) speechScope(r *http.Request) (store.Scope, error) {
	tok := strings.TrimSpace(r.Header.Get("Authorization"))
	if tok == "" {
		return a.devScope(r.Context())
	}
	if len(tok) > 7 && strings.EqualFold(tok[:7], "bearer ") {
		tok = tok[7:]
	}
	return a.scopeForToken(r.Context(), tok)
}

// Compiled once. These used to be built inside speechText, which recompiled
// five patterns per article — on a continuous session that is five compilations
// per track, for patterns that never change.
var (
	tagRE   = regexp.MustCompile(`(?is)<[^>]*>`)
	blockRE = regexp.MustCompile(`(?i)</(p|div|li|h[1-6]|blockquote|tr)>`)
	brRE    = regexp.MustCompile(`(?i)<br\s*/?>`)
	runRE   = regexp.MustCompile(`[ \t\r\f\v]+`)
	paraRE  = regexp.MustCompile(`\n\s*\n\s*`)
)

// speechText reduces an item to what should be read aloud.
//
// The title first, then the body, because a voice that starts mid-article gives
// the listener nothing to hang it on. Markup is stripped rather than sent: the
// provider bills per character, and `<div class="wp-block-group">` is neither
// speech nor cheap.
func speechText(it store.Item) string {
	body := speechBody(it)
	if intro := speechIntro(it); intro != "" {
		if body == "" {
			return intro
		}
		return intro + "\n\n" + body
	}
	return body
}

// speechIntro is the announcement that opens a segment.
//
// Source AND title, not the title alone, because of how this is actually used:
// a continuous session plays one article after another with no visual, and
// "Fsyncgate" arriving cold tells a listener what it is called but not where it
// came from — which is most of how anyone decides whether to keep listening.
// Naming the publication is the difference between a queue and a radio station.
//
// Written as a sentence with a full stop rather than a colon: a synthesiser
// reads a colon as a pause of no particular length, and "From Hacker News:
// Fsyncgate" comes out as one run-on. The full stop is what makes it land as a
// station identifier followed by a headline.
func speechIntro(it store.Item) string {
	title := strings.TrimSpace(it.Title)
	source := strings.TrimSpace(it.SourceTitle)
	switch {
	case title == "" && source == "":
		return ""
	case title == "":
		return "From " + source + "."
	case source == "":
		return title + "."
	default:
		return "From " + source + ". " + title + "."
	}
}

// speechBody is the article itself, with the markup taken out.
//
// Separate from speechText because the summariser wants exactly this and none
// of the announcement: a digest that has been fed "From Hacker News." will
// dutifully summarise the fact that it came from Hacker News.
func speechBody(it store.Item) string {
	body := it.ContentHTML
	if strings.TrimSpace(body) == "" {
		body = it.Summary
	}
	// Block-level tags become paragraph breaks first, so sentences do not run
	// into each other once the tags are gone.
	body = blockRE.ReplaceAllString(body, "\n\n")
	body = brRE.ReplaceAllString(body, "\n")
	body = tagRE.ReplaceAllString(body, " ")
	body = htmlUnescape(body)
	return strings.TrimSpace(collapseSpace(body))
}

// collapseSpace squeezes runs of whitespace, keeping paragraph breaks.
func collapseSpace(s string) string {
	return paraRE.ReplaceAllString(runRE.ReplaceAllString(s, " "), "\n\n")
}

// htmlUnescape resolves entities. Feeds are full of them, and a synthesiser
// reading "&amp;#8217;" aloud is unmistakable.
func htmlUnescape(s string) string { return html.UnescapeString(s) }
