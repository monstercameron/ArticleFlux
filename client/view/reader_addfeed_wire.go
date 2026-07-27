//go:build js && wasm

package view

import (
	"context"
	"strconv"
	"strings"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// The add-a-feed dialog's open and close actions.
//
// Small, and extracted anyway: it is entirely self-contained — eleven handles that no other
// section reads — so it is the cheapest possible demonstration that a section CAN leave
// reader.go without anything else moving. Every field here exists only for this dialog.
//
// Closing resets the whole dialog rather than only hiding it. A dialog that reopens holding
// the last attempt's error, candidates and half-typed URL is one that reads as broken the
// second time it is used.

type addFeedWiring struct {
	act            ui.Ref[*actions]
	addOpen        ui.State[bool]
	addNewOpen     ui.State[bool]
	addLooking     ui.State[bool]
	addSearched    ui.State[bool]
	addErr         ui.State[string]
	addFolder      ui.State[string]
	addCands       ui.State[[]*pb.FeedCandidate]
	addProposal    ui.State[*pb.ScrapeProposal]
	addSmartBusy   ui.State[bool]
	tr             i18n.Runtime
	addSmartStatus ui.State[string]
	// The ladder's own dependencies (§11). Wider than the dialog's because climbing the
	// rungs is what actually subscribes: it reads the feed list, the folder list and the
	// current selection, and on success it reloads all three.
	//
	// The five closures are Reader's, not this file's. They are passed rather than moved
	// because they are the loaders every other section calls too — relocating them here to
	// satisfy one caller would just move the coupling somewhere less obvious.
	addBusy      ui.State[bool]
	addNewCat    ui.State[string]
	addTitle     ui.State[string]
	addURL       ui.State[string]
	smartFollow  ui.State[bool]
	client       ui.State[*data.Client]
	feeds        ui.State[[]*pb.Feed]
	feedsGen     ui.Ref[int]
	folders      ui.State[[]*pb.Folder]
	foldersGen   ui.Ref[int]
	hostsRef     ui.Ref[map[string]string]
	notice       ui.State[string]
	sel          ui.State[scope]
	totalUnread  ui.State[int]
	unreadOnly   ui.State[bool]
	loadFeeds    func()
	loadFolders  func()
	loadItems    func(scope, bool)
	savePrefs    func(map[string]string)
	subscribeURL func(string)
}

// wire is called once, unconditionally, from Reader.
func (r addFeedWiring) wire() {
	// --- the add-a-feed dialog ----------------------------------------------

	// clearLadder throws away everything learned about the previous address.
	//
	// Called whenever the URL changes and whenever the dialog opens, because a
	// result that belongs to a different address is worse than no result at all:
	// it is indistinguishable from an answer about the one on screen.
	clearLadder := func() {
		r.addLooking.Set(false)
		r.addSearched.Set(false)
		r.addCands.Set(nil)
		r.addProposal.Set(nil)
		r.addSmartStatus.Set("")
		r.addSmartBusy.Set(false)
	}

	r.act.Get().openAddFeed = func() {
		r.addErr.Set("")
		r.addOpen.Set(true)
		clearLadder()
		// The categories may have changed on another device since boot, and this
		// dialog is where a stale list would be visible.
		r.loadFolders()
		// Focus has to wait for the field to exist: the dialog renders on the
		// next tick. FocusField retries on the following frame for that reason.
		platform.FocusField("add-feed")
	}
	r.act.Get().closeAddFeed = func() {
		r.addOpen.Set(false)
		r.addErr.Set("")
	}
	// The picker is single-choice, so choosing an existing category also closes
	// the new-category field: having both a chip pressed and a name typed would
	// leave two answers to one question and no rule for which wins.
	r.act.Get().pickAddFolder = func(id string) {
		r.addFolder.Set(id)
		r.addNewOpen.Set(false)
		r.addErr.Set("")
	}
	r.act.Get().toggleAddNewCat = func() {
		next := !r.addNewOpen.Get()
		r.addNewOpen.Set(next)
		r.addErr.Set("")
		if next {
			platform.FocusField("add-feed-category")
		}
	}

	// --- the ladder (§11) --------------------------------------------------

	// analyzeSite climbs the ladder for whatever is in the URL field.
	//
	// smart is passed through rather than read from the preference here: the
	// server checks the preference too, and this flag is what says the reader
	// pressed the button THIS time. Both have to hold before a page's markup
	// leaves the machine.
	r.act.Get().analyzeSite = func(smart bool) {
		c := r.client.Get()
		url := strings.TrimSpace(r.addURL.Get())
		if c == nil || url == "" {
			return
		}
		// A typo is not a site. Climbing the ladder for "not-a-url" spends a
		// round trip to be told what a two-line check already knows, and — worse
		// — replaces "that is not a feed address" with a second, vaguer message
		// about the same mistake.
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return
		}
		if r.addLooking.Get() || r.addSmartBusy.Get() {
			return
		}
		if smart {
			r.addSmartBusy.Set(true)
		} else {
			r.addLooking.Set(true)
			// The candidates from the previous attempt go now rather than when
			// the new ones arrive, so a slow search cannot show a stale list
			// next to a spinner.
			r.addCands.Set(nil)
			r.addProposal.Set(nil)
			r.addSmartStatus.Set("")
		}
		go func() {
			res, err := c.AnalyzeSite(context.Background(), url, smart)
			ui.PostAsync(func() {
				r.addLooking.Set(false)
				r.addSmartBusy.Set(false)
				r.addSearched.Set(true)
				if err != nil {
					// The subscribe error, when there is one, is the better
					// message: it is about the thing the reader did. This one
					// only speaks when nothing else has.
					if r.addErr.Get() == "" {
						r.addErr.Set(r.tr.T("reader", "errAnalyzeSite", i18n.Args{"err": err.Error()}))
					}
					return
				}
				// A result ANSWERS the failed subscribe, so its error goes —
				// whether the answer is "here are the r.feeds it points at" or
				// "no feed here, and here is what else is possible". Leaving a
				// red line saying "not a recognisable feed" above a block that
				// says the same thing in plain words is the app talking twice.
				//
				// The error survives only when the ladder itself failed, which
				// is the branch above: then it is the only thing that knows
				// anything.
				r.addErr.Set("")
				r.addCands.Set(res.GetFeeds())
				r.addSmartStatus.Set(res.GetSmartStatus())
				if res.GetScrape() != nil {
					r.addProposal.Set(res.GetScrape())
				}
				// One press, when the lamp is lit.
				//
				// The free rungs found nothing and this reader has already
				// consented — standing consent is what the lamp on the address
				// row IS — so making them press a second button to spend it
				// would be asking the same question twice. With the lamp off,
				// nothing happens here and the block explains what would.
				if !smart && len(res.GetFeeds()) == 0 && r.smartFollow.Get() {
					r.act.Get().analyzeSite(true)
				}
			})
		}()
	}

	// toggleSmartFollow is the standing consent, saved server-side like every
	// other preference so it follows the reader between machines.
	r.act.Get().toggleSmartFollow = func() {
		next := !r.smartFollow.Get()
		r.smartFollow.Set(next)
		r.savePrefs(map[string]string{smartFollowPref: strconv.FormatBool(next)})
	}

	// addCandidate subscribes to a feed the ladder found, keeping the category
	// and the name the reader had already chosen in the form.
	r.act.Get().addCandidate = func(url string) {
		if strings.TrimSpace(url) == "" {
			return
		}
		// The field is updated too, so a failure leaves the reader looking at the
		// address that failed rather than the one they typed — but the subscribe
		// takes the URL directly, because state set in this frame is not readable
		// until the next one.
		r.addURL.Set(url)
		r.subscribeURL(url)
	}

	// followPage accepts the proposal: the rule the reader just looked at is
	// sent back exactly as it was shown, and the server re-runs it against the
	// live page before writing anything.
	r.act.Get().followPage = func() {
		c := r.client.Get()
		prop := r.addProposal.Get()
		if c == nil || prop == nil || r.addBusy.Get() {
			return
		}
		url := strings.TrimSpace(r.addURL.Get())
		title := strings.TrimSpace(r.addTitle.Get())
		folderID := r.addFolder.Get()
		newCat := ""
		if r.addNewOpen.Get() {
			newCat = strings.TrimSpace(r.addNewCat.Get())
			if newCat == "" {
				r.addErr.Set(r.tr.T("reader", "errNeedCategory"))
				return
			}
		}
		r.addBusy.Set(true)
		r.addErr.Set("")
		go func() {
			if newCat != "" {
				f, err := c.CreateFolder(context.Background(), newCat)
				if err != nil {
					ui.PostAsync(func() {
						r.addBusy.Set(false)
						r.addErr.Set(r.tr.T("reader", "errNewCategory", i18n.Args{"err": err.Error()}))
					})
					return
				}
				folderID = f.GetId()
			}
			res, err := c.SubscribeScrape(context.Background(), url, title, folderID, prop.GetRule())

			// Same discipline as r.subscribeURL (~line 1730): r.folders/r.feeds are
			// fetched HERE, in this same goroutine, before the single PostAsync
			// below — not via r.loadFolders()/r.loadFeeds(), whose own nested
			// PostAsync is the one that never paints.
			var folderList []*pb.Folder
			var feedList []*pb.Feed
			var total int32
			okFolders, okFeeds := false, false
			if fl, ferr := c.ListFolders(context.Background()); ferr == nil {
				folderList, okFolders = fl, true
			}
			if err == nil {
				if fres, _, ferr := c.ListFeedsCached(context.Background()); ferr == nil {
					feedList, total, okFeeds = fres.GetFeeds(), fres.GetTotalUnread(), true
				}
			}

			ui.PostAsync(func() {
				r.addBusy.Set(false)
				if okFolders {
					r.foldersGen.Set(r.foldersGen.Get() + 1)
					r.folders.Set(folderList)
				}
				if err != nil {
					r.addErr.Set(r.tr.T("reader", "errFollowPage", i18n.Args{"err": err.Error()}))
					return
				}
				r.addOpen.Set(false)
				r.addURL.Set("")
				r.addTitle.Set("")
				r.addNewCat.Set("")
				r.addNewOpen.Set(false)
				r.addFolder.Set("")
				clearLadder()
				r.notice.Set(r.tr.T("addFeed", "followed", i18n.CountWith(int(res.GetItems()),
					i18n.Args{"name": res.GetFeed().GetTitle()})))
				if okFeeds {
					r.feedsGen.Set(r.feedsGen.Get() + 1)
					r.feeds.Set(feedList)
					r.hostsRef.Set(iconHostsOf(feedList))
					r.totalUnread.Set(int(total))
				}
				r.loadItems(r.sel.Get(), r.unreadOnly.Get())
			})
		}()
	}
}
