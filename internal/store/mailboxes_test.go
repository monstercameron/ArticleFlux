package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/mailparse"
)

type mailFixture struct {
	db   *DB
	mail *MailboxRepo
	repo *ReaderRepo
	sc   Scope
	ctx  context.Context
}

func newMailFixture(t *testing.T) *mailFixture {
	t.Helper()
	ctx := context.Background()
	db := openTest(t)
	repo := NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "u", Hash: "x",
		Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	return &mailFixture{
		db: db, mail: NewMailboxRepo(db, testEncKey), repo: repo, ctx: ctx,
		sc: Scope{TenantID: "t1", UserID: "u1", Role: "member"},
	}
}

func (f *mailFixture) add(t *testing.T, host, user, pw string) string {
	t.Helper()
	id, err := f.mail.PutMailbox(f.ctx, f.sc, Mailbox{Host: host, Username: user}, pw)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// The password is the only credential here belonging to a third party, and the
// poller has to USE it — so it cannot be hashed and must round-trip exactly.
func TestAMailboxPasswordRoundTripsAndIsNotStoredInTheClear(t *testing.T) {
	f := newMailFixture(t)
	const pw = "hunter2-app-specific"
	id := f.add(t, "imap.example.com", "reader@example.com", pw)

	got, err := f.mail.MailboxSecret(f.ctx, f.sc, id)
	if err != nil {
		t.Fatal(err)
	}
	if got != pw {
		t.Errorf("password came back as %q", got)
	}

	// And the row itself must not contain it. A credential readable from a
	// database dump is a credential leaked by every backup.
	var raw []byte
	if err := f.db.Read.QueryRowContext(f.ctx,
		`SELECT secret_enc FROM mailboxes WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), pw) {
		t.Error("the password is stored in the clear")
	}
}

// The struct has no password field, so listing mailboxes cannot serialise one
// into an RPC response — and cannot start doing so when a field is added later.
func TestListingMailboxesCannotCarryACredential(t *testing.T) {
	f := newMailFixture(t)
	const pw = "do-not-leak-me"
	f.add(t, "imap.example.com", "reader@example.com", pw)

	boxes, err := f.mail.ListMailboxes(f.ctx, f.sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 1 {
		t.Fatalf("%d mailboxes, want 1", len(boxes))
	}
	// Every string the struct exposes, so adding a field that happens to carry
	// the credential is caught rather than only the fields known today.
	if found := findMarker(reflect.ValueOf(boxes), 0); found != "" {
		t.Errorf("a listed mailbox carried the marker: %q", found)
	}
	for _, s := range []string{boxes[0].Host, boxes[0].Username, boxes[0].Folder,
		boxes[0].LastError, boxes[0].ID} {
		if strings.Contains(s, pw) {
			t.Errorf("the password appeared in %q", s)
		}
	}
}

// An update with no password keeps the stored one. That is what lets a settings
// screen edit a mailbox without the credential making a round trip through the
// browser.
func TestAnEmptyPasswordOnUpdateKeepsTheStoredOne(t *testing.T) {
	f := newMailFixture(t)
	const pw = "original"
	id := f.add(t, "imap.example.com", "reader@example.com", pw)

	if _, err := f.mail.PutMailbox(f.ctx, f.sc, Mailbox{
		ID: id, Host: "imap2.example.com", Username: "reader@example.com",
		Folder: "Newsletters",
	}, ""); err != nil {
		t.Fatal(err)
	}

	got, err := f.mail.MailboxSecret(f.ctx, f.sc, id)
	if err != nil {
		t.Fatal(err)
	}
	if got != pw {
		t.Errorf("an update with no password changed it to %q", got)
	}
	boxes, _ := f.mail.ListMailboxes(f.ctx, f.sc)
	if boxes[0].Host != "imap2.example.com" || boxes[0].Folder != "Newsletters" {
		t.Errorf("the update did not apply: %+v", boxes[0])
	}
}

// A mailbox that cannot authenticate is a row that fails forever and looks
// configured, so creating one without a password is refused.
func TestANewMailboxNeedsAPassword(t *testing.T) {
	f := newMailFixture(t)
	if _, err := f.mail.PutMailbox(f.ctx, f.sc,
		Mailbox{Host: "imap.example.com", Username: "reader@example.com"}, ""); err == nil {
		t.Error("a mailbox with no password was created")
	}
	if _, err := f.mail.PutMailbox(f.ctx, f.sc, Mailbox{Username: "reader"}, "pw"); err == nil {
		t.Error("a mailbox with no host was created")
	}
}

// Without a key, refuse rather than store plaintext. An operator told this
// failed will fix it; one whose password was written in the clear never finds
// out.
func TestWithoutAKeyTheRepoRefusesRatherThanStoringPlaintext(t *testing.T) {
	f := newMailFixture(t)
	keyless := NewMailboxRepo(f.db, nil)
	if keyless.CanStoreSecrets() {
		t.Fatal("a repo with no key claims it can store secrets")
	}
	_, err := keyless.PutMailbox(f.ctx, f.sc,
		Mailbox{Host: "imap.example.com", Username: "r@example.com"}, "pw")
	if !errors.Is(err, ErrNoKey) {
		t.Errorf("= %v, want ErrNoKey", err)
	}
	var n int
	f.db.Read.QueryRowContext(f.ctx, `SELECT count(*) FROM mailboxes`).Scan(&n)
	if n != 0 {
		t.Errorf("%d mailboxes were written without a key", n)
	}
}

// §6.4: mailbox sources are keyed per USER. Global keying would merge two
// people's private mail into one row that both of them read.
func TestMailboxSourcesArePerUserAndNeverGlobal(t *testing.T) {
	f := newMailFixture(t)
	if err := f.repo.CreateTenantAndUser(f.ctx, NewTenant{
		TenantID: "t2", Name: "T2", UserID: "u2", Username: "u2", Hash: "x",
		Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	other := Scope{TenantID: "t2", UserID: "u2", Role: "member"}

	mine := f.add(t, "imap.example.com", "me@example.com", "pw")
	theirs, err := f.mail.PutMailbox(f.ctx, other,
		Mailbox{Host: "imap.example.com", Username: "them@example.com"}, "pw")
	if err != nil {
		t.Fatal(err)
	}

	// The SAME newsletter sender, reached through two different mailboxes.
	const sender = "weekly@newsletter.example"
	a, err := f.mail.EnsureMailboxSource(f.ctx, f.sc, mine, sender, "Weekly")
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.mail.EnsureMailboxSource(f.ctx, other, theirs, sender, "Weekly")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two readers of the same newsletter got ONE shared source — " +
			"their private mail is now in a row they both read")
	}

	// And the key is the one mailparse builds, not one formatted here.
	var key string
	if err := f.db.Read.QueryRowContext(f.ctx,
		`SELECT natural_key FROM sources WHERE id = ?`, a).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if want := mailparse.NaturalKey(mine, sender); key != want {
		t.Errorf("natural_key = %q, want %q", key, want)
	}
}

// Calling it twice is what a second message from the same sender does.
func TestEnsureMailboxSourceIsIdempotent(t *testing.T) {
	f := newMailFixture(t)
	id := f.add(t, "imap.example.com", "me@example.com", "pw")

	first, err := f.mail.EnsureMailboxSource(f.ctx, f.sc, id, "a@b.example", "A")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.mail.EnsureMailboxSource(f.ctx, f.sc, id, "A@B.example", "A")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("a second message from the same sender created a second source "+
			"(%s then %s) — sender case must not matter", first, second)
	}
	feeds, err := f.repo.ListFeeds(f.ctx, f.sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 1 {
		t.Errorf("%d subscriptions after two messages from one sender, want 1", len(feeds))
	}
}

// A mailbox source's feed_url is `mailbox:<id>:<sender>`, which nothing can
// fetch. Handing it to the feed parser is "not a recognisable feed" on every
// poll forever — which is how a feature like this silently never works.
func TestMailboxSourcesAreNeverHandedToTheFeedPoller(t *testing.T) {
	f := newMailFixture(t)
	id := f.add(t, "imap.example.com", "me@example.com", "pw")
	src, err := f.mail.EnsureMailboxSource(f.ctx, f.sc, id, "a@b.example", "A")
	if err != nil {
		t.Fatal(err)
	}

	due, err := f.repo.DueSources(f.ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range due {
		if d.ID == src {
			t.Fatal("a mailbox source is in the feed poller's queue; its feed_url " +
				"is not a URL, so this is a permanent per-poll error")
		}
	}

	// And the lag metric must not count it either, or an instance with one
	// mailbox reports a permanent backlog.
	lag, err := f.repo.PollerLag(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lag.Due != 0 {
		t.Errorf("poller lag reports %d due with only a mailbox source configured", lag.Due)
	}
}

// UID bookkeeping only moves forward: a server that resets UID validity would
// otherwise walk the mailbox backwards and re-ingest everything as new mail.
func TestLastUIDOnlyMovesForward(t *testing.T) {
	f := newMailFixture(t)
	id := f.add(t, "imap.example.com", "me@example.com", "pw")

	for _, uid := range []int64{40, 120, 90, 0} {
		if err := f.mail.RecordMailboxPoll(f.ctx, id, uid, ""); err != nil {
			t.Fatal(err)
		}
	}
	boxes, _ := f.mail.ListMailboxes(f.ctx, f.sc)
	if boxes[0].LastUID != 120 {
		t.Errorf("last_uid = %d after 40, 120, 90, 0 — want 120", boxes[0].LastUID)
	}
}

// A wrong password does not fail politely: providers lock accounts, and some
// count failed IMAP logins toward the same limit as failed web logins.
func TestFailedPollsBackOffAndSuccessClearsIt(t *testing.T) {
	f := newMailFixture(t)
	id := f.add(t, "imap.example.com", "me@example.com", "pw")

	var prev time.Time
	for i := 0; i < 5; i++ {
		if err := f.mail.RecordMailboxPoll(f.ctx, id, 0, "AUTHENTICATIONFAILED"); err != nil {
			t.Fatal(err)
		}
		boxes, _ := f.mail.ListMailboxes(f.ctx, f.sc)
		next, err := time.Parse(time.RFC3339Nano, boxes[0].NextPollAt)
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && !next.After(prev) {
			t.Errorf("failure %d scheduled the next poll at %v, not after %v — "+
				"the backoff is not doubling", i+1, next, prev)
		}
		prev = next
		if boxes[0].ConsecutiveFailures != i+1 {
			t.Errorf("consecutive_failures = %d after %d failures",
				boxes[0].ConsecutiveFailures, i+1)
		}
	}
	// Capped, or a mailbox that recovers is never tried again.
	boxes, _ := f.mail.ListMailboxes(f.ctx, f.sc)
	next, _ := time.Parse(time.RFC3339Nano, boxes[0].NextPollAt)
	if d := time.Until(next); d > 7*time.Hour {
		t.Errorf("backoff reached %v; the ceiling is six hours", d)
	}

	if err := f.mail.RecordMailboxPoll(f.ctx, id, 7, ""); err != nil {
		t.Fatal(err)
	}
	boxes, _ = f.mail.ListMailboxes(f.ctx, f.sc)
	if boxes[0].ConsecutiveFailures != 0 || boxes[0].LastError != "" {
		t.Errorf("a successful poll left the failure state: %+v", boxes[0])
	}
}

// Changing the password clears the failure state, because the usual reason to
// change one is that the old one stopped working.
func TestANewPasswordClearsTheFailureState(t *testing.T) {
	f := newMailFixture(t)
	id := f.add(t, "imap.example.com", "me@example.com", "old")
	if err := f.mail.RecordMailboxPoll(f.ctx, id, 0, "AUTHENTICATIONFAILED"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mail.PutMailbox(f.ctx, f.sc, Mailbox{
		ID: id, Host: "imap.example.com", Username: "me@example.com",
	}, "new"); err != nil {
		t.Fatal(err)
	}
	boxes, _ := f.mail.ListMailboxes(f.ctx, f.sc)
	if boxes[0].ConsecutiveFailures != 0 || boxes[0].LastError != "" {
		t.Errorf("changing the password left the mailbox looking broken: %+v", boxes[0])
	}
}

// Removing a mailbox withdraws the credential, and retires the sources it fed
// without destroying the mail already delivered.
func TestDeletingAMailboxWithdrawsTheCredentialAndRetiresItsSources(t *testing.T) {
	f := newMailFixture(t)
	id := f.add(t, "imap.example.com", "me@example.com", "pw")
	src, err := f.mail.EnsureMailboxSource(f.ctx, f.sc, id, "a@b.example", "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.IngestItems(f.ctx, src, []IngestItem{{
		GUID: "m1", Title: "A newsletter", PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	if err := f.mail.DeleteMailbox(f.ctx, f.sc, id); err != nil {
		t.Fatal(err)
	}

	if _, err := f.mail.MailboxSecret(f.ctx, f.sc, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("the credential survived deletion: %v", err)
	}
	var deactivated string
	if err := f.db.Read.QueryRowContext(f.ctx,
		`SELECT COALESCE(deactivated_at,'') FROM sources WHERE id = ?`, src).Scan(&deactivated); err != nil {
		t.Fatal(err)
	}
	if deactivated == "" {
		t.Error("the mailbox source is still active after its mailbox was removed")
	}
	// A22: the row survives, so the mail already delivered is not orphaned.
	var items int
	f.db.Read.QueryRowContext(f.ctx, `SELECT count(*) FROM items WHERE source_id = ?`, src).Scan(&items)
	if items != 1 {
		t.Errorf("%d items survive the deletion, want 1 — deleting a mailbox "+
			"must not destroy the mail it already delivered", items)
	}
}

// §17's cascade needs to FIND them, which is what owner_user_id (0016) is for.
func TestOwnedSourcesAreFindableForDeletion(t *testing.T) {
	f := newMailFixture(t)
	id := f.add(t, "imap.example.com", "me@example.com", "pw")
	if _, err := f.mail.EnsureMailboxSource(f.ctx, f.sc, id, "a@b.example", "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mail.EnsureMailboxSource(f.ctx, f.sc, id, "c@d.example", "C"); err != nil {
		t.Fatal(err)
	}
	// A plain syndicated feed must NOT be in the list: it is global and shared.
	if _, _, err := f.repo.Subscribe(f.ctx, f.sc, NewSubscription{
		NaturalKey: "feed:public.example/rss", FeedURL: "https://public.example/rss",
		Title: "Public",
	}); err != nil {
		t.Fatal(err)
	}

	owned, err := f.mail.OwnedSourceIDs(f.ctx, f.sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 2 {
		t.Errorf("%d owned sources, want the 2 mailbox ones and not the shared feed", len(owned))
	}
}

// DueMailboxes is the poller's queue, and a mailbox just added must be in it.
func TestDueMailboxesSchedulesANewOneImmediately(t *testing.T) {
	f := newMailFixture(t)
	id := f.add(t, "imap.example.com", "me@example.com", "pw")

	due, err := f.mail.DueMailboxes(f.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != id {
		t.Fatalf("a newly added mailbox is not due: %+v", due)
	}

	// After a successful poll it waits its interval.
	if err := f.mail.RecordMailboxPoll(f.ctx, id, 1, ""); err != nil {
		t.Fatal(err)
	}
	due, err = f.mail.DueMailboxes(f.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Errorf("a mailbox polled a moment ago is due again: %+v", due)
	}
}
