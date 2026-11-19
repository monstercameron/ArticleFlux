//go:build js && wasm

package view

// The kit's proof of equivalence: for every distinct shape the controls-and-
// forms family has at its call sites today, the existing helper (or, for a
// shape that only exists inline, a verbatim copy of the inline literal) is
// rendered beside the kit call meant to replace it, and the two strings must
// be identical.
//
// That is the whole adoption contract. The e2e suite and the stylesheet
// select on these bytes; a kit call that renders the same bytes as the code it
// replaces cannot break either, and one that does not is caught here rather
// than by 180 specs in a browser.
//
// Where the "before" calls the real function (fsFolders, podcastChip,
// mfDeleteControl…), a later edit to that function that the kit does not
// mirror fails this test — which is the point: until adoption, the two must
// not drift.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

type kitCase struct {
	name          string
	before, after func(tr i18n.Runtime) ui.Node
}

// kitWrap renders a []ui.Node helper's result as one node.
func kitWrap(nodes ...ui.Node) ui.Node { return html.Div(html.Props{}, nodes...) }

func kitChipCases() []kitCase {
	folders := []*pb.Folder{{Id: "f1", Name: "Tech"}, {Id: "f2", Name: "News"}}
	return []kitCase{
		// --- the helpers -------------------------------------------------------
		{"actionChip",
			func(i18n.Runtime) ui.Node { return legacyActionChip("refresh", glyphRefresh, "Fetch all") },
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: "refresh", Glyph: glyphRefresh, Label: "Fetch all"})
			}},
		{"glyphChip on",
			func(i18n.Runtime) ui.Node { return legacyGlyphChip("toggle-unread", glyphUnread, "Unread only", true) },
			func(i18n.Runtime) ui.Node {
				return kitToggle(kitChipProps{Action: "toggle-unread", Glyph: glyphUnread, Label: "Unread only"}, true)
			}},
		{"glyphChip off",
			func(i18n.Runtime) ui.Node { return legacyGlyphChip("toggle-unread", glyphUnread, "Unread only", false) },
			func(i18n.Runtime) ui.Node {
				return kitToggle(kitChipProps{Action: "toggle-unread", Glyph: glyphUnread, Label: "Unread only"}, false)
			}},
		{"glyphItemChip",
			func(i18n.Runtime) ui.Node { return legacyGlyphItemChip("like", glyphLiked, "Like", true, "it-1") },
			func(i18n.Runtime) ui.Node {
				return kitToggle(kitChipProps{Action: "like", Glyph: glyphLiked, Label: "Like", ItemID: "it-1"}, true)
			}},
		{"glyphItemAction",
			func(i18n.Runtime) ui.Node { return legacyGlyphItemAction("listen-stop", glyphStop, "Stop", "it-1") },
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: "listen-stop", Glyph: glyphStop, Label: "Stop", ItemID: "it-1"})
			}},
		{"itemChip",
			func(i18n.Runtime) ui.Node { return legacyItemChip("x", "Label", false, "it-1") },
			func(i18n.Runtime) ui.Node {
				return kitToggle(kitChipProps{Action: "x", Label: "Label", ItemID: "it-1"}, false)
			}},
		{"itemAction",
			func(i18n.Runtime) ui.Node { return itemAction("expand", "Read the rest", "it-1") },
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: "expand", Label: "Read the rest", ItemID: "it-1"})
			}},
		{"actionButton chip",
			func(i18n.Runtime) ui.Node { return legacyActionButton(actPassSet, "chip", "Save") },
			func(i18n.Runtime) ui.Node { return kitAction(kitChipProps{Action: actPassSet, Label: "Save"}) }},
		{"actionButton btn ghost close",
			func(i18n.Runtime) ui.Node { return legacyActionButton(actAddClose, "btn btn-ghost af-close", "Close") },
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: actAddClose, Look: kitChipBtn, Extra: "btn-ghost af-close", Label: "Close"})
			}},
		{"actionButton btn danger",
			func(i18n.Runtime) ui.Node { return legacyActionButton(actCatDelete, "btn fs-danger", "Delete") },
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: actCatDelete, Look: kitChipBtn, Danger: true, Label: "Delete"})
			}},
		{"actionButton btn list-more",
			func(i18n.Runtime) ui.Node { return legacyActionButton("more", "btn list-more", "Load more") },
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: "more", Look: kitChipBtn, Extra: "list-more", Label: "Load more"})
			}},
		{"actionButtonRole",
			func(i18n.Runtime) ui.Node {
				return legacyActionButtonRole("feed-settings-close", roleFeedSettingsClose, "btn btn-ghost fs-close", "Close")
			},
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: "feed-settings-close", DataRole: roleFeedSettingsClose,
					Look: kitChipBtn, Extra: "btn-ghost fs-close", Label: "Close"})
			}},
		{"pickChip",
			func(i18n.Runtime) ui.Node { return legacyPickChip(actRate, "1.1", "1.1×", true) },
			func(i18n.Runtime) ui.Node {
				return kitPick(kitChipProps{Action: actRate, Mini: true, Key: actRate + "-1.1", Label: "1.1×"}, "1.1", true)
			}},
		{"pickChip empty value",
			func(i18n.Runtime) ui.Node { return legacyPickChip(actAddFolder, "", "No category", false) },
			func(i18n.Runtime) ui.Node {
				return kitPick(kitChipProps{Action: actAddFolder, Mini: true, Key: actAddFolder + "-", Label: "No category"}, "", false)
			}},
		{"fsFolders",
			func(tr i18n.Runtime) ui.Node { return fsFolders(tr, "src-1", "f2", folders) },
			func(tr i18n.Runtime) ui.Node {
				pick := func(id, label string) ui.Node {
					return kitPick(kitChipProps{Action: "fs-folder", ItemID: "src-1", Mini: true,
						Key: "fs-folder-" + id, Label: label}, id, id == "f2")
				}
				kids := []ui.Node{pick("", tr.T("feedSettings", "noCategory"))}
				for _, f := range folders {
					kids = append(kids, pick(f.GetId(), f.GetName()))
				}
				return kitChipGroup(kitGroupChoices, "", kids...)
			}},
		{"fsChoices",
			func(i18n.Runtime) ui.Node {
				return fsChoices("fs-poll", "src-1", 3600, []fsChoice{{"Hourly", 3600}, {"Daily", 86400}})
			},
			func(i18n.Runtime) ui.Node {
				return kitChipGroup(kitGroupChoices, "",
					kitPick(kitChipProps{Action: "fs-poll", ItemID: "src-1", Mini: true, Key: "fs-poll-3600", Label: "Hourly"}, "3600", true),
					kitPick(kitChipProps{Action: "fs-poll", ItemID: "src-1", Mini: true, Key: "fs-poll-86400", Label: "Daily"}, "86400", false))
			}},
		{"mfDial",
			func(tr i18n.Runtime) ui.Node {
				return kitWrap(mfDial(mfRowProps{key: "k", target: "t1", action: "mf-steer",
					level: pb.SteerLevel_STEER_LEVEL_LESS}, tr)...)
			},
			func(tr i18n.Runtime) ui.Node {
				var kids []ui.Node
				for _, l := range steerLevels {
					kids = append(kids, kitPick(kitChipProps{Action: "mf-steer", ItemID: "t1", Mini: true,
						Key: "k-" + l.key, Label: tr.T("myFeed", "level."+l.key)},
						l.key, l.level == effectiveLevel(pb.SteerLevel_STEER_LEVEL_LESS)))
				}
				return kitWrap(kids...)
			}},
		// A row with no target: the dial before the kit, verbatim, which wrote
		// data-for-item="" rather than leaving the attribute out.
		{"mfDial, no target",
			func(tr i18n.Runtime) ui.Node {
				var kids []ui.Node
				for _, l := range steerLevels {
					kids = append(kids, html.Button(html.Props{
						Class: "chip chip-mini",
						Key:   "k-" + l.key,
						Raw: map[string]any{
							"data-action":   "mf-steer",
							"data-for-item": "",
							"data-value":    l.key,
						},
						Aria: map[string]string{"pressed": boolAttr(l.level == effectiveLevel(pb.SteerLevel_STEER_LEVEL_LESS))},
					}, html.Text(tr.T("myFeed", "level."+l.key))))
				}
				return kitWrap(kids...)
			},
			func(tr i18n.Runtime) ui.Node {
				return kitWrap(mfDial(mfRowProps{key: "k", target: "", action: "mf-steer",
					level: pb.SteerLevel_STEER_LEVEL_LESS}, tr)...)
			}},
		{"fsToggle",
			func(i18n.Runtime) ui.Node {
				return legacyFsToggle("fs-megafeed", "src-1", true, "In My Feed", "Left out")
			},
			func(i18n.Runtime) ui.Node {
				return kitSwitch(kitChipProps{Action: "fs-megafeed", ItemID: "src-1"}, true, "In My Feed", "Left out")
			}},
		{"smartToggle disabled",
			func(i18n.Runtime) ui.Node { return legacySmartToggle("classify-smart-text", false, true, "On", "Off") },
			func(i18n.Runtime) ui.Node {
				return kitSwitch(kitChipProps{Action: "classify-smart-text", Disabled: true}, false, "On", "Off")
			}},
		{"smartToggle enabled",
			func(i18n.Runtime) ui.Node { return legacySmartToggle(actSmartFeedPlus, true, false, "On", "Off") },
			func(i18n.Runtime) ui.Node {
				return kitSwitch(kitChipProps{Action: actSmartFeedPlus}, true, "On", "Off")
			}},
		{"mfDeleteControl armed",
			func(tr i18n.Runtime) ui.Node {
				return kitWrap(mfDeleteControl(myFeedRowFeed, "s1", "Rust", true, tr)...)
			},
			func(tr i18n.Runtime) ui.Node {
				return kitWrap(
					kitAction(kitChipProps{Action: actMyFeedDeleteConfirm, ItemID: "s1", Value: myFeedRowFeed,
						Data: []html.DataAttribute{{Name: "armed", Value: "true"}}, Mini: true, Danger: true,
						Label:     tr.T("myFeed", "rowDeleteConfirm"),
						AriaLabel: tr.T("myFeed", "rowDeleteConfirmAria", i18n.Args{"name": "Rust"})}),
					kitAction(kitChipProps{Action: actMyFeedDeleteCancel, Mini: true,
						Label:     tr.T("myFeed", "rowDeleteCancel"),
						AriaLabel: tr.T("myFeed", "rowDeleteCancelAria")}))
			}},
		{"mfDeleteControl resting",
			func(tr i18n.Runtime) ui.Node {
				return kitWrap(mfDeleteControl(myFeedRowFeed, "s1", "Rust", false, tr)...)
			},
			func(tr i18n.Runtime) ui.Node {
				return kitWrap(kitAction(kitChipProps{Action: actMyFeedDeleteArm, ItemID: "s1", Value: myFeedRowFeed,
					Data: []html.DataAttribute{{Name: "armed", Value: "false"}}, Mini: true,
					Label:     tr.T("myFeed", "rowDelete"),
					AriaLabel: tr.T("myFeed", "rowDeleteAria", i18n.Args{"name": "Rust"})}))
			}},
		{"listCorner icon chips",
			func(tr i18n.Runtime) ui.Node { return listCorner(tr) },
			func(tr i18n.Runtime) ui.Node {
				icon := func(action, glyph, name string) ui.Node {
					return kitAction(kitChipProps{Action: action, Glyph: glyph, Label: name, Mini: true, Icon: true})
				}
				return html.Div(html.Props{Class: "list-corner"},
					icon("open-discover", glyphDiscover, tr.T("discover", "title")),
					icon("open-settings", glyphSettings, tr.T("list", "settings")),
					icon("help-open", glyphHelp, tr.T("list", "shortcuts")))
			}},
		{"chip (list.go)",
			func(i18n.Runtime) ui.Node { return legacyChip("toggle-unread", "Unread", true) },
			func(i18n.Runtime) ui.Node {
				return kitToggle(kitChipProps{Action: "toggle-unread", Label: "Unread"}, true)
			}},
		{"connFix",
			func(i18n.Runtime) ui.Node { return connFix("reconnect", "Retry") },
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: "reconnect", Extra: "conn-fix", Label: "Retry"})
			}},
		{"pageModeChips",
			func(tr i18n.Runtime) ui.Node { return pageModeChips(tr, "it-1", true) },
			func(tr i18n.Runtime) ui.Node {
				return kitChipGroup(kitGroupModes, tr.T("article", "viewPageModes"),
					kitToggle(kitChipProps{Action: "page-mode-doc", ItemID: "it-1", Label: tr.T("article", "viewPageModeDoc")}, false),
					kitToggle(kitChipProps{Action: "page-mode-live", ItemID: "it-1", Label: tr.T("article", "viewPageModeLive")}, true))
			}},
		{"podcastChip ready",
			func(tr i18n.Runtime) ui.Node { return podcastChip(tr, "") },
			func(tr i18n.Runtime) ui.Node {
				// aria-pressed="false" on a control that opens a screen: today's
				// bytes, kept, and flagged in kitToggle's adoption notes.
				return kitToggle(kitChipProps{Action: actPodcastOpen, Glyph: glyphPodcast, Label: tr.T("list", "podcast")}, false)
			}},
		{"podcastChip blocked",
			func(tr i18n.Runtime) ui.Node { return podcastChip(tr, "voice") },
			func(tr i18n.Runtime) ui.Node {
				return kitToggle(kitChipProps{Action: actSlideNeeds, Glyph: glyphPodcast,
					Data:  []html.DataAttribute{{Name: "blocked", Value: "voice"}},
					Label: tr.T("list", "podcastBlocked")}, false)
			}},
		{"markAllChip",
			func(tr i18n.Runtime) ui.Node { return markAllChip(tr, true) },
			func(tr i18n.Runtime) ui.Node {
				return kitDisclosure(kitChipProps{Action: "mark-all-arm", Glyph: glyphMarkRead, Label: tr.T("list", "markAllRead")}, true)
			}},
		{"discoverSmartPlusToggle",
			func(tr i18n.Runtime) ui.Node { return discoverSmartPlusToggle(tr, true, true) },
			func(tr i18n.Runtime) ui.Node {
				return kitToggle(kitChipProps{TypeButton: true, Extra: "discover-smartplus", Disabled: true,
					Data:      []html.DataAttribute{{Name: strings.TrimPrefix(attrDiscoverSmartPlus, "data-"), Value: "1"}},
					AriaLabel: tr.T("discover", "smartPlusToggleLabel"),
					Label:     tr.T("discover", "smartPlusToggle")}, true)
			}},
		{"langChip with hint, busy",
			func(i18n.Runtime) ui.Node { return langChip("fr", "Français", "Paid for", true, false, "fr") },
			func(i18n.Runtime) ui.Node {
				return kitPick(kitChipProps{Action: actSmartLang, Mini: true, Extra: "lang-chip", Key: "lang-fr",
					Disabled: true, AriaLabel: "Français", DescribedBy: "lang-hint-fr",
					Kids: []ui.Node{
						html.Span(html.Props{}, html.Text("Français")),
						html.Span(html.Props{Class: "lang-hint", ID: "lang-hint-fr"}, html.Text("Paid for")),
						html.Span(html.Props{Class: "tag-wait", Aria: map[string]string{"hidden": "true"}}, html.Text(glyphSyncing)),
					}}, "fr", true)
			}},
		{"langChip plain",
			func(i18n.Runtime) ui.Node { return langChip("de", "Deutsch", "", false, false, "") },
			func(i18n.Runtime) ui.Node {
				return kitPick(kitChipProps{Action: actSmartLang, Mini: true, Extra: "lang-chip", Key: "lang-de",
					AriaLabel: "Deutsch", Kids: []ui.Node{html.Span(html.Props{}, html.Text("Deutsch"))}}, "de", false)
			}},
		{"podcastStartProps disabled",
			func(i18n.Runtime) ui.Node { return html.Button(podcastStartProps(true), html.Text("Start")) },
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: actPodcastStart, Disabled: true, Label: "Start"})
			}},
		{"ratePicker",
			func(tr i18n.Runtime) ui.Node { return ratePicker(tr, "1.3") },
			func(tr i18n.Runtime) ui.Node {
				var kids []ui.Node
				for _, r := range speechRateChoices {
					label := tr.T("settings", "rateTimes", i18n.Args{"n": r})
					if r == speechRateDefault {
						label = tr.T("settings", "rateTimesDefault", i18n.Args{"n": r})
					}
					kids = append(kids, kitPick(kitChipProps{Action: actRate, Mini: true, Key: actRate + "-" + r, Label: label}, r, r == "1.3"))
				}
				return kitChipGroup(kitGroupPicks, "", kids...)
			}},
		{"staticChip",
			func(i18n.Runtime) ui.Node { return legacyStaticChip("1,420 words") },
			func(i18n.Runtime) ui.Node { return kitStatic(kitStaticProps{Label: "1,420 words"}) }},

		// --- shapes that exist only inline: the "before" is the literal, copied
		// from the file named --------------------------------------------------
		{"inline chip-new (addfeed.go)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class: "chip chip-new",
					Key:   "af-new",
					Raw:   map[string]any{"data-action": actAddNewCat},
					Aria:  map[string]string{"pressed": "true"},
				}, html.Text("New category"))
			},
			func(i18n.Runtime) ui.Node {
				return kitToggle(kitChipProps{Action: actAddNewCat, Extra: "chip-new", Key: "af-new", Label: "New category"}, true)
			}},
		{"danger chip shape (was settings.go passClear; that site is now confirm.go armableRemove — not an adoption target)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class: "chip fs-danger",
					Raw:   map[string]any{"data-action": actPassClear},
				}, html.Text("Remove"))
			},
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: actPassClear, Danger: true, Label: "Remove"})
			}},
		{"danger glyph chip shape (was feedsettings.go Unsubscribe; now confirm.go armableRemoveFor — not an adoption target)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class: "chip fs-danger",
					Raw:   map[string]any{"data-action": "fs-unsubscribe", "data-for-item": "src-1"},
				}, lead("✕"), html.Text("Unsubscribe"))
			},
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: "fs-unsubscribe", ItemID: "src-1", Danger: true,
					Glyph: "✕", Label: "Unsubscribe"})
			}},
		{"inline sign-out armed (settings.go signOutGroup)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class: "chip fs-danger",
					Raw:   map[string]any{"data-action": actSignOutDo, "data-armed": "true"},
					Aria:  map[string]string{"busy": "false"},
				}, lead(glyphSignOut), html.Text("Sign out now"))
			},
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: actSignOutDo, Danger: true, Glyph: glyphSignOut,
					Data: []html.DataAttribute{{Name: "armed", Value: "true"}}, Busy: kitTriOf(false),
					Label: "Sign out now"})
			}},
		{"inline mfReset armed (myfeedsettings.go)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class: "chip chip-mini fs-danger",
					Raw: map[string]any{
						"data-action": actMyFeedResetConfirm, "data-value": "topics", "data-armed": "true",
					},
					Aria: map[string]string{"label": "Reset topics"},
				}, html.Text("Reset"))
			},
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: actMyFeedResetConfirm, Value: "topics",
					Data: []html.DataAttribute{{Name: "armed", Value: "true"}}, Mini: true, Danger: true,
					AriaLabel: "Reset topics", Label: "Reset"})
			}},
		{"inline banner undo (list.go)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class:    "chip chip-mini banner-undo",
					DataAttr: html.DataAttribute{Name: "action", Value: "undo-mark-all"},
				}, html.Text("Undo"))
			},
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: "undo-mark-all", Mini: true, Extra: "banner-undo", Label: "Undo"})
			}},
		{"inline mark-all confirm (panes.go markAllConfirm)",
			func(i18n.Runtime) ui.Node {
				return kitWrap(
					html.Button(html.Props{
						Class:     "chip chip-mini banner-undo fs-danger",
						DataAttrs: []html.DataAttribute{{Name: "action", Value: "mark-all"}, {Name: "armed", Value: "true"}},
					}, html.Text("Mark read")),
					html.Button(html.Props{
						Class:    "chip chip-mini banner-undo",
						DataAttr: html.DataAttribute{Name: "action", Value: "mark-all-cancel"},
						Data:     map[string]string{"role": markAllCancelRole},
					}, html.Text("Cancel")))
			},
			func(i18n.Runtime) ui.Node {
				return kitWrap(
					kitAction(kitChipProps{Action: "mark-all", Data: []html.DataAttribute{{Name: "armed", Value: "true"}},
						Mini: true, Extra: "banner-undo", Danger: true, Label: "Mark read"}),
					kitAction(kitChipProps{Action: "mark-all-cancel", DataRole: markAllCancelRole,
						Mini: true, Extra: "banner-undo", Label: "Cancel"}))
			}},
		{"inline open original (article.go)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class:     "chip",
					DataAttrs: []html.DataAttribute{{Name: "action", Value: "open-original"}, {Name: "for-item", Value: "it-1"}},
				}, html.Text("Open original"),
					html.Span(html.Props{Class: "gl-trail",
						Aria: map[string]string{"hidden": "true"}},
						html.Text(glyphExternal)))
			},
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: "open-original", ItemID: "it-1", Trail: glyphExternal, Label: "Open original"})
			}},
		{"inline page frame close (article.go)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class:     "chip chip-mini page-frame-close",
					DataAttrs: []html.DataAttribute{{Name: "action", Value: "toggle-page"}, {Name: "for-item", Value: "it-1"}},
				}, html.Text("Close"))
			},
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: "toggle-page", ItemID: "it-1", Mini: true, Extra: "page-frame-close", Label: "Close"})
			}},
		{"inline listen mini toggle (article_listen.go)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class:    "chip chip-mini",
					DataAttr: html.DataAttribute{Name: "action", Value: "toggle-smart-voice"},
					Title:    "Use the Smart+ voice",
					Aria:     map[string]string{"pressed": "false"},
				}, html.Text("Smart+ voice"))
			},
			func(i18n.Runtime) ui.Node {
				return kitToggle(kitChipProps{Action: "toggle-smart-voice", Mini: true,
					Title: "Use the Smart+ voice", Label: "Smart+ voice"}, false)
			}},
		{"inline log level (settings.go settingsActivity)",
			func(i18n.Runtime) ui.Node {
				var chips []ui.Node
				for _, l := range []string{"DEBUG", "INFO"} {
					chips = append(chips, html.Button(html.Props{
						Class: "chip chip-mini",
						Key:   "loglvl-" + l,
						Raw:   map[string]any{"data-action": "settings-loglevel", "data-value": l},
						Aria:  map[string]string{"pressed": boolAttr(l == "INFO")},
					}, html.Text(strings.ToLower(l))))
				}
				return html.Div(html.Props{Class: "fs-choices", Role: "group",
					Aria: map[string]string{"label": "Lowest level shown"}}, chips...)
			},
			func(i18n.Runtime) ui.Node {
				var chips []ui.Node
				for _, l := range []string{"DEBUG", "INFO"} {
					chips = append(chips, kitPick(kitChipProps{Action: "settings-loglevel", Mini: true,
						Key: "loglvl-" + l, Label: strings.ToLower(l)}, l, l == "INFO"))
				}
				return kitChipGroup(kitGroupChoices, "Lowest level shown", chips...)
			}},
		{"inline reading size (theme.go)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class: "chip",
					Key:   "rdsize-large",
					Raw:   map[string]any{"data-action": "set-reading", "data-value": "large"},
					Aria:  map[string]string{"pressed": "true"},
				}, html.Text("Large"))
			},
			func(i18n.Runtime) ui.Node {
				return kitPick(kitChipProps{Action: "set-reading", Key: "rdsize-large", Label: "Large"}, "large", true)
			}},
		{"inline compose go, disabled (theme.go)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class:    "chip",
					Raw:      map[string]any{"data-action": actComposeTheme},
					Disabled: true,
				}, html.Text("Compose"))
			},
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: actComposeTheme, Disabled: true, Label: "Compose"})
			}},
		{"typed-button chip shape (setup.go copy, minus OnClick and data-copied; that site has OnClick and is never converted)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class: "chip", Type: "button",
					Data: map[string]string{"role": "setup-copy"},
				}, html.Text("Copy"))
			},
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{TypeButton: true, DataRole: "setup-copy", Label: "Copy"})
			}},
		{"inline af-go busy (addfeed.go)",
			func(i18n.Runtime) ui.Node {
				return html.Button(html.Props{
					Class: "btn af-go",
					Raw:   map[string]any{"data-action": actAddSubmit},
					Aria:  map[string]string{"busy": "true"},
				}, html.Text("Working"))
			},
			func(i18n.Runtime) ui.Node {
				return kitAction(kitChipProps{Action: actAddSubmit, Look: kitChipBtn, Extra: "af-go",
					Busy: kitTriOf(true), Label: "Working"})
			}},
		{"inline static mini keyed + fs-tags (feedsettings.go, tagsettings.go)",
			func(i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "fs-tags"},
					html.Span(html.Props{Class: "chip chip-static chip-mini", Key: "fs-tag-go"}, html.Text("go")))
			},
			func(i18n.Runtime) ui.Node {
				return kitChipGroup(kitGroupTags, "", kitStatic(kitStaticProps{Label: "go", Mini: true, Key: "fs-tag-go"}))
			}},
		{"inline static chip-off (myfeedsettings.go)",
			func(i18n.Runtime) ui.Node {
				return html.Span(html.Props{Class: "chip chip-static chip-off"}, html.Text("Off"))
			},
			func(i18n.Runtime) ui.Node { return kitStatic(kitStaticProps{Label: "Off", Extra: "chip-off"}) }},
		{"inline static log count (settings.go)",
			func(i18n.Runtime) ui.Node {
				return html.Span(html.Props{Class: "chip chip-static chip-mini log-warn", Key: "lc-WARN"}, html.Text("warn 3"))
			},
			func(i18n.Runtime) ui.Node {
				return kitStatic(kitStaticProps{Label: "warn 3", Mini: true, Extra: "log-warn", Key: "lc-WARN"})
			}},
		{"inline static is-missing (podcastsettings.go)",
			func(i18n.Runtime) ui.Node {
				return html.Span(html.Props{Class: "chip chip-static is-missing"}, html.Text("Missing"))
			},
			func(i18n.Runtime) ui.Node { return kitStatic(kitStaticProps{Label: "Missing", Extra: "is-missing"}) }},
		{"set-actions row",
			func(i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "set-actions"}, legacyActionChip("settings-refresh", glyphRefresh, "Reload"))
			},
			func(i18n.Runtime) ui.Node {
				return kitChipGroup(kitGroupActions, "",
					kitAction(kitChipProps{Action: "settings-refresh", Glyph: glyphRefresh, Label: "Reload"}))
			}},
		{"fs-actions row",
			func(i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "fs-actions"}, legacyGlyphItemAction("fs-refresh", glyphRefresh, "Fetch now", "s1"))
			},
			func(i18n.Runtime) ui.Node {
				return kitChipGroup(kitGroupPanelActions, "",
					kitAction(kitChipProps{Action: "fs-refresh", Glyph: glyphRefresh, Label: "Fetch now", ItemID: "s1"}))
			}},
	}
}

