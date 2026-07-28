//go:build js && wasm

package view

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The Data tab: how a reading life arrives, and how it leaves (F1).
//
// Until this existed the only importer was `articleflux import`, which needs a
// shell on the server. On a single-user instance that is a documentation gap; on
// the multi-tenant server this is built to be, a member had no path at all and
// an operator had to SSH in to add a feed list. Every competitor in the matrix
// ships this in the interface, including the free self-hosted ones.
//
// Import sits ABOVE export, which is the opposite of how these screens are
// usually ordered. The reason is who is looking: somebody opens this tab on
// their first evening to bring 151 feeds in, and once every few years to take
// them out. The first-evening thing goes first.
//
// # What the report says, and why it says that much
//
// Four numbers and a list. The numbers separate "new to you" from "you already
// had this", because re-running an import is the normal way to top up after
// adding feeds elsewhere, and a screen that reported 151 subscriptions on the
// second run would be lying about what it just did. The list names the rows that
// did NOT make it, with the reason, because "12 skipped" is not something a
// person can act on — the reader wants to know which twelve, and whether they
// were feeds they cared about.
//
// Nothing here claims the articles have arrived. Import subscribes without
// fetching (§F1), so the honest sentence is that the sidebar is ready and the
// poller is filling it in — which is also what stops somebody concluding the
// import failed because a feed shows zero unread a minute later.

type dataProps struct {
	// busy is true while the file is being read or the import is running. One
	// flag for both, because from the reader's side they are one wait.
	busy bool
	// exporting is the other button's own wait. Separate from busy so that an
	// export during an import cannot make the import's button look idle.
	exporting bool
	// fileName is what was chosen, held so the result can name it. A report that
	// does not say which file it is about is one nobody can check.
	fileName string
	// result is the last import's report, or nil before there has been one.
	result *pb.ImportOpmlResponse
	// note answers the last press — an error, or the export's confirmation.
	note    string
	noteBad bool
	// feeds is the current subscription count, so the export row can say what
	// pressing it will produce rather than making the reader guess.
	feeds int
}

const (
	actDataImport = "data-import"
	actDataExport = "data-export"

	// What the chooser offers by default. A hint and never a filter: a reader
	// can always switch it to "all files", exports arrive named .xml as often as
	// .opml, and one from a scripted backup may have no extension at all. The
	// parser is what decides whether a file is a subscription list.
	opmlAccept = ".opml,.xml,text/xml,application/xml,text/x-opml"
	// The download's name. Plain and boring on purpose: it lands in a downloads
	// folder beside a hundred other files, and "feeds.opml" is what somebody
	// searching for it six months from now will type.
	opmlFileName = "feeds.opml"
	opmlMIME     = "text/x-opml"
)

func settingsData(tr i18n.Runtime, p dataProps) []ui.Node {
	importLabel := tr.T("data", "importPick")
	if p.busy {
		importLabel = tr.T("data", "importBusy")
	}
	exportLabel := tr.T("data", "exportDo", i18n.Count(p.feeds))
	if p.exporting {
		exportLabel = tr.T("data", "exportBusy")
	}

	out := []ui.Node{
		fsGroup(glyphAdd, tr.T("data", "inGroup"), tr.T("data", "inGroupHint")),
		setRow(tr.T("data", "importLabel"), tr.T("data", "importHint"),
			glyphChip(actDataImport, glyphFeeds, importLabel, false)),
		html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("data", "importPollNote"))),
	}

	// The answer to the press, whichever way it went. Above the report, because
	// a failure has no report and a reader who has just pressed a button looks
	// where the button is.
	if p.note != "" {
		out = append(out, html.Div(html.Props{
			Class: "set-note set-note-live",
			Data:  map[string]string{"bad": strconv.FormatBool(p.noteBad)},
			Role:  "status",
		}, html.Text(p.note)))
	}
	if p.result != nil {
		out = append(out, importReport(tr, p.fileName, p.result)...)
	}

	out = append(out,
		fsGroup(glyphExternal, tr.T("data", "outGroup"), tr.T("data", "outGroupHint")),
		setRow(tr.T("data", "exportLabel"), tr.T("data", "exportHint"),
			glyphChip(actDataExport, glyphFeeds, exportLabel, false)),
		html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("data", "outNote"))),
	)
	return out
}

// importReport is what the last import did.
func importReport(tr i18n.Runtime, fileName string, r *pb.ImportOpmlResponse) []ui.Node {
	// The file's own title when it had one, the filename otherwise. Both are how
	// a person confirms they picked the export they meant to; neither is
	// guaranteed to exist, and a heading reading "from" with nothing after it is
	// worse than no heading.
	from := r.GetTitle()
	if from == "" {
		from = fileName
	}

	out := []ui.Node{
		fsGroup(glyphMarkRead, tr.T("data", "reportGroup"),
			hintFrom(tr, from)),
		setFact(tr.T("data", "factSubscribed"), thousands(tr, int(r.GetSubscribed()))),
	}
	// Only when they are not zero. A row reading "0 already subscribed" on a
	// first import is a row that carries nothing, and when it does appear it
	// says something by existing.
	if r.GetAlreadySubscribed() > 0 {
		out = append(out, setFact(tr.T("data", "factAlready"),
			thousands(tr, int(r.GetAlreadySubscribed()))))
	}
	if r.GetFolders() > 0 {
		out = append(out, setFact(tr.T("data", "factFolders"),
			thousands(tr, int(r.GetFolders()))))
	}
	// The deployment's own fact (A14): a feed somebody else here already reads
	// is polled once, not twice. Shown because it is the answer to "will 151
	// feeds hammer these publishers", which is a fair thing to wonder.
	if r.GetShared() > 0 {
		out = append(out, setFact(tr.T("data", "factShared"),
			thousands(tr, int(r.GetShared()))))
	}

	skips := r.GetSkips()
	if len(skips) == 0 {
		return append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("data", "noneSkipped"))))
	}
	out = append(out, html.Div(html.Props{Class: "set-note"},
		html.Text(tr.T("data", "skippedIntro", i18n.Count(len(skips))))))
	for i, s := range skips {
		name := s.GetTitle()
		if name == "" {
			name = s.GetUrl()
		}
		out = append(out, html.Div(html.Props{
			Class: "imp-skip", Key: "skip-" + strconv.Itoa(i),
		},
			html.Span(html.Props{Class: "imp-skip-name"}, html.Text(name)),
			// The URL as well as the name, because the name is the old reader's
			// label and the address is what a person retypes into Add a feed.
			ui.If(s.GetUrl() != "" && s.GetUrl() != name, func() ui.Node {
				return html.Span(html.Props{Class: "imp-skip-url"}, html.Text(s.GetUrl()))
			}),
			html.Span(html.Props{Class: "imp-skip-why"}, html.Text(s.GetReason())),
		))
	}
	return out
}

// hintFrom names the file the report is about, or says nothing when neither the
// document nor the chooser gave a name worth printing.
func hintFrom(tr i18n.Runtime, from string) string {
	if from == "" {
		return ""
	}
	return tr.T("data", "reportFrom", i18n.Args{"name": from})
}
