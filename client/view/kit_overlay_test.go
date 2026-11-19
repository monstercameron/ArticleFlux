//go:build js && wasm

package view

// The adoption contract for kit_overlay.go.
//
// Each test below renders one existing call site's markup — COPIED here as it
// stood when the kit was written, not called, so that a later edit to the
// screen cannot quietly move the goalposts — beside the kit call meant to
// replace it, and asserts the two serialize to the same bytes. A site may adopt
// the kit exactly when its current markup still matches the "before" here; if
// it has drifted, the drift is the thing to look at first.
//
// The strings inside are catalog lookups or plain test data. Whether a key
// resolves does not matter to an equality test — both halves render the same
// identifier — so the keys are the real ones only for readability.

import (
	"strconv"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// kitOverlaySame renders before and after and fails with both when they differ.
func kitOverlaySame(t *testing.T, name string, before, after func(tr i18n.Runtime) ui.Node) {
	t.Helper()
	b := renderView(t, before)
	a := renderView(t, after)
	if a != b {
		t.Errorf("%s: kit markup differs from the hand-written original\nbefore: %s\nafter:  %s", name, b, a)
	}
	if strings.TrimSpace(b) == "" {
		t.Errorf("%s: rendered nothing, so the comparison proves nothing", name)
	}
}

// kitOverlayWrap puts a []ui.Node under one element, for the parts that are returned
// as slices (kitArmParts.Nodes, settings groups).
func kitOverlayWrap(kids ...ui.Node) ui.Node { return html.Div(html.Props{Class: "kit-test"}, kids...) }

// --- dialogs -------------------------------------------------------------------

// addfeed.go addFeed: af head with close, body, foot with an error and actions.
func TestKitDialogAddFeed(t *testing.T) {
	for _, tc := range []struct {
		open      bool
		err, busy string
	}{{true, "", ""}, {false, "", ""}, {true, "no feed there", "true"}} {
		busy := tc.busy == "true"
		body := func() []ui.Node {
			return []ui.Node{html.Div(html.Props{Class: "af-field"}, html.Text("field"))}
		}
		kitOverlaySame(t, "addFeed/"+strconv.FormatBool(tc.open)+"/"+tc.err,
			func(tr i18n.Runtime) ui.Node {
				return scrim(tc.open, "add-feed-close",
					html.Div(html.Props{Class: "af", Role: "dialog",
						Raw:  map[string]any{"data-action": "modal-keep"},
						Aria: map[string]string{"modal": "true", "label": tr.T("addFeed", "title")}},
						html.Div(html.Props{Class: "af-head"},
							html.H2(html.Props{Class: "af-mark"}, html.Text(tr.T("addFeed", "title"))),
							legacyActionButton("add-feed-close", "btn btn-ghost af-close", tr.T("addFeed", "close")),
						),
						html.Div(html.Props{Class: "af-body"}, body()...),
						html.Div(html.Props{Class: "af-foot"},
							ui.If(tc.err != "", func() ui.Node {
								return html.Div(html.Props{Class: "af-error", Role: "alert"},
									html.Text(tc.err))
							}),
							html.Div(html.Props{Class: "af-actions"},
								legacyActionButton("add-feed-close", "btn btn-ghost", tr.T("addFeed", "cancel")),
								html.Button(html.Props{
									Class: "btn af-go",
									Raw:   map[string]any{"data-action": "add-feed-submit"},
									Aria:  map[string]string{"busy": strconv.FormatBool(busy)},
								}, html.Text(tr.T("addFeed", "submit"))),
							),
						),
					),
				)
			},
			func(tr i18n.Runtime) ui.Node {
				var errNode ui.Node
				if tc.err != "" {
					errNode = kitAlert("af-error", tc.err)
				}
				return kitDialog(kitDialogProps{Open: tc.open, CloseAction: "add-feed-close",
					Class: "af", Title: tr.T("addFeed", "title")},
					kitDialogHead(kitDialogHeadProps{Prefix: "af", Title: tr.T("addFeed", "title"),
						Close: kitDialogClose("af", "add-feed-close", "", tr.T("addFeed", "close"))}),
					kitDialogBody("af", body()...),
					kitDialogFoot("af", []ui.Node{errNode},
						legacyActionButton("add-feed-close", "btn btn-ghost", tr.T("addFeed", "cancel")),
						html.Button(html.Props{
							Class: "btn af-go",
							Raw:   map[string]any{"data-action": "add-feed-submit"},
							Aria:  map[string]string{"busy": strconv.FormatBool(busy)},
						}, html.Text(tr.T("addFeed", "submit"))),
					),
				)
			})
	}
}

// addfeed.go categoryDialog: af-narrow, the arm-then-confirm delete in the
// button row and its warning in the foot.
func TestKitDialogCategory(t *testing.T) {
	const name = "Science"
	for _, tc := range []struct {
		confirm bool
		err     string
	}{{false, ""}, {true, ""}, {true, "taken"}, {false, "taken"}} {
		kitOverlaySame(t, "category/"+strconv.FormatBool(tc.confirm)+"/"+tc.err,
			func(tr i18n.Runtime) ui.Node {
				danger := legacyActionButton("category-delete", "btn fs-danger", tr.T("category", "delete"))
				if tc.confirm {
					danger = legacyActionButton("category-delete-confirm", "btn fs-danger", tr.T("category", "confirm"))
				}
				return scrim(true, "category-close",
					html.Div(html.Props{Class: "af af-narrow", Role: "dialog",
						Raw:  map[string]any{"data-action": "modal-keep"},
						Aria: map[string]string{"modal": "true", "label": tr.T("category", "title")}},
						html.Div(html.Props{Class: "af-head"},
							html.H2(html.Props{Class: "af-mark"}, html.Text(name)),
							legacyActionButton("category-close", "btn btn-ghost af-close", tr.T("category", "close")),
						),
						html.Div(html.Props{Class: "af-body"}, html.Text("field")),
						html.Div(html.Props{Class: "af-foot"},
							ui.If(tc.err != "", func() ui.Node {
								return html.Div(html.Props{Class: "af-error", Role: "alert"},
									html.Text(tc.err))
							}),
							ui.If(tc.confirm, func() ui.Node {
								return html.Div(html.Props{Class: "af-warn", Role: "alert"},
									html.Text(tr.T("category", "confirmWarn", i18n.Args{"name": name})))
							}),
							html.Div(html.Props{Class: "af-actions"},
								danger,
								html.Div(html.Props{Class: "af-spring"}),
								legacyActionButton("category-close", "btn btn-ghost", tr.T("category", "cancel")),
								html.Button(html.Props{
									Class: "btn af-go",
									Raw:   map[string]any{"data-action": "category-save"},
									Aria:  map[string]string{"busy": "false"},
								}, html.Text(tr.T("category", "save"))),
							),
						),
					),
				)
			},
			func(tr i18n.Runtime) ui.Node {
				arm := kitArm(kitArmProps{Armed: tc.confirm,
					Action: "category-delete", ArmedAction: "category-delete-confirm",
					Label: tr.T("category", "delete"), ArmedLabel: tr.T("category", "confirm"),
					Look: kitChipBtn, Danger: true,
					Warn: tr.T("category", "confirmWarn", i18n.Args{"name": name}), WarnClass: "af-warn"})
				var errNode ui.Node
				if tc.err != "" {
					errNode = kitAlert("af-error", tc.err)
				}
				return kitDialog(kitDialogProps{Open: true, CloseAction: "category-close",
					Class: "af af-narrow", Title: tr.T("category", "title")},
					kitDialogHead(kitDialogHeadProps{Prefix: "af", Title: name,
						Close: kitDialogClose("af", "category-close", "", tr.T("category", "close"))}),
					kitDialogBody("af", html.Text("field")),
					kitDialogFoot("af", []ui.Node{errNode, arm.Warn},
						arm.Button,
						html.Div(html.Props{Class: "af-spring"}),
						legacyActionButton("category-close", "btn btn-ghost", tr.T("category", "cancel")),
						html.Button(html.Props{
							Class: "btn af-go",
							Raw:   map[string]any{"data-action": "category-save"},
							Aria:  map[string]string{"busy": "false"},
						}, html.Text(tr.T("category", "save"))),
					),
				)
			})
	}
}

// feedsettings.go feedSettings and tagsettings.go tagSettings: fs head with an
// optional lead glyph, the saving marker, and a focusable close.
func TestKitDialogSettingsPanels(t *testing.T) {
	for _, saving := range []bool{false, true} {
		kitOverlaySame(t, "feedSettings/"+strconv.FormatBool(saving),
			func(tr i18n.Runtime) ui.Node {
				return scrim(true, "feed-settings-close",
					html.Div(html.Props{Class: "fs", Role: "dialog",
						Raw:  map[string]any{"data-action": "modal-keep"},
						Aria: map[string]string{"modal": "true", "label": tr.T("feedSettings", "title")}},
						html.Div(html.Props{Class: "fs-head"},
							html.H2(html.Props{Class: "fs-mark"}, html.Text("A Feed")),
							ui.If(saving, func() ui.Node {
								return html.Span(html.Props{Class: "fs-saving"},
									html.Text(tr.T("feedSettings", "saving")))
							}),
							legacyActionButtonRole("feed-settings-close", "feed-settings-close",
								"btn btn-ghost fs-close", tr.T("feedSettings", "close")),
						),
						html.Div(html.Props{Class: "fs-body"},
							html.Div(html.Props{Class: "fs-error", Role: "alert"}, html.Text("gone"))),
					),
				)
			},
			func(tr i18n.Runtime) ui.Node {
				return kitDialog(kitDialogProps{Open: true, CloseAction: "feed-settings-close",
					Class: "fs", Title: tr.T("feedSettings", "title")},
					kitDialogHead(kitDialogHeadProps{Prefix: "fs", Title: "A Feed",
						After: []ui.Node{kitDialogSaving(saving, tr.T("feedSettings", "saving"))},
						Close: kitDialogClose("fs", "feed-settings-close", "feed-settings-close",
							tr.T("feedSettings", "close"))}),
					kitDialogBody("fs", kitAlert("fs-error", "gone")),
				)
			})

		kitOverlaySame(t, "tagSettings/"+strconv.FormatBool(saving),
			func(tr i18n.Runtime) ui.Node {
				return scrim(false, "tag-settings-close",
					html.Div(html.Props{Class: "fs", Role: "dialog",
						Raw:  map[string]any{"data-action": "modal-keep"},
						Aria: map[string]string{"modal": "true", "label": tr.T("tagSettings", "title")}},
						html.Div(html.Props{Class: "fs-head"},
							lead("#"),
							html.H2(html.Props{Class: "fs-mark"}, html.Text("rust")),
							ui.If(saving, func() ui.Node {
								return html.Span(html.Props{Class: "fs-saving"}, html.Text(tr.T("tagSettings", "saving")))
							}),
							legacyActionButtonRole("tag-settings-close", "tag-settings-close-btn",
								"btn btn-ghost fs-close", tr.T("tagSettings", "close")),
						),
						html.Div(html.Props{Class: "fs-body"}, html.Text("body")),
					),
				)
			},
			func(tr i18n.Runtime) ui.Node {
				return kitDialog(kitDialogProps{Open: false, CloseAction: "tag-settings-close",
					Class: "fs", Title: tr.T("tagSettings", "title")},
					kitDialogHead(kitDialogHeadProps{Prefix: "fs",
						Before: []ui.Node{lead("#")},
						Title:  "rust",
						After:  []ui.Node{kitDialogSaving(saving, tr.T("tagSettings", "saving"))},
						Close: kitDialogClose("fs", "tag-settings-close", "tag-settings-close-btn",
							tr.T("tagSettings", "close"))}),
					kitDialogBody("fs", html.Text("body")),
				)
			})
	}
}

// settings.go credConfirmDialog: described by its lead, a head with no close.
func TestKitDialogCredConfirm(t *testing.T) {
	kitOverlaySame(t, "credConfirm",
		func(tr i18n.Runtime) ui.Node {
			title := tr.T("settings", "pwConfirmTitle")
			return scrim(true, "cred-cancel",
				html.Div(html.Props{Class: "fs cred-confirm", Role: "dialog",
					Raw: map[string]any{"data-action": "modal-keep"},
					Aria: map[string]string{"modal": "true", "label": title,
						"describedby": "cred-lead"}},
					html.Div(html.Props{Class: "fs-head"},
						html.H2(html.Props{Class: "fs-mark"}, html.Text(title))),
					html.Div(html.Props{Class: "fs-body"},
						html.P(html.Props{Class: "set-note", ID: "cred-lead"}, html.Text("lead")),
						html.Div(html.Props{Class: "set-actions"},
							legacyActionButton("cred-confirm", "chip", "yes"),
							legacyActionButton("cred-cancel", "chip", "no"),
						),
					),
				),
			)
		},
		func(tr i18n.Runtime) ui.Node {
			title := tr.T("settings", "pwConfirmTitle")
			return kitDialog(kitDialogProps{Open: true, CloseAction: "cred-cancel",
				Class: "fs cred-confirm", Title: title, DescribedBy: "cred-lead"},
				kitDialogHead(kitDialogHeadProps{Prefix: "fs", Title: title}),
				kitDialogBody("fs",
					html.P(html.Props{Class: "set-note", ID: "cred-lead"}, html.Text("lead")),
					html.Div(html.Props{Class: "set-actions"},
						legacyActionButton("cred-confirm", "chip", "yes"),
						legacyActionButton("cred-cancel", "chip", "no"),
					),
				),
			)
		})
}

// chrome.go helpSheet and palette.go palette: the two that spelled
// data-action as DataAttr, and that have no body/foot of the af/fs kind.
func TestKitDialogHelpAndPalette(t *testing.T) {
	kitOverlaySame(t, "help",
		func(tr i18n.Runtime) ui.Node {
			return scrim(true, "help-close",
				html.Div(html.Props{Class: "help", Role: "dialog",
					DataAttr: html.DataAttribute{Name: "action", Value: "modal-keep"},
					Aria:     map[string]string{"modal": "true", "label": tr.T("help", "title")}},
					html.Div(html.Props{Class: "help-head"},
						html.H2(html.Props{Class: "help-mark"}, html.Text(tr.T("help", "mark"))),
						html.Span(html.Props{Class: "help-sub"},
							html.Text(tr.T("help", "sub"))),
						legacyActionButtonRole("help-close", roleHelpClose, "btn btn-ghost help-close",
							tr.T("help", "close")),
					),
					html.Div(html.Props{Class: "help-cols"}, html.Text("cols")),
				),
			)
		},
		func(tr i18n.Runtime) ui.Node {
			return kitDialog(kitDialogProps{Open: true, CloseAction: "help-close",
				Class: "help", Title: tr.T("help", "title")},
				kitDialogHead(kitDialogHeadProps{Prefix: "help", Title: tr.T("help", "mark"),
					After: []ui.Node{html.Span(html.Props{Class: "help-sub"}, html.Text(tr.T("help", "sub")))},
					Close: kitDialogClose("help", "help-close", roleHelpClose, tr.T("help", "close"))}),
				html.Div(html.Props{Class: "help-cols"}, html.Text("cols")),
			)
		})

	kitOverlaySame(t, "palette",
		func(tr i18n.Runtime) ui.Node {
			return scrim(true, "palette-close",
				html.Div(html.Props{Class: "pal", Role: "dialog",
					DataAttr: html.DataAttribute{Name: "action", Value: "modal-keep"},
					Aria:     map[string]string{"modal": "true", "label": tr.T("palette", "title")}},
					html.Div(html.Props{Class: "pal-field"}, html.Text("input")),
					html.Div(html.Props{Class: "pal-list", ID: "pal-list"},
						html.Div(html.Props{Class: "pal-empty", Role: "status"}, html.Text("none"))),
					html.Div(html.Props{Class: "pal-foot"},
						html.Text(tr.T("palette", "foot"))),
				),
			)
		},
		func(tr i18n.Runtime) ui.Node {
			return kitDialog(kitDialogProps{Open: true, CloseAction: "palette-close",
				Class: "pal", Title: tr.T("palette", "title")},
				html.Div(html.Props{Class: "pal-field"}, html.Text("input")),
				html.Div(html.Props{Class: "pal-list", ID: "pal-list"},
					kitLive(kitLiveProps{Class: "pal-empty", Role: kitRoleStatus}, html.Text("none"))),
				html.Div(html.Props{Class: "pal-foot"},
					html.Text(tr.T("palette", "foot"))),
			)
		})
}

// --- live regions --------------------------------------------------------------

func TestKitAlertShapes(t *testing.T) {
	for _, class := range []string{"fs-error", "af-error", "af-warn", "set-note set-note-live"} {
		kitOverlaySame(t, "alert/"+class,
			func(i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: class, Role: "alert"}, html.Text("it broke"))
			},
			func(i18n.Runtime) ui.Node { return kitAlert(class, "it broke") })
	}
}

