package demodata

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/opml"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Migration in the demo (F1): the two RPCs behind Settings → Data.
//
// These are here rather than left unserved because they are the one pair of
// features a demo can honour COMPLETELY. Everything else the demo refuses is
// refused for a reason a static page cannot argue with — an OpenAI key it must
// not hold, a fetch it cannot make. Import and export need neither: OPML is a
// file the browser already has and a file the browser can be handed back, and
// the parser and writer are internal/opml, exactly the ones the server uses.
//
// Which makes the demo's answer to "can I get my subscriptions in, and can I get
// them out again" a demonstration rather than a promise, and that question is
// the one somebody evaluating a self-hosted reader asks first.
//
// The one honest difference is what an import produces: the server subscribes
// and then POLLS, and the articles arrive behind the import as each feed is
// reached. Nothing here can fetch, so an imported feed is stocked with the same
// visibly-generated placeholders Subscribe uses (seed.go's stockItems), which
// say what they are rather than inventing plausible headlines under a real
// publisher's name.

// maxImportBytes mirrors grpcsrv.MaxImportBytes rather than internal/opml's own
// 8 MiB. The server's bound is about the tunnel's 4 MiB receive limit, and a
// demo that accepted a file the real application would refuse would be teaching
// the wrong limit — one the reader only discovers after migrating.
const maxImportBytes = 2 << 20

func (s *readerService) ImportOpml(_ context.Context, req *pb.ImportOpmlRequest) (
	*pb.ImportOpmlResponse, error) {

	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	data := req.GetOpml()
	switch {
	case len(data) == 0:
		return nil, status.Error(codes.InvalidArgument, "that file was empty")
	case len(data) > maxImportBytes:
		return nil, status.Error(codes.InvalidArgument,
			"that file is too large to import — a subscription list is normally a few dozen kilobytes")
	}

	doc, err := opml.Parse(bytes.NewReader(data))
	if err != nil {
		if errors.Is(err, opml.ErrNotOPML) {
			// The reader picked the wrong file, which is something they can fix
			// — so it is a sentence about the file, not a swallowed Internal.
			return nil, status.Error(codes.InvalidArgument,
				"that file is not an OPML subscription list — export one from your old reader and pick that")
		}
		return nil, status.Error(codes.Internal, "that file could not be read")
	}

	res := &pb.ImportOpmlResponse{Title: doc.Title}

	// Folders first, so a feed's row can be attached to one in the same pass.
	// Counted only when created: an import re-run into the categories it made
	// last time reporting "3 folders" both times would be describing work that
	// did not happen the second time.
	folderIDs := map[string]string{}
	for _, name := range doc.Folders {
		if fo := in.folderByName(name); fo != nil {
			folderIDs[name] = fo.id
			continue
		}
		fo := &folder{id: in.nextID("fold"), name: name, position: int32(len(in.folders))}
		in.folders = append(in.folders, fo)
		folderIDs[name] = fo.id
		res.Folders++
	}

	for _, f := range doc.Feeds {
		url := strings.TrimSpace(f.FeedURL)
		// The server validates an address by FETCHING it, which is the one thing
		// this instance cannot do — so it applies the half of the check that
		// needs no network, exactly as Subscribe does, and skips the row with a
		// reason rather than creating a subscription to nothing.
		if url == "" || !strings.Contains(url, ".") {
			res.Skips = append(res.Skips, &pb.ImportSkip{
				Title: f.Title, Url: f.FeedURL,
				Reason: "that does not look like a feed address",
			})
			continue
		}
		res.Subscribed++
		if existing := in.feedByURL(url); existing != nil {
			res.AlreadySubscribed++
			continue
		}
		nf := in.addFeed(url, f.Title, folderIDs[f.Folder], "feed")
		// addFeed fabricates host/feed.xml, which is right for Subscribe — the
		// reader types a SITE there and the demo cannot go and discover the feed
		// on it. An OPML row is the opposite case: xmlUrl is the feed's actual
		// address, already known, and rewriting it would mean a list that did
		// not survive a round trip through this reader — the one thing an
		// exporter exists to guarantee.
		nf.feedURL = url
		if site := strings.TrimSpace(f.SiteURL); site != "" {
			nf.siteURL = site
		}
		in.stockItems(nf, 1)
	}
	in.resort()

	// shared stays zero, and that is a fact about this deployment rather than a
	// gap: A14's shared sources exist so two tenants polling the same feed poll
	// it once, and a demo instance has exactly one reader.
	in.logf("INFO", "imported a subscription list",
		"feeds", strconv.Itoa(int(res.Subscribed)),
		"skipped", strconv.Itoa(len(res.Skips)))
	return res, nil
}

func (s *readerService) ExportOpml(_ context.Context, _ *pb.ExportOpmlRequest) (
	*pb.ExportOpmlResponse, error) {

	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	doc := &opml.Document{Title: "ArticleFlux"}
	for _, f := range in.feeds {
		e := opml.Feed{Title: f.title(), FeedURL: f.feedURL, SiteURL: f.siteURL}
		if fo := in.folderByID(f.folderID); fo != nil {
			e.Folder = fo.name
		}
		doc.Feeds = append(doc.Feeds, e)
	}

	var buf bytes.Buffer
	if err := opml.Write(&buf, doc); err != nil {
		return nil, status.Error(codes.Internal, "the export could not be written")
	}
	return &pb.ExportOpmlResponse{Opml: buf.Bytes(), Feeds: int32(len(doc.Feeds))}, nil
}