func kitFormCases() []kitCase {
	input := func(id string) html.Props {
		return html.Props{Class: "field fs-field", Type: "text",
			Raw: map[string]any{"autocomplete": "off", "spellcheck": "false"}}
	}
	return []kitCase{
		{"fsRow with hint",
			func(i18n.Runtime) ui.Node { return legacyFsRow("Articles", "Which to show", legacyStaticChip("x")) },
			func(i18n.Runtime) ui.Node {
				return kitSetting(kitSettingProps{Label: "Articles", Hint: "Which to show", Control: kitStatic(kitStaticProps{Label: "x"})})
			}},
		{"fsRow without hint",
			func(i18n.Runtime) ui.Node {
				return legacyFsRow("Address", "", html.Div(html.Props{Class: "fs-url"}, html.Text("u")))
			},
			func(i18n.Runtime) ui.Node {
				return kitSetting(kitSettingProps{Label: "Address", Control: html.Div(html.Props{Class: "fs-url"}, html.Text("u"))})
			}},
		{"setRow",
			func(i18n.Runtime) ui.Node {
				return legacySetRow("Rate", "How fast", html.Span(html.Props{Class: "set-picks"}))
			},
			func(i18n.Runtime) ui.Node {
				return kitSetting(kitSettingProps{Label: "Rate", Hint: "How fast", Control: kitChipGroup(kitGroupPicks, "")})
			}},
		{"afField choice (no id)",
			func(i18n.Runtime) ui.Node {
				return legacyAfField("Category", "Where it goes", "", html.Div(html.Props{Class: "af-cats"}))
			},
			func(i18n.Runtime) ui.Node {
				return kitSetting(kitSettingProps{Look: kitFormDialog, Label: "Category", Hint: "Where it goes",
					Control: html.Div(html.Props{Class: "af-cats"})})
			}},
		{"fsField with hint",
			func(i18n.Runtime) ui.Node {
				p := input(fieldFeedTitle)
				p.ID, p.Aria = fieldFeedTitle, fieldAria(fieldFeedTitle, "Your name for it")
				return legacyFsField("Name", "Your name for it", fieldFeedTitle, html.Input(p))
			},
			func(i18n.Runtime) ui.Node {
				return kitField(kitFieldProps{Label: "Name", Hint: "Your name for it", ID: fieldFeedTitle,
					Control: html.Input(kitWire(fieldFeedTitle, "Your name for it", input(fieldFeedTitle)))})
			}},
		{"fsField without hint, external describedby (pwNew)",
			func(i18n.Runtime) ui.Node {
				p := input(fieldPwNew)
				p.ID, p.Aria = fieldPwNew, map[string]string{"describedby": pwRulesID}
				return legacyFsField("New password", "", fieldPwNew, html.Input(p))
			},
			func(i18n.Runtime) ui.Node {
				p := input(fieldPwNew)
				p.Aria = map[string]string{"describedby": pwRulesID}
				return kitField(kitFieldProps{Label: "New password", ID: fieldPwNew,
					Control: html.Input(kitWire(fieldPwNew, "", p))})
			}},
		{"fsField select (landingViewPicker's aria)",
			func(i18n.Runtime) ui.Node {
				return legacyFsField("Open on", "Where to start", fieldLanding,
					html.Select(html.Props{Class: "field", ID: fieldLanding,
						Aria: map[string]string{"label": "Open on", "describedby": fieldLanding + "-hint"}},
						html.Option(html.Props{Value: "resume", Selected: true}, html.Text("Resume"))))
			},
			func(i18n.Runtime) ui.Node {
				return kitField(kitFieldProps{Label: "Open on", Hint: "Where to start", ID: fieldLanding,
					Control: html.Select(kitWire(fieldLanding, "Where to start",
						html.Props{Class: "field", Aria: map[string]string{"label": "Open on"}}),
						html.Option(html.Props{Value: "resume", Selected: true}, html.Text("Resume")))})
			}},
		{"afField with id",
			func(i18n.Runtime) ui.Node {
				return legacyAfField("Name", "Optional", fieldAddTitle, html.Input(html.Props{Class: "field af-input",
					Type: "text", ID: fieldAddTitle, Aria: fieldAria(fieldAddTitle, "Optional")}))
			},
			func(i18n.Runtime) ui.Node {
				return kitField(kitFieldProps{Look: kitFormDialog, Label: "Name", Hint: "Optional", ID: fieldAddTitle,
					Control: html.Input(kitWire(fieldAddTitle, "Optional", html.Props{Class: "field af-input", Type: "text"}))})
			}},
		{"afFieldWith aside",
			func(i18n.Runtime) ui.Node {
				return legacyAfFieldWith("Address", "A feed or a page", fieldAddURL,
					html.Div(html.Props{Class: "af-lamps"}),
					html.Input(html.Props{Class: "field af-input", Type: "url", ID: fieldAddURL,
						Aria: fieldAria(fieldAddURL, "A feed or a page")}))
			},
			func(i18n.Runtime) ui.Node {
				return kitField(kitFieldProps{Look: kitFormDialog, Label: "Address", Hint: "A feed or a page", ID: fieldAddURL,
					Aside:   html.Div(html.Props{Class: "af-lamps"}),
					Control: html.Input(kitWire(fieldAddURL, "A feed or a page", html.Props{Class: "field af-input", Type: "url"}))})
			}},
		{"inline login field, no hint (login.go)",
			func(i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "login-field"},
					html.Label(html.Props{Class: "login-label",
						Raw: map[string]any{"for": "login-password"}},
						html.Text("Password")),
					html.Input(html.Props{Class: "field login-input", Type: "password", ID: "login-password",
						Raw: map[string]any{"autocomplete": "current-password", "enterkeyhint": "go"}}),
				)
			},
			func(i18n.Runtime) ui.Node {
				return kitField(kitFieldProps{Look: kitFormCard, Label: "Password", ID: "login-password",
					Control: html.Input(kitWire("login-password", "", html.Props{Class: "field login-input", Type: "password",
						Raw: map[string]any{"autocomplete": "current-password", "enterkeyhint": "go"}}))})
			}},
		{"inline login field with login-hint (login.go recover-code)",
			func(i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "login-field"},
					html.Label(html.Props{Class: "login-label",
						Raw: map[string]any{"for": "recover-code"}},
						html.Text("Recovery code")),
					html.Input(html.Props{Class: "field login-input", Type: "text", ID: "recover-code",
						Aria: map[string]string{"describedby": "recover-code-hint"}}),
					html.P(html.Props{Class: "login-hint", ID: "recover-code-hint"},
						html.Text("Eight letters")),
				)
			},
			func(i18n.Runtime) ui.Node {
				return kitField(kitFieldProps{Look: kitFormCard, Label: "Recovery code", Hint: "Eight letters", ID: "recover-code",
					Control: html.Input(kitWire("recover-code", "Eight letters", html.Props{Class: "field login-input", Type: "text"}))})
			}},
		{"inline setup field with login-foot (setup.go)",
			func(i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "login-field"},
					html.Label(html.Props{Class: "login-label",
						Raw: map[string]any{"for": "setup-email"}},
						html.Text("Email")),
					html.Input(html.Props{Class: "field login-input", Type: "email", ID: "setup-email",
						Aria: map[string]string{"describedby": "setup-email-hint"}}),
					html.P(html.Props{Class: "login-foot", ID: "setup-email-hint"},
						html.Text("A label, not a way back in")),
				)
			},
			func(i18n.Runtime) ui.Node {
				return kitField(kitFieldProps{Look: kitFormCard, Label: "Email", Hint: "A label, not a way back in",
					ID: "setup-email", HintClass: "login-foot",
					Control: html.Input(kitWire("setup-email", "A label, not a way back in",
						html.Props{Class: "field login-input", Type: "email"}))})
			}},
		// setup.go's four fields, each written out as it stood before the kit,
		// with every attribute but OnInput (a handler, which SSR does not print).
		{"setup username (setup.go)",
			func(tr i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "login-field"},
					html.Label(html.Props{Class: "login-label",
						Raw: map[string]any{"for": "setup-username"}},
						html.Text(tr.T("setup", "username"))),
					html.Input(html.Props{
						Class: "field login-input", Type: "text", ID: "setup-username",
						Value: "cam",
						Data:  map[string]string{"role": "setup-username"},
						Raw: map[string]any{
							"autocomplete":   "username",
							"autocapitalize": "none",
							"spellcheck":     "false",
							"enterkeyhint":   "go",
						},
					}),
				)
			},
			func(tr i18n.Runtime) ui.Node {
				return kitField(kitFieldProps{Look: kitFormCard, Label: tr.T("setup", "username"), ID: "setup-username",
					Control: html.Input(kitWire("setup-username", "", html.Props{
						Class: "field login-input", Type: "text",
						Value: "cam",
						Data:  map[string]string{"role": "setup-username"},
						Raw: map[string]any{
							"autocomplete":   "username",
							"autocapitalize": "none",
							"spellcheck":     "false",
							"enterkeyhint":   "go",
						},
					}))})
			}},
		// The address field itself is gone from setup.go (the username is the address); this
		// case stays as a record of the markup kitField still has to reproduce byte for byte
		// for a Card field with a login-foot hint, so its strings are literals now.
		{"setup email (setup.go)",
			func(i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "login-field"},
					html.Label(html.Props{Class: "login-label",
						Raw: map[string]any{"for": "setup-email"}},
						html.Text("Email")),
					html.Input(html.Props{
						Class: "field login-input", Type: "email", ID: "setup-email",
						Value: "cam@example.com",
						Data:  map[string]string{"role": "setup-email"},
						Raw:   map[string]any{"autocomplete": "email", "enterkeyhint": "go"},
						Aria:  map[string]string{"describedby": "setup-email-hint"},
					}),
					html.P(html.Props{Class: "login-foot", ID: "setup-email-hint"},
						html.Text("A label, not a way back in")),
				)
			},
			func(i18n.Runtime) ui.Node {
				return kitField(kitFieldProps{Look: kitFormCard, Label: "Email",
					Hint: "A label, not a way back in", HintClass: "login-foot", ID: "setup-email",
					Control: html.Input(kitWire("setup-email", "A label, not a way back in", html.Props{
						Class: "field login-input", Type: "email",
						Value: "cam@example.com",
						Data:  map[string]string{"role": "setup-email"},
						Raw:   map[string]any{"autocomplete": "email", "enterkeyhint": "go"},
					}))})
			}},
		{"setup password (setup.go)",
			func(tr i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "login-field"},
					html.Label(html.Props{Class: "login-label",
						Raw: map[string]any{"for": "setup-password"}},
						html.Text(tr.T("setup", "password"))),
					html.Input(html.Props{
						Class: "field login-input", Type: "password", ID: "setup-password",
						Value: "hunter2hunter2",
						Data:  map[string]string{"role": "setup-password"},
						Raw:   map[string]any{"autocomplete": "new-password", "enterkeyhint": "go"},
						Aria:  map[string]string{"describedby": "setup-password-hint"},
					}),
					html.P(html.Props{Class: "login-foot", ID: "setup-password-hint"},
						html.Text(tr.T("setup", "passwordHint"))),
				)
			},
			func(tr i18n.Runtime) ui.Node {
				return kitField(kitFieldProps{Look: kitFormCard, Label: tr.T("setup", "password"),
					Hint: tr.T("setup", "passwordHint"), HintClass: "login-foot", ID: "setup-password",
					Control: html.Input(kitWire("setup-password", tr.T("setup", "passwordHint"), html.Props{
						Class: "field login-input", Type: "password",
						Value: "hunter2hunter2",
						Data:  map[string]string{"role": "setup-password"},
						Raw:   map[string]any{"autocomplete": "new-password", "enterkeyhint": "go"},
					}))})
			}},
		{"setup confirm (setup.go)",
			func(tr i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "login-field"},
					html.Label(html.Props{Class: "login-label",
						Raw: map[string]any{"for": "setup-confirm"}},
						html.Text(tr.T("setup", "confirm"))),
					html.Input(html.Props{
						Class: "field login-input", Type: "password", ID: "setup-confirm",
						Value: "hunter2hunter2",
						Data:  map[string]string{"role": "setup-confirm"},
						Raw:   map[string]any{"autocomplete": "new-password", "enterkeyhint": "go"},
					}),
				)
			},
			func(tr i18n.Runtime) ui.Node {
				return kitField(kitFieldProps{Look: kitFormCard, Label: tr.T("setup", "confirm"), ID: "setup-confirm",
					Control: html.Input(kitWire("setup-confirm", "", html.Props{
						Class: "field login-input", Type: "password",
						Value: "hunter2hunter2",
						Data:  map[string]string{"role": "setup-confirm"},
						Raw:   map[string]any{"autocomplete": "new-password", "enterkeyhint": "go"},
					}))})
			}},
		{"fsGroup with note",
			func(i18n.Runtime) ui.Node { return legacyFsGroup(glyphShared, "Shared", "Others read this") },
			func(i18n.Runtime) ui.Node { return kitHeading(glyphShared, "Shared", "Others read this") }},
		{"fsGroup without note",
			func(i18n.Runtime) ui.Node { return legacyFsGroup(glyphHealth, "Health", "") },
			func(i18n.Runtime) ui.Node { return kitHeading(glyphHealth, "Health", "") }},
		{"fsFacts",
			func(i18n.Runtime) ui.Node {
				return legacyFsFacts(legacyFsFact("Last fetched", "3 minutes ago"), legacyFsFact("Next fetch", "in 12 minutes"))
			},
			func(i18n.Runtime) ui.Node {
				return kitFacts(kitFactsPanel, kitFact("Last fetched", "3 minutes ago"), kitFact("Next fetch", "in 12 minutes"))
			}},
		{"setFacts",
			func(i18n.Runtime) ui.Node { return setFacts(setFact("Version", "1.2"), setFact("Uptime", "3 days")) },
			func(i18n.Runtime) ui.Node {
				return kitFacts(kitFactsTab, kitFact("Version", "1.2"), kitFact("Uptime", "3 days"))
			}},
		{"fs-error shown and absent",
			func(i18n.Runtime) ui.Node {
				msg := "Wrong password"
				return kitWrap(
					ui.If(msg != "", func() ui.Node {
						return html.Div(html.Props{Class: "fs-error", Role: "alert"}, html.Text(msg))
					}),
					ui.If(false, func() ui.Node { return html.Div(html.Props{Class: "fs-error"}) }))
			},
			func(i18n.Runtime) ui.Node {
				return kitWrap(kitFieldError(kitFormSettings, "Wrong password"), kitFieldError(kitFormSettings, ""))
			}},
		{"af-error",
			func(i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "af-error", Role: "alert"}, html.Text("Not a feed"))
			},
			func(i18n.Runtime) ui.Node { return kitFieldError(kitFormDialog, "Not a feed") }},
		{"login error, both states",
			func(i18n.Runtime) ui.Node {
				slot := func(msg string) ui.Node {
					return html.Div(html.Props{Class: errClass(msg), Role: "alert"}, html.Text(msg))
				}
				return kitWrap(slot(""), slot("No such user"))
			},
			func(i18n.Runtime) ui.Node {
				return kitWrap(kitFieldError(kitFormCard, ""), kitFieldError(kitFormCard, "No such user"))
			}},
	}
}