func TestKitLiveShapes(t *testing.T) {
	cases := []struct {
		name   string
		before func() ui.Node
		after  func() ui.Node
	}{
		{"password done (settings.go done/nameDone/passDone)",
			func() ui.Node {
				return html.Div(html.Props{
					Class: "set-note set-note-live", Role: "status",
					Aria: map[string]string{"live": "polite"},
					Data: map[string]string{"good": "true"},
				}, html.Text("done"))
			},
			func() ui.Node {
				return kitLive(kitLiveProps{Class: "set-note set-note-live", Role: kitRoleStatus,
					Live: kitLivePolite, Data: map[string]string{"good": "true"}}, html.Text("done"))
			}},
		{"my feed note (Raw data-bad)",
			func() ui.Node {
				return html.Div(html.Props{
					Class: "set-note set-note-live", Role: "status",
					Raw: map[string]any{"data-bad": strconv.FormatBool(true)},
				}, html.Text("note"))
			},
			func() ui.Node {
				return kitLive(kitLiveProps{Class: "set-note set-note-live", Role: kitRoleStatus,
					Data: map[string]string{"bad": "true"}}, html.Text("note"))
			}},
		{"data import note / sign-out stranded (Data bad)",
			func() ui.Node {
				return html.Div(html.Props{
					Class: "set-note set-note-live",
					Data:  map[string]string{"bad": strconv.FormatBool(false)},
					Role:  "status",
				}, html.Text("note"))
			},
			func() ui.Node {
				return kitLive(kitLiveProps{Class: "set-note set-note-live", Role: kitRoleStatus,
					Data: map[string]string{"bad": "false"}}, html.Text("note"))
			}},
		{"set-note working (theme/smart)",
			func() ui.Node {
				return html.Div(html.Props{Class: "set-note", Role: "status",
					Aria: map[string]string{"live": "polite"}}, html.Text("working"))
			},
			func() ui.Node {
				return kitLive(kitLiveProps{Class: "set-note", Role: kitRoleStatus, Live: kitLivePolite},
					html.Text("working"))
			}},
		{"settings busy banner",
			func() ui.Node { return html.Div(html.Props{Class: "banner", Role: "status"}, html.Text("busy")) },
			func() ui.Node {
				return kitLive(kitLiveProps{Class: "banner", Role: kitRoleStatus}, html.Text("busy"))
			}},
		{"login error slot",
			func() ui.Node {
				return html.Div(html.Props{
					Class: errClass(""),
					Role:  "alert",
				}, html.Text(""))
			},
			func() ui.Node {
				return kitLive(kitLiveProps{Class: errClass(""), Role: kitRoleAlert}, html.Text(""))
			}},
		{"login notice slot (no role)",
			func() ui.Node {
				return html.Div(html.Props{
					Class: noticeClass("ok"),
					Aria:  map[string]string{"live": "polite"},
				}, html.Text("ok"))
			},
			func() ui.Node {
				return kitLive(kitLiveProps{Class: noticeClass("ok"), Live: kitLivePolite}, html.Text("ok"))
			}},
		{"recovered notice (<p>)",
			func() ui.Node {
				return html.P(html.Props{
					Class: noticeClass("last code"),
					Role:  "status",
					Aria:  map[string]string{"live": "polite"},
				}, html.Text("last code"))
			},
			func() ui.Node {
				return kitLive(kitLiveProps{Paragraph: true, Class: noticeClass("last code"),
					Role: kitRoleStatus, Live: kitLivePolite}, html.Text("last code"))
			}},
		{"discover loading",
			func() ui.Node {
				return html.Div(html.Props{Class: "discover-status", Role: "status"},
					html.Div(html.Props{Class: "spin-ring", Aria: map[string]string{"hidden": "true"}}),
					html.Span(html.Props{Class: "spin-label"}, html.Text("loading")),
				)
			},
			func() ui.Node {
				return kitLive(kitLiveProps{Class: "discover-status", Role: kitRoleStatus},
					html.Div(html.Props{Class: "spin-ring", Aria: map[string]string{"hidden": "true"}}),
					html.Span(html.Props{Class: "spin-label"}, html.Text("loading")),
				)
			}},
		{"discover failed",
			func() ui.Node {
				return html.Div(html.Props{Class: "discover-status discover-status-error", Role: "status"},
					html.Text("failed"))
			},
			func() ui.Node {
				return kitLive(kitLiveProps{Class: "discover-status discover-status-error", Role: kitRoleStatus},
					html.Text("failed"))
			}},
	}
	for _, c := range cases {
		kitOverlaySame(t, c.name,
			func(i18n.Runtime) ui.Node { return c.before() },
			func(i18n.Runtime) ui.Node { return c.after() })
	}
}

