package reader

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/feed"
)

// H5/H8: an address whose first fetch fails is REFUSED at add time, with an
// error that says what was seen, and nothing is left in the sidebar. It used to
// keep the subscription for every failure but two and report "Added".
func TestSubscribeRefusesAnAddressThatDoesNotYieldAFeed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gone.xml", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/boom.xml", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	mux.HandleFunc("/broken.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>x</title>
<item><title>Unclosed <b>tag</title><description>oops & not escaped</channel>`))
	})
	mux.HandleFunc("/page.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><p>hello</p></body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// A port nothing listens on: grab one and let it go.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + ln.Addr().String() + "/feed.xml"
	_ = ln.Close()

	cases := []struct {
		name  string
		url   string
		check func(error) bool
	}{
		{"a 404", srv.URL + "/gone.xml", func(e error) bool {
			var he *feed.HTTPError
			return errors.As(e, &he) && he.Status == 404
		}},
		{"a 500", srv.URL + "/boom.xml", func(e error) bool {
			var he *feed.HTTPError
			return errors.As(e, &he) && he.Status == 500
		}},
		{"a feed that is broken", srv.URL + "/broken.xml", func(e error) bool {
			return errors.Is(e, feed.ErrBrokenFeed) && errors.Is(e, feed.ErrNotAFeed)
		}},
		{"a page that is not a feed", srv.URL + "/page.html", func(e error) bool {
			return errors.Is(e, feed.ErrNotAFeed) && !errors.Is(e, feed.ErrBrokenFeed)
		}},
		{"a host that does not answer", dead, func(e error) bool {
			return e != nil && !errors.Is(e, feed.ErrNotAFeed)
		}},
		{"text that is not an address", "notaurlatall", func(e error) bool {
			return errors.Is(e, ErrBadURL)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, sc := testService(t)
			ctx := context.Background()
			_, _, _, err := svc.Subscribe(ctx, sc, tc.url, "", "")
			if err == nil {
				t.Fatal("Subscribe succeeded; it must refuse")
			}
			if !tc.check(err) {
				t.Errorf("error %v is not the one this case names", err)
			}
			feeds, _, ferr := svc.ListFeeds(ctx, sc)
			if ferr != nil {
				t.Fatal(ferr)
			}
			if len(feeds) != 0 {
				t.Errorf("%d feeds left in the sidebar after a refusal, want 0", len(feeds))
			}
		})
	}
}

// A reader who already follows an address keeps it when a later add of the same
// address fails its fetch: the refusal exists to stop typos, not to unsubscribe
// somebody from a feed that is down today. OPML import is how such a row exists
// at all now — it subscribes without fetching.
func TestSubscribeNeverUndoesASubscriptionTheReaderAlreadyHad(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	svc, _, sc := testService(t)
	ctx := context.Background()
	if _, _, err := svc.SubscribeOnly(ctx, sc, srv.URL+"/imported.xml", "Imported", "", ""); err != nil {
		t.Fatalf("SubscribeOnly: %v", err)
	}
	if _, _, _, err := svc.Subscribe(ctx, sc, srv.URL+"/imported.xml", "", ""); err != nil {
		t.Fatalf("re-adding a followed feed that is down returned %v, want nil", err)
	}
	feeds, _, _ := svc.ListFeeds(ctx, sc)
	if len(feeds) != 1 {
		t.Errorf("%d feeds after the re-add, want the 1 the reader already had", len(feeds))
	}
}