// TestKitByteIdentical is the adoption contract: each kit call renders exactly
// what the code it replaces renders today.
func TestKitByteIdentical(t *testing.T) {
	for _, c := range append(kitChipCases(), kitFormCases()...) {
		t.Run(c.name, func(t *testing.T) {
			before := renderView(t, c.before)
			after := renderView(t, c.after)
			if before != after {
				t.Errorf("kit output differs from today's\nbefore: %s\nafter:  %s", before, after)
			}
		})
	}
}

var (
	kitButtonRE = regexp.MustCompile(`(?s)<button([^>]*)>(.*?)</button>`)
	kitHiddenRE = regexp.MustCompile(`(?s)<span[^>]*aria-hidden="true"[^>]*>.*?</span>`)
	kitTagRE    = regexp.MustCompile(`<[^>]*>`)
	kitIDRE     = regexp.MustCompile(`\sid="([^"]*)"`)
	kitRefRE    = regexp.MustCompile(`\s(for|aria-labelledby|aria-describedby)="([^"]*)"`)
)

// TestKitControlsHaveNames: every button the kit draws, in every shape above,
// has an accessible name — words a screen reader can read, not counting the
// glyphs it is told to skip, or an aria-label.
func TestKitControlsHaveNames(t *testing.T) {
	for _, c := range kitChipCases() {
		out := renderView(t, c.after)
		for _, m := range kitButtonRE.FindAllStringSubmatch(out, -1) {
			text := strings.TrimSpace(kitTagRE.ReplaceAllString(kitHiddenRE.ReplaceAllString(m[2], ""), ""))
			if text == "" && !strings.Contains(m[1], `aria-label="`) {
				t.Errorf("%s: a button with no accessible name: %s", c.name, m[0])
			}
		}
	}
}

