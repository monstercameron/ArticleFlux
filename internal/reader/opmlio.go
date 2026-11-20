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
	// Subscribed counts the subscriptions this import CREATED. A row the reader
	// already followed is not one: it is AlreadySubscribed, so a repeat import
	// reads as "nothing new" and not as the first one's number again (G12 of the
	// second UX audit — it used to count every row that ended as a subscription,
	// and a re-import said "Subscribed 3 · You already had 3").
	Subscribed int
	// AlreadySubscribed is how many rows were already in the sidebar, whether
	// from before the import or from an earlier row of the same file.
	AlreadySubscribed int
	// Shared counts the new subscriptions whose source already existed
	// server-side and was attached rather than created (A14) — a fact about the
	// deployment, not the reader.
	Shared int
	// Total is how many rows the file holds, and Next the index of the first one
	// this call did not reach (Total once the file is done). See ImportOPMLRange.
	Total, Next int
	// Folders is how many categories the file's groups resolved to.
	Folders int
	Skips   []ImportSkip

	// skipTotal is every skip, including the ones past MaxImportSkips that
	// Skips itself does not hold — see addSkip and SkipCount.
	skipTotal int
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
	return s.ImportOPMLRange(ctx, sc, data, 0, 0)
}

// ImportBatch is how many rows share one write transaction.
//
// The write pool has one connection (store.DB.Tx), so a transaction is a lock on
// every other writer in the process. A hundred rows is a few milliseconds of
// that lock; the three thousand of the audit's file is thirty such turns with
// the rest of the server getting in between them, where it used to be one
// commit per row with a full ListFeeds scan after each (G1).
const ImportBatch = 100

// ImportOPMLRange is ImportOPML for rows [start, start+max) of the file; max 0
// means "to the end".
//
// It exists so the client can walk a big file in calls that each finish well
// inside its deadline and say "n of m" between them. The whole file in one call
// was one deadline: a 3,000-row list outlasted the client's, the call was
// abandoned half way, and the report — the one thing that tells somebody what
// happened — never came (G1). Each call re-parses the file, which is a few
// hundred kilobytes and cheap beside the writes, and what it did is in its own
// result: the caller sums the counts, and Total and Next say where the file
// stands. A file that changes between calls is the caller's file and the
// caller's problem; rows are addressed by position, so an unchanged file is the
// contract.
func (s *Service) ImportOPMLRange(ctx context.Context, sc store.Scope, data []byte, start, max int) (ImportResult, error) {
	var res ImportResult
	if len(data) == 0 {
		return res, errors.New("reader: empty OPML file")
	}
	doc, err := opml.Parse(bytes.NewReader(data))
	if err != nil {
		return res, err
	}
	res.Title = doc.Title
	res.Total = len(doc.Feeds)

	if start < 0 {
		start = 0
	}
	if start > len(doc.Feeds) {
		start = len(doc.Feeds)
	}
	end := len(doc.Feeds)
	if max > 0 && start+max < end {
		end = start + max
	}
	rows := doc.Feeds[start:end]
	res.Next = end

	// The OPML folders become categories, resolved once each rather than per
	// feed: an export with 151 feeds has perhaps eight of them, and resolving
	// inside the loop would be 151 writes to create eight rows.
	//
	// This is the one place a category arrives as a NAME — see FolderByName.
	//
	// The first call resolves every folder in the file, so its Folders is the
	// file's and the later calls (which only need the ones their own rows use)
	// do not report a count the caller would have to de-duplicate. A later
	// call's failed folder is not recorded again: the first call already said.
	folderIDs := map[string]string{}
	resolve := func(name string, report bool) {
		if name == "" {
			return
		}
		if _, done := folderIDs[name]; done {
			return
		}
		id, ferr := s.FolderByName(ctx, sc, name)
		if ferr != nil {
			// An unusable category name must not cost the feeds inside it: they
			// import unfiled, which is recoverable, where skipping them is not.
			// Recorded rather than silent, so a reader whose categories did not
			// survive can see that rather than conclude the import ate them.
			if report {
				res.addSkip(ImportSkip{
					Title:  name,
					Reason: fmt.Sprintf("category not created: %v", ferr),
				})
			}
			folderIDs[name] = ""
			return
		}
		folderIDs[name] = id
	}
	if start == 0 {
		for _, f := range doc.Feeds {
			resolve(f.Folder, true)
		}
		for _, id := range folderIDs {
			if id != "" {
				res.Folders++
			}
		}
	}

	for from := 0; from < len(rows); from += ImportBatch {
		to := min(from+ImportBatch, len(rows))
		chunk := rows[from:to]

		subs := make([]store.NewSubscription, 0, len(chunk))
		origin := make([]int, 0, len(chunk)) // index in chunk, for the skip report
		for i, f := range chunk {
			resolve(f.Folder, false)
			sub, serr := newSubscription(f.FeedURL, f.Title, f.SiteURL, folderIDs[f.Folder])
			if serr != nil {
				// One bad row must not abort a 151-feed migration. This is the
				// whole reason skips are a list: a private-address feed and a
				// typo'd URL both land here, and neither is a reason to lose the
				// other 150.
				res.addSkip(ImportSkip{Title: f.Title, URL: f.FeedURL, Reason: serr.Error()})
				continue
			}
			subs = append(subs, sub)
			origin = append(origin, i)
		}
		if len(subs) == 0 {
			continue
		}
		outcomes, berr := s.repo.SubscribeBatch(ctx, sc, subs)
		if berr != nil {
			return res, berr
		}
		for j, o := range outcomes {
			f := chunk[origin[j]]
			switch {
			case o.Err != nil:
				res.addSkip(ImportSkip{Title: f.Title, URL: f.FeedURL, Reason: o.Err.Error()})
			case o.New:
				res.Subscribed++
				if o.SourceExisted {
					res.Shared++
				}
			default:
				res.AlreadySubscribed++
			}
		}
	}
	return res, nil
}

// addSkip records a skip, keeping the count exact past the cap on the list.
func (r *ImportResult) addSkip(s ImportSkip) {
	r.skipTotal++
	if len(r.Skips) < MaxImportSkips {
		r.Skips = append(r.Skips, s)
	}
}

// SkipCount is how many rows did not make it, including any past MaxImportSkips.
//
// Stored rather than derived from len(Skips): the list itself is capped at
// MaxImportSkips, so deriving the count from it silently undercounts every
// import with more than MaxImportSkips failures.
func (r ImportResult) SkipCount() int { return r.skipTotal }

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