// --- banners -------------------------------------------------------------------

// list.go listStatusSlot.
func TestKitBannerStatusSlot(t *testing.T) {
	for _, tc := range []struct{ status, busy, undo string }{
		{"", "", ""}, {"Marking…", "Marking…", ""}, {"Marked 40 read", "", "tok"},
	} {
		kitOverlaySame(t, "statusSlot/"+tc.status+"/"+tc.undo,
			func(tr i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "banner-slot",
					Data: map[string]string{"open": strconv.FormatBool(tc.status != "")}},
					html.Div(html.Props{Class: "banner-clip"},
						html.Div(html.Props{Class: "banner", Role: "status",
							Data: map[string]string{"busy": strconv.FormatBool(tc.busy != "")},
							Aria: map[string]string{"live": "polite"}},
							html.Span(html.Props{Class: "banner-text"}, html.Text(tc.status)),
							ui.If(tc.undo != "", func() ui.Node {
								return html.Button(html.Props{
									Class:    "chip chip-mini banner-undo",
									DataAttr: html.DataAttribute{Name: "action", Value: "undo-mark-all"},
								}, html.Text(tr.T("list", "undo")))
							}),
						),
					),
				)
			},
			func(tr i18n.Runtime) ui.Node {
				var undo ui.Node
				if tc.undo != "" {
					undo = kitBannerChip(kitBannerChipProps{Action: "undo-mark-all", Label: tr.T("list", "undo")})
				}
				return kitBannerSlot(tc.status != "",
					kitBanner(kitBannerProps{Text: tc.status, Busy: tc.busy != ""}, undo))
			})
	}
}