// kitEveryProp is a chip with every prop that is not a pressed state set, so
// the invariants below are tested against the most an action can carry.
func kitEveryProp() kitChipProps {
	return kitChipProps{Action: "a", Label: "Do it", Glyph: glyphAction, Trail: glyphExternal,
		ItemID: "i", Value: "v", DataRole: "r", Data: []html.DataAttribute{{Name: "armed", Value: "true"}},
		Key: "k", Mini: true, Extra: "x", Danger: true, Title: "Does it", AriaLabel: "Do it now",
		DescribedBy: "d", Busy: kitTriOf(true), Disabled: true, TypeButton: true}
}

// TestKitActionNeverPressed: nothing an action can be given puts aria-pressed
// on it, and neither does a disclosure. The structural half of this — that
// kitChipProps has no field for it — is the compiler's.
func TestKitActionNeverPressed(t *testing.T) {
	for name, n := range map[string]func() ui.Node{
		"action, every prop": func() ui.Node { return kitAction(kitEveryProp()) },
		"action, icon":       func() ui.Node { return kitAction(kitChipProps{Action: "a", Label: "Go", Glyph: "?", Icon: true}) },
		"action, btn":        func() ui.Node { return kitAction(kitChipProps{Action: "a", Label: "Go", Look: kitChipBtn}) },
		"action, zero":       func() ui.Node { return kitAction(kitChipProps{Label: "Go"}) },
		"disclosure":         func() ui.Node { return kitDisclosure(kitEveryProp(), false) },
	} {
		out := renderView(t, func(i18n.Runtime) ui.Node { return n() })
		if strings.Contains(out, "aria-pressed") {
			t.Errorf("%s carries aria-pressed: %s", name, out)
		}
	}
}

