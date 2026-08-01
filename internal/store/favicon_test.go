package store

import (
	"errors"
	"testing"
	"time"
)

func TestPutFaviconThenGetFaviconRoundTrips(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()

	if err := repo.PutFavicon(ctx, FaviconRow{
		Host: "example.com", Bytes: []byte{1, 2, 3}, ContentType: "image/png",
		ETag: "abc123", Failures: 0,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetFavicon(ctx, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "example.com" || string(got.Bytes) != "\x01\x02\x03" ||
		got.ContentType != "image/png" || got.ETag != "abc123" {
		t.Errorf("got %+v", got)
	}
}

// A host with no icon is recorded as a negative entry (nil bytes), which is
// what stops every page load from retrying the fetch forever.
func TestPutFaviconRecordsAbsence(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()

	if err := repo.PutFavicon(ctx, FaviconRow{Host: "no-icon.example", Failures: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetFavicon(ctx, "no-icon.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bytes) != 0 {
		t.Errorf("Bytes = %v, want empty for a negative entry", got.Bytes)
	}
	if got.Failures != 1 {
		t.Errorf("Failures = %d, want 1", got.Failures)
	}
}

func TestPutFaviconUpsertsRatherThanDuplicating(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	if err := repo.PutFavicon(ctx, FaviconRow{Host: "example.com", Bytes: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutFavicon(ctx, FaviconRow{Host: "example.com", Bytes: []byte{9, 9}, Failures: 2}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetFavicon(ctx, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Bytes) != "\x09\x09" || got.Failures != 2 {
		t.Errorf("got %+v, want the second write's values", got)
	}
	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM favicons WHERE host='example.com'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d rows for one host, want 1", n)
	}
}

func TestGetFaviconNotFound(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	if _, err := repo.GetFavicon(t.Context(), "never-fetched.example"); !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

func TestSourceHostsListsActiveOnly(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	now := time.Now().UTC().Format(time.RFC3339)

	for _, s := range []struct{ id, feedURL, siteURL string }{
		{"s1", "https://a.example/feed", "https://a.example"},
		{"s2", "https://b.example/feed", ""},
	} {
		var site any
		if s.siteURL != "" {
			site = s.siteURL
		}
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO sources (id,natural_key,feed_url,site_url,created_at) VALUES (?,?,?,?,?)`,
			s.id, "feed:"+s.id, s.feedURL, site, now); err != nil {
			t.Fatal(err)
		}
	}
	// A deactivated source must not appear.
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO sources (id,natural_key,feed_url,deactivated_at,created_at) VALUES ('s3','feed:s3','https://c.example/feed',?,?)`,
		now, now); err != nil {
		t.Fatal(err)
	}

	hosts, err := repo.SourceHosts(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, h := range hosts {
		set[h] = true
	}
	if !set["https://a.example"] {
		t.Errorf("missing site_url-preferred entry: %v", hosts)
	}
	if !set["https://b.example/feed"] {
		t.Errorf("missing feed_url fallback entry: %v", hosts)
	}
	if len(hosts) != 2 {
		t.Errorf("got %d hosts, want 2 (deactivated source excluded): %v", len(hosts), hosts)
	}
}

func TestSourceHostsRespectsLimit(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	now := time.Now().UTC().Format(time.RFC3339)
	for i := 0; i < 5; i++ {
		id := "s" + string(rune('a'+i))
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO sources (id,natural_key,feed_url,created_at) VALUES (?,?,?,?)`,
			id, "feed:"+id, "https://"+id+".example/feed", now); err != nil {
			t.Fatal(err)
		}
	}
	hosts, err := repo.SourceHosts(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Errorf("got %d hosts, want the limit of 2", len(hosts))
	}
}

func TestFileSizesReportsZeroBeforeAnyWrite(t *testing.T) {
	db := openTest(t)
	dbBytes, walBytes := db.FileSizes()
	if dbBytes <= 0 {
		t.Errorf("dbBytes = %d, want > 0 (the file exists once opened+migrated)", dbBytes)
	}
	// The WAL may or may not have been checkpointed away by now; both are
	// legitimate, but the call itself must not error or panic on a path that
	// does not (yet, or any longer) exist.
	if walBytes < 0 {
		t.Errorf("walBytes = %d, want >= 0", walBytes)
	}
}