// list.go listCategorySuggestSlot.
func TestKitBannerCategorySuggest(t *testing.T) {
	for _, busy := range []bool{false, true} {
		kitOverlaySame(t, "categorySuggest/"+strconv.FormatBool(busy),
			func(tr i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "banner-slot",
					Data: map[string]string{"open": strconv.FormatBool(true)}},
					html.Div(html.Props{Class: "banner-clip"},
						html.Div(html.Props{Class: "banner", Role: "status",
							Data: map[string]string{"busy": strconv.FormatBool(busy)},
							Aria: map[string]string{"live": "polite"}},
							html.Span(html.Props{Class: "banner-text"},
								html.Text(tr.T("list", "categorySuggest", i18n.Args{"category": "Science"}))),
							html.Button(html.Props{
								Class:    "chip chip-mini banner-undo",
								DataAttr: html.DataAttribute{Name: "action", Value: "accept-category-suggestion"},
							}, html.Text(tr.T("list", "categorySuggestAccept"))),
							html.Button(html.Props{
								Class:    "chip chip-mini banner-undo",
								DataAttr: html.DataAttribute{Name: "action", Value: "dismiss-category-suggestion"},
							}, html.Text(tr.T("list", "categorySuggestDismiss"))),
						),
					),
				)
			},
			func(tr i18n.Runtime) ui.Node {
				return kitBannerSlot(true,
					kitBanner(kitBannerProps{Busy: busy,
						Text: tr.T("list", "categorySuggest", i18n.Args{"category": "Science"})},
						kitBannerChip(kitBannerChipProps{Action: "accept-category-suggestion",
							Label: tr.T("list", "categorySuggestAccept")}),
						kitBannerChip(kitBannerChipProps{Action: "dismiss-category-suggestion",
							Label: tr.T("list", "categorySuggestDismiss")}),
					))
			})
	}
}