// TestKitToggleAlwaysPressed: every toggle states its position, off included.
func TestKitToggleAlwaysPressed(t *testing.T) {
	for _, on := range []bool{true, false} {
		want := `aria-pressed="` + boolAttr(on) + `"`
		for name, n := range map[string]func() ui.Node{
			"toggle":        func() ui.Node { return kitToggle(kitEveryProp(), on) },
			"toggle, zero":  func() ui.Node { return kitToggle(kitChipProps{Label: "On"}, on) },
			"pick":          func() ui.Node { return kitPick(kitChipProps{Action: "p", Label: "One"}, "", on) },
			"switch":        func() ui.Node { return kitSwitch(kitChipProps{Action: "s"}, on, "On", "Off") },
			"switch, every": func() ui.Node { return kitSwitch(kitEveryProp(), on, "On", "Off") },
		} {
			out := renderView(t, func(i18n.Runtime) ui.Node { return n() })
			if !strings.Contains(out, want) {
				t.Errorf("%s (on=%v) lacks %s: %s", name, on, want, out)
			}
		}
	}
}

// TestKitPickCarriesEmptyValue: "No category" is the empty value, and the
// handler can only see it was chosen if data-value="" is there.
func TestKitPickCarriesEmptyValue(t *testing.T) {
	out := renderView(t, func(i18n.Runtime) ui.Node {
		return kitPick(kitChipProps{Action: "p", Label: "None"}, "", true)
	})
	if !strings.Contains(out, `data-value=""`) {
		t.Errorf("an empty choice lost its data-value: %s", out)
	}
}

