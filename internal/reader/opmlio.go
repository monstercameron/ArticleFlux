package reader

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/monstercameron/ArticleFlux/internal/opml"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Migration in and out, as service verbs rather than as CLI-only code (F1).
//
// `articleflux import` had this logic inside cmd/, which meant the only way to
// bring a subscription list in was a shell on the server. On a multi-tenant
// instance that is not a documentation gap, it is a member with no path at all.
// Moving it here is what lets the RPC, the CLI and any later Google Reader-side
// importer run the SAME migration rather than three that drift.
//
// Neither verb fetches. Subscribing 151 feeds is under a second; fetching them
// is minutes, and an import that appears to hang is one people interrupt
// halfway. The poller fills the sidebar in behind the reader.

// ImportSkip is one OPML row that did not become a subscription.
//
// Reported per row rather than as a count, because "12 skipped" is not
// something a person can act on — the reader wants to know which twelve.
type ImportSkip struct {
	Title  string
	URL    string
	Reason string
}

// ImportResult is what an import did, in the terms the person who ran it cares
// about: what they gained, what they already had, and what did not make it.
type ImportResult struct {
	// Title is the file's own <head><title>, when it had one. Worth handing
	// back: it is how somebody confirms they picked the export they meant to.
	Title string
	// Subscribed counts rows that ended as a subscription, INCLUDING ones this
	// reader already had. Re-running an import must not read as 151 failures.
	Subscribed int
	// AlreadySubscribed is how many of those were already in the sidebar.
	AlreadySubscribed int
	// Shared counts sources that already existed server-side and were attached
	// rather than created (A14) — a fact about the deployment, not the reader.
	Shared int
	// Folders is how many categories the file's groups resolved to.
	Folders int
	Skips   []ImportSkip
}

// MaxImportSkips bounds the report, not the import.
//
// A file whose every row fails is a file with the wrong shape, and 151 identical
// reasons do not diagnose it better than the first thirty do. The COUNT is
// always exact; only the list is capped.
const MaxImportSkips = 30

// ImportOPML subscribes this reader to everything in an OPML file.
//
// The bytes arrive unparsed on purpose: OPML in the wild disagrees with the
// specification about nesting, about which attribute holds the title, and about
// the encoding it declares, and internal/opml is written against what real
// exports from FreshRSS, Feedly, Inoreader and NewsBlur actually contain.
func (s *Service) ImportOPML(ctx context.Context, sc store.Scope, data []byte) (ImportResult, error) {
	var res ImportResult
	if len(data) == 0 {
		return res, errors.New("reader: empty OPML file")
	}
	doc, err := opml.Parse(bytes.NewReader(data))
	if err != nil {
		return res, err
	}
	res.Title = doc.Title

	// What this reader already had, BEFORE the import — the only way to tell
	// "you already had this" from "this is new to you". SubscribeOnly's bool
	// answers a different question: whether the SOURCE existed for any tenant
	// (A14), which is true for a popular feed nobody here subscribes to.
	before := map[string]bool{}
	if feeds, _, ferr := s.ListFeeds(ctx, sc); ferr == nil {
		for _, f := range feeds {
			before[f.SourceID] = true
		}
	}

	// The OPML folders become categories, resolved once each rather than per
	// feed: an export with 151 feeds has perhaps eight of them, and resolving
	// inside the loop would be 151 writes to create eight rows.
	//
	// This is the one place a category arrives as a NAME — see FolderByName.
	folderIDs := map[string]string{}
	for _, f := range doc.Feeds {
		if f.Folder == "" {
			continue
		}
		if _, done := folderIDs[f.Folder]; done {
			continue
		}
		id, ferr := s.FolderByName(ctx, sc, f.Folder)
		if ferr != nil {
			// An unusable category name must not cost the feeds inside it: they
			// import unfiled, which is recoverable, where skipping them is not.
			// Recorded rather than silent, so a reader whose categories did not
			// survive can see that rather than conclude the import ate them.
			res.addSkip(ImportSkip{
				Title:  f.Folder,
				Reason: fmt.Sprintf("category not created: %v", ferr),
			})
			continue
		}
		folderIDs[f.Folder] = id
	}
	res.Folders = len(folderIDs)

	for _, f := range doc.Feeds {
		feed, sourceExisted, serr := s.SubscribeOnly(ctx, sc, f.FeedURL, f.Title, f.SiteURL,
			folderIDs[f.Folder])
		if serr != nil {
			// One bad row must not abort a 151-feed migration. This is the whole
			// reason skips are a list: a private-address feed and a typo'd URL
			// both land here, and neither is a reason to lose the other 150.
			res.addSkip(ImportSkip{Title: f.Title, URL: f.FeedURL, Reason: serr.Error()})
			continue
		}
		res.Subscribed++
		if sourceExisted {
			res.Shared++
		}
		if before[feed.SourceID] {
			res.AlreadySubscribed++
		}
	}
	return res, nil
}

// addSkip records a skip, keeping the count exact past the cap on the list.
func (r *ImportResult) addSkip(s ImportSkip) {
	if len(r.Skips) < MaxImportSkips {
		r.Skips = append(r.Skips, s)
	}
}

// SkipCount is how many rows did not make it, including any past MaxImportSkips.
//
// Derived rather than stored, because a counter beside a slice is a counter that
// eventually disagrees with it.
func (r ImportResult) SkipCount() int { return len(r.Skips) }

// ExportOPML writes this reader's subscriptions out as OPML.
//
// Categories are carried. The CLI exporter dropped them — every feed came back
// flat — which makes the round trip lossy in exactly the way that matters to
// somebody who spent an evening filing 151 feeds. The importer already reads
// groups, so the exporter not writing them was a one-sided contract.
func (s *Service) ExportOPML(ctx context.Context, sc store.Scope) ([]byte, int, error) {
	feeds, _, err := s.ListFeeds(ctx, sc)
	if err != nil {
		return nil, 0, err
	}
	folders, err := s.ListFolders(ctx, sc)
	if err != nil {
		return nil, 0, err
	}
	names := make(map[string]string, len(folders))
	for _, f := range folders {
		names[f.ID] = f.Name
	}

	doc := &opml.Document{Title: "ArticleFlux"}
	for _, f := range feeds {
		doc.Feeds = append(doc.Feeds, opml.Feed{
			Title:   f.Title,
			FeedURL: f.FeedURL,
			SiteURL: f.SiteURL,
			Folder:  names[f.FolderID],
		})
	}

	var buf bytes.Buffer
	if err := opml.Write(&buf, doc); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), len(doc.Feeds), nil
}