// list.go listMarkAllSlot + panes.go markAllConfirm.
func TestKitBannerMarkAllConfirm(t *testing.T) {
	confirmBanner := func(tr i18n.Runtime) ui.Node {
		return kitBanner(kitBannerProps{Confirm: true,
			Text:      tr.T("list", "markAllConfirm", i18n.Args{"scope": "Everything"}),
			Title:     tr.T("list", "markAllConfirmAria"),
			TextID:    "mark-all-confirm-text",
			FocusRole: "mark-all-confirm"},
			kitBannerChip(kitBannerChipProps{Action: "mark-all", Danger: true,
				Label: tr.T("list", "markAllConfirmGo")}),
			kitBannerChip(kitBannerChipProps{Action: "mark-all-cancel", FocusRole: "mark-all-cancel",
				Label: tr.T("list", "markAllConfirmCancel")}),
		)
	}
	for _, armed := range []bool{false, true} {
		kitOverlaySame(t, "markAllSlot/"+strconv.FormatBool(armed),
			func(tr i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "banner-slot",
					Data: map[string]string{"open": strconv.FormatBool(armed)}},
					html.Div(html.Props{Class: "banner-clip"},
						ui.If(armed, func() ui.Node {
							return html.Div(html.Props{Class: "banner", Role: "alertdialog",
								Data: map[string]string{"role": "mark-all-confirm"},
								Aria: map[string]string{"label": tr.T("list", "markAllConfirmAria"),
									"describedby": "mark-all-confirm-text"}},
								html.Span(html.Props{Class: "banner-text", ID: "mark-all-confirm-text"},
									html.Text(tr.T("list", "markAllConfirm", i18n.Args{"scope": "Everything"}))),
								html.Button(html.Props{
									Class:     "chip chip-mini banner-undo fs-danger",
									DataAttrs: []html.DataAttribute{{Name: "action", Value: "mark-all"}, {Name: "armed", Value: "true"}},
								}, html.Text(tr.T("list", "markAllConfirmGo"))),
								html.Button(html.Props{
									Class:    "chip chip-mini banner-undo",
									DataAttr: html.DataAttribute{Name: "action", Value: "mark-all-cancel"},
									Data:     map[string]string{"role": "mark-all-cancel"},
								}, html.Text(tr.T("list", "markAllConfirmCancel"))),
							)
						}),
					),
				)
			},
			func(tr i18n.Runtime) ui.Node {
				return kitBannerSlot(armed, ui.If(armed, func() ui.Node { return confirmBanner(tr) }))
			})
	}
}