// TestKitKeepEmptyItem: KeepEmptyItem writes data-for-item="" for an empty id,
// on every constructor that takes one, and changes nothing when the id is set
// or the prop is not.
func TestKitKeepEmptyItem(t *testing.T) {
	keep := kitChipProps{Action: "a", Label: "Go", KeepEmptyItem: true}
	plain := kitChipProps{Action: "a", Label: "Go"}
	for name, n := range map[string]func(p kitChipProps) ui.Node{
		"action": kitAction,
		"toggle": func(p kitChipProps) ui.Node { return kitToggle(p, true) },
		"pick":   func(p kitChipProps) ui.Node { return kitPick(p, "v", true) },
		"switch": func(p kitChipProps) ui.Node { return kitSwitch(p, true, "On", "Off") },
	} {
		render := func(p kitChipProps) string {
			return renderView(t, func(i18n.Runtime) ui.Node { return n(p) })
		}
		if out := render(keep); !strings.Contains(out, `data-for-item=""`) {
			t.Errorf("%s with KeepEmptyItem lost its empty data-for-item: %s", name, out)
		}
		if out := render(plain); strings.Contains(out, "data-for-item") {
			t.Errorf("%s without KeepEmptyItem grew a data-for-item: %s", name, out)
		}
		withID, keepID := plain, keep
		withID.ItemID, keepID.ItemID = "it-1", "it-1"
		if a, b := render(withID), render(keepID); a != b {
			t.Errorf("%s: KeepEmptyItem changed a control that has an id\n%s\n%s", name, a, b)
		}
	}
}