// --- arm, then confirm ---------------------------------------------------------

// settings.go signOutGroup's button and warning (the group heading and scope
// note around them are not this family's).
func TestKitArmSignOut(t *testing.T) {
	const glyph = "⏻"
	for _, tc := range []struct{ armed, busy bool }{{false, false}, {true, false}, {true, true}, {false, true}} {
		kitOverlaySame(t, "signOut/"+strconv.FormatBool(tc.armed)+"/"+strconv.FormatBool(tc.busy),
			func(tr i18n.Runtime) ui.Node {
				label, action := tr.T("settings", "signOut"), "sign-out"
				switch {
				case tc.busy:
					label, action = tr.T("settings", "signOutBusy"), "sign-out-confirm"
				case tc.armed:
					label, action = tr.T("settings", "signOutArmed"), "sign-out-confirm"
				}
				class := "chip"
				if tc.armed || tc.busy {
					class = "chip fs-danger"
				}
				return kitOverlayWrap(
					html.Div(html.Props{Class: "set-actions"},
						html.Button(html.Props{
							Class: class,
							Raw: map[string]any{
								"data-action": action,
								"data-armed":  strconv.FormatBool(tc.armed && !tc.busy),
							},
							Aria: map[string]string{"busy": strconv.FormatBool(tc.busy)},
						}, lead(glyph), html.Text(label)),
					),
					ui.If(tc.armed && !tc.busy, func() ui.Node {
						return html.Div(html.Props{
							Class: "set-note set-note-live",
							Role:  "alert",
						}, html.Text(tr.T("settings", "signOutWarn")))
					}),
				)
			},
			func(tr i18n.Runtime) ui.Node {
				arm := kitArm(kitArmProps{Armed: tc.armed, Busy: tc.busy,
					Action: "sign-out", ArmedAction: "sign-out-confirm",
					Label: tr.T("settings", "signOut"), ArmedLabel: tr.T("settings", "signOutArmed"),
					BusyLabel: tr.T("settings", "signOutBusy"),
					Glyph:     glyph,
					DataArmed: true, AriaBusy: true,
					Warn: tr.T("settings", "signOutWarn"), WarnClass: "set-note set-note-live"})
				return kitOverlayWrap(html.Div(html.Props{Class: "set-actions"}, arm.Button), arm.Warn)
			})
	}
}

// myfeedsettings.go mfDeleteControl and mfResetControl.
func TestKitArmMyFeed(t *testing.T) {
	for _, armed := range []bool{false, true} {
		kitOverlaySame(t, "mfDelete/"+strconv.FormatBool(armed),
			func(tr i18n.Runtime) ui.Node {
				label, action := tr.T("myFeed", "rowDelete"), "my-feed-delete-arm"
				labelAria := tr.T("myFeed", "rowDeleteAria", i18n.Args{"name": "rust"})
				class := "chip chip-mini"
				if armed {
					label, action = tr.T("myFeed", "rowDeleteConfirm"), "my-feed-delete-confirm"
					labelAria = tr.T("myFeed", "rowDeleteConfirmAria", i18n.Args{"name": "rust"})
					class = "chip chip-mini fs-danger"
				}
				out := []ui.Node{html.Button(html.Props{
					Class: class,
					Raw: map[string]any{
						"data-action": action, "data-for-item": "rust", "data-value": "topic",
						"data-armed": strconv.FormatBool(armed),
					},
					Aria: map[string]string{"label": labelAria},
				}, html.Text(label))}
				if armed {
					out = append(out, html.Button(html.Props{
						Class: "chip chip-mini",
						Raw:   map[string]any{"data-action": "my-feed-delete-cancel"},
						Aria:  map[string]string{"label": tr.T("myFeed", "rowDeleteCancelAria")},
					}, html.Text(tr.T("myFeed", "rowDeleteCancel"))))
				}
				return kitOverlayWrap(out...)
			},
			func(tr i18n.Runtime) ui.Node {
				return kitOverlayWrap(kitArm(kitArmProps{Armed: armed,
					Action: "my-feed-delete-arm", ArmedAction: "my-feed-delete-confirm",
					Label: tr.T("myFeed", "rowDelete"), ArmedLabel: tr.T("myFeed", "rowDeleteConfirm"),
					AriaLabel:      tr.T("myFeed", "rowDeleteAria", i18n.Args{"name": "rust"}),
					ArmedAriaLabel: tr.T("myFeed", "rowDeleteConfirmAria", i18n.Args{"name": "rust"}),
					Mini:           true,
					Item:           "rust", Value: "topic", DataArmed: true,
					CancelAction: "my-feed-delete-cancel", CancelLabel: tr.T("myFeed", "rowDeleteCancel"),
					CancelAriaLabel: tr.T("myFeed", "rowDeleteCancelAria")}).Nodes()...)
			})

		kitOverlaySame(t, "mfReset/"+strconv.FormatBool(armed),
			func(tr i18n.Runtime) ui.Node {
				label, action := tr.T("myFeed", "catReset"), "my-feed-reset-arm"
				labelAria := tr.T("myFeed", "catResetAria")
				class := "chip chip-mini"
				if armed {
					label, action = tr.T("myFeed", "catResetConfirm"), "my-feed-reset-confirm"
					labelAria = tr.T("myFeed", "catResetConfirmAria")
					class = "chip chip-mini fs-danger"
				}
				kids := []ui.Node{html.Button(html.Props{
					Class: class,
					Raw: map[string]any{
						"data-action": action, "data-value": "topics", "data-armed": strconv.FormatBool(armed),
					},
					Aria: map[string]string{"label": labelAria},
				}, html.Text(label))}
				if armed {
					kids = append(kids,
						html.Button(html.Props{
							Class: "chip chip-mini",
							Raw:   map[string]any{"data-action": "my-feed-reset-cancel"},
							Aria:  map[string]string{"label": tr.T("myFeed", "catResetCancelAria")},
						}, html.Text(tr.T("myFeed", "rowDeleteCancel"))),
						html.Div(html.Props{Class: "set-note set-note-live", Role: "alert"},
							html.Text(tr.T("myFeed", "catResetWarn"))),
					)
				}
				return html.Div(html.Props{Class: "set-actions mf-reset"}, kids...)
			},
			func(tr i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "set-actions mf-reset"}, kitArm(kitArmProps{Armed: armed,
					Action: "my-feed-reset-arm", ArmedAction: "my-feed-reset-confirm",
					Label: tr.T("myFeed", "catReset"), ArmedLabel: tr.T("myFeed", "catResetConfirm"),
					AriaLabel: tr.T("myFeed", "catResetAria"), ArmedAriaLabel: tr.T("myFeed", "catResetConfirmAria"),
					Mini:  true,
					Value: "topics", DataArmed: true,
					CancelAction: "my-feed-reset-cancel", CancelLabel: tr.T("myFeed", "rowDeleteCancel"),
					CancelAriaLabel: tr.T("myFeed", "catResetCancelAria"),
					Warn:            tr.T("myFeed", "catResetWarn"), WarnClass: "set-note set-note-live"}).Nodes()...)
			})
	}
}

// --- invariants ----------------------------------------------------------------