// TestKitFieldReferencesResolve: every for, aria-labelledby and
// aria-describedby a kit row or field emits — with its control built through
// kitWire — names an id that is in the same output. A reference to a missing
// element is a broken association that no screen reader reports.
func TestKitFieldReferencesResolve(t *testing.T) {
	var cases []func() ui.Node
	for _, look := range []kitFormLook{kitFormSettings, kitFormDialog, kitFormCard} {
		for _, hint := range []string{"", "What it is for"} {
			look, hint := look, hint
			cases = append(cases,
				func() ui.Node {
					return kitField(kitFieldProps{Look: look, Label: "Name", Hint: hint, ID: "f-name",
						Control: html.Input(kitWire("f-name", hint, html.Props{Class: "field"}))})
				},
				func() ui.Node {
					return kitSetting(kitSettingProps{Look: look, Label: "Articles", Hint: hint,
						Control: kitToggle(kitChipProps{Action: "t", Label: "Unread"}, true)})
				})
		}
	}
	for i, n := range cases {
		out := renderView(t, func(i18n.Runtime) ui.Node { return n() })
		ids := map[string]bool{}
		for _, m := range kitIDRE.FindAllStringSubmatch(out, -1) {
			ids[m[1]] = true
		}
		refs := kitRefRE.FindAllStringSubmatch(out, -1)
		if len(refs) == 0 {
			t.Errorf("case %d: a field or row with no association at all: %s", i, out)
		}
		for _, m := range refs {
			for _, ref := range strings.Fields(m[2]) {
				if !ids[ref] {
					t.Errorf("case %d: %s=%q names no element here: %s", i, m[1], ref, out)
				}
			}
		}
	}
}

// TestKitWireKeepsCallerAria: kitWire copies the caller's map rather than
// writing to it, and adds the hint after a describedby already there.
func TestKitWireKeepsCallerAria(t *testing.T) {
	mine := map[string]string{"describedby": pwRulesID}
	got := kitWire("f", "hint", html.Props{Aria: mine})
	if mine["describedby"] != pwRulesID {
		t.Errorf("kitWire wrote to the caller's Aria map: %v", mine)
	}
	if got.Aria["describedby"] != pwRulesID+" f-hint" || got.ID != "f" {
		t.Errorf("kitWire = id %q describedby %q", got.ID, got.Aria["describedby"])
	}
	again := kitWire("f", "hint", got)
	if again.Aria["describedby"] != pwRulesID+" f-hint" {
		t.Errorf("kitWire added the hint twice: %q", again.Aria["describedby"])
	}
}