// A dialog is always a named modal dialog: a screen reader opening one with no
// name hears "dialog" and nothing about which.
func TestKitDialogAlwaysNamed(t *testing.T) {
	for _, p := range []kitDialogProps{
		{Open: true, CloseAction: "x-close", Class: "af", Title: "Add a feed"},
		{Open: false, CloseAction: "x-close", Class: "fs", Title: "Feed settings", DescribedBy: "lead"},
	} {
		out := renderView(t, func(i18n.Runtime) ui.Node { return kitDialog(p) })
		tag := elementTag(t, out, `role="dialog"`)
		for _, want := range []string{`aria-label="` + p.Title + `"`, `aria-modal="true"`, `data-action="modal-keep"`} {
			if !strings.Contains(tag, want) {
				t.Errorf("dialog %q: tag lacks %s: %s", p.Class, want, tag)
			}
		}
		if p.DescribedBy != "" && !strings.Contains(tag, `aria-describedby="`+p.DescribedBy+`"`) {
			t.Errorf("dialog %q: describedby not wired: %s", p.Class, tag)
		}
		if p.DescribedBy == "" && strings.Contains(tag, "aria-describedby") {
			t.Errorf("dialog %q: describedby pointing at nothing: %s", p.Class, tag)
		}
		scrimTag := elementTag(t, out, `class="pal-scrim"`)
		if !strings.Contains(scrimTag, `data-open="`+strconv.FormatBool(p.Open)+`"`) ||
			!strings.Contains(scrimTag, `data-action="x-close"`) {
			t.Errorf("scrim does not carry open state and close action: %s", scrimTag)
		}
	}
}

// A close button's name is its visible label, so it can never be an
// unlabelled glyph.
func TestKitDialogCloseNamed(t *testing.T) {
	for _, role := range []string{"", "focus-me"} {
		out := renderView(t, func(i18n.Runtime) ui.Node { return kitDialogClose("fs", "fs-x", role, "Close") })
		block := buttonBlock(t, out, `data-action="fs-x"`)
		if !strings.Contains(block, ">Close</button>") || !strings.Contains(block, "btn btn-ghost fs-close") {
			t.Errorf("close button lacks its visible name or class: %s", block)
		}
		if role != "" && !strings.Contains(block, `data-role="focus-me"`) {
			t.Errorf("close button lacks its focus handle: %s", block)
		}
	}
}

// An armed confirm says what the next press does, offers the safe answer, and
// leaves nothing destructive reachable once it is answered or disarmed.
func TestKitArmConsequenceAndSafeDefault(t *testing.T) {
	base := kitArmProps{Action: "x-arm", ArmedAction: "x-do", Label: "Remove", ArmedLabel: "Really remove?",
		BusyLabel: "Removing…", DataArmed: true, AriaBusy: true,
		CancelAction: "x-cancel", CancelLabel: "Keep", Warn: "This removes it everywhere.", WarnClass: "set-note"}

	rest := kitArm(base)
	if rest.Cancel != nil || rest.Warn != nil {
		t.Errorf("a resting control rendered its cancel or warning")
	}

	armed := base
	armed.Armed = true
	parts := kitArm(armed)
	if parts.Warn == nil || parts.Cancel == nil {
		t.Fatalf("an armed control is missing its warning or its safe answer")
	}
	out := renderView(t, func(i18n.Runtime) ui.Node { return kitOverlayWrap(parts.Nodes()...) })
	if w := elementTag(t, out, `role="alert"`); !strings.Contains(out, "This removes it everywhere.") || w == "" {
		t.Errorf("armed warning is not an alert carrying the consequence: %s", out)
	}
	if b := elementTag(t, out, `data-action="x-do"`); !strings.Contains(b, `data-armed="true"`) || !strings.Contains(b, "fs-danger") {
		t.Errorf("armed button does not report itself armed: %s", b)
	}
	if !strings.Contains(out, `data-action="x-cancel"`) {
		t.Errorf("armed control offers no cancel: %s", out)
	}

	busy := armed
	busy.Busy = true
	bp := kitArm(busy)
	if bp.Warn != nil || bp.Cancel != nil {
		t.Errorf("a confirmed press in flight still asks its question")
	}
	bout := renderView(t, func(i18n.Runtime) ui.Node { return bp.Button })
	if !strings.Contains(bout, `aria-busy="true"`) || !strings.Contains(bout, `data-armed="false"`) {
		t.Errorf("busy button does not report busy / disarmed: %s", bout)
	}
}

// A confirm banner is described by its own sentence and puts its focus handle
// on the answer that does nothing.
func TestKitConfirmBannerWiring(t *testing.T) {
	out := renderView(t, func(i18n.Runtime) ui.Node {
		return kitBanner(kitBannerProps{Confirm: true, Text: "Mark all of Science read?", Title: "Confirm",
			TextID: "q-text", FocusRole: "q"},
			kitBannerChip(kitBannerChipProps{Action: "go", Label: "Mark read", Danger: true}),
			kitBannerChip(kitBannerChipProps{Action: "no", Label: "Cancel", FocusRole: "q-cancel"}))
	})
	box := elementTag(t, out, `role="alertdialog"`)
	if !strings.Contains(box, `aria-describedby="q-text"`) || !strings.Contains(box, `aria-label="Confirm"`) {
		t.Errorf("confirm banner is not named and described: %s", box)
	}
	if !strings.Contains(elementTag(t, out, `id="q-text"`), "banner-text") {
		t.Errorf("describedby does not point at the banner's sentence: %s", out)
	}
	if c := elementTag(t, out, `data-action="no"`); !strings.Contains(c, `data-role="q-cancel"`) || strings.Contains(c, "fs-danger") {
		t.Errorf("cancel is not the safe, focusable default: %s", c)
	}
}
