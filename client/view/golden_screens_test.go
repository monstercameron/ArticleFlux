//go:build js && wasm

package view

// Golden snapshots of the screens golden_test.go does not cover — the
// dialogs, every Settings tab, the sign-in and setup cards, Discover and the
// slideshow's chrome — added before the kit adoption so every file the
// adoption touches has a byte-exact net under it. Same contract as
// golden_test.go: a failure is "name the difference and justify it", never
// "run -update". Uses that file's renderView, firstDifference and -update flag.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

func screenGoldenCases() []goldenCase {
	nodes := func(build func(tr i18n.Runtime) []ui.Node) func(tr i18n.Runtime) ui.Node {
		return func(tr i18n.Runtime) ui.Node { return html.Div(html.Props{Class: "golden-probe"}, build(tr)...) }
	}
	stats := &pb.GetServerStatsResponse{Version: "1.0", Methods: []*pb.MethodStat{
		{Method: "GetFeeds", Calls: 10, P50Ms: 3, P95Ms: 12, MaxMs: 40, Errors: 2},
	}}
	cfg := &pb.GetSmartConfigResponse{Configured: true, KeyHint: "4f2a", DefaultModel: "gpt-x"}
	feed := &pb.FeedSettings{
		SourceId: "s1", ResolvedTitle: "Example", FeedUrl: "https://example.com/feed",
		SiteUrl: "https://example.com", SubscriberCount: 3, ConsecutiveFailures: 2,
	}
	recs := []*pb.Recommendation{
		{Domain: "a.example", Title: "A", Evidence: "seen twice"},
		{Domain: "b.example", Evidence: "linked"},
		{Domain: "c.example", Title: "C"},
	}
	return []goldenCase{
		// --- dialogs (addfeed.go, feedsettings.go, tagsettings.go, settings.go) ---
		{"dlg-addfeed-open", func(tr i18n.Runtime) ui.Node {
			return addFeedDialog(tr, addFeedProps{open: true, newOpen: true,
				folders: []*pb.Folder{{Id: "f1", Name: "News"}}})
		}},
		{"dlg-addfeed-closed", func(tr i18n.Runtime) ui.Node { return addFeedDialog(tr, addFeedProps{}) }},
		{"dlg-category-rest", func(tr i18n.Runtime) ui.Node {
			return categoryDialog(tr, categoryProps{open: true, id: "f1", name: "News", feeds: 2})
		}},
		{"dlg-category-armed", func(tr i18n.Runtime) ui.Node {
			return categoryDialog(tr, categoryProps{open: true, id: "f1", name: "News", feeds: 2, confirm: true})
		}},
		{"dlg-feedsettings", func(tr i18n.Runtime) ui.Node {
			return feedSettings(tr, feedSettingsProps{open: true, s: feed, tags: []string{"rust"}})
		}},
		{"dlg-feedsettings-loading", func(tr i18n.Runtime) ui.Node {
			return feedSettings(tr, feedSettingsProps{open: true})
		}},
		{"dlg-tagsettings", func(tr i18n.Runtime) ui.Node {
			return tagSettings(tr, tagSettingsProps{open: true, t: &pb.Tag{Id: "t1", Name: "rust"},
				feeds: []string{"Example"}})
		}},
		{"dlg-tagsettings-closed", func(tr i18n.Runtime) ui.Node {
			return tagSettings(tr, tagSettingsProps{open: false, t: nil})
		}},
		{"dlg-credconfirm", func(tr i18n.Runtime) ui.Node { return credConfirmDialog(tr, pendingPassword, "", "cam") }},

		// --- settings tabs (settings.go, theme.go, datasettings.go, myfeedsettings.go,
		// smartsettings.go, classifysettings.go, podcastsettings.go) ---
		{"set-pane", func(tr i18n.Runtime) ui.Node { return settingsPane(tr, settingsProps{}) }},
		{"set-reading", nodes(func(tr i18n.Runtime) []ui.Node { return settingsReading(tr, settingsProps{}) })},
		{"set-listening", nodes(func(tr i18n.Runtime) []ui.Node {
			return settingsListening(tr, settingsProps{speakPodcast: true})
		})},
		{"set-feeds", nodes(func(tr i18n.Runtime) []ui.Node { return settingsFeeds(tr, settingsProps{}) })},
		{"set-account", nodes(func(tr i18n.Runtime) []ui.Node {
			return settingsAccount(tr, settingsProps{whoami: "cam", reconnects: 1,
				password: passwordProps{username: "cam"}})
		})},
		{"set-server", nodes(func(tr i18n.Runtime) []ui.Node { return settingsServer(tr, settingsProps{stats: stats}) })},
		{"set-speed", nodes(func(tr i18n.Runtime) []ui.Node { return settingsSpeed(tr, settingsProps{stats: stats}) })},
		{"set-activity", nodes(func(tr i18n.Runtime) []ui.Node { return settingsActivity(tr, settingsProps{tab: setActivity}) })},
		{"set-appearance", nodes(func(tr i18n.Runtime) []ui.Node { return settingsAppearance(tr, settingsProps{}) })},
		{"set-data", nodes(func(tr i18n.Runtime) []ui.Node { return settingsData(tr, dataProps{}) })},
		{"set-myfeed", nodes(func(tr i18n.Runtime) []ui.Node {
			return settingsMyFeed(tr, myFeedProps{profile: demoProfile()})
		})},
		{"set-myfeed-loading", nodes(func(tr i18n.Runtime) []ui.Node {
			return settingsMyFeed(tr, myFeedProps{loading: true})
		})},
		{"set-smart", nodes(func(tr i18n.Runtime) []ui.Node {
			return settingsSmart(tr, smartProps{cfg: cfg, models: []string{"gpt-x"}})
		})},
		{"set-smart-typed", nodes(func(tr i18n.Runtime) []ui.Node { return settingsSmart(tr, smartProps{cfg: cfg}) })},
		{"set-smart-empty", nodes(func(tr i18n.Runtime) []ui.Node { return settingsSmart(tr, smartProps{}) })},
		{"set-classify", nodes(func(tr i18n.Runtime) []ui.Node {
			return settingsClassify(tr, classifySettingsProps{})
		})},
		{"set-podcast", nodes(func(tr i18n.Runtime) []ui.Node {
			return settingsPodcast(tr, podcastProps{podcast: true, needs: []slidePrereq{
				{Key: prereqSmartVoice, Fixable: true}, {Key: prereqServerKey, On: true},
			}})
		})},

		// --- sign-in and setup (login.go, setup.go) ---
		{"login-recover", func(tr i18n.Runtime) ui.Node { return recoverCard(recoverCardProps{tr: tr}) }},
		{"login-recovered", func(tr i18n.Runtime) ui.Node {
			return recoverCard(recoverCardProps{tr: tr, recovered: true})
		}},
		// Login() and Setup() themselves cannot be rendered here: they register
		// ui.UseEvent handlers, which panic under SSR ("GoUseFunc dom adapter is
		// nil"). Their bodies are therefore OUT of the adoption's scope; only the
		// plain helpers below are covered and may be converted.
		{"setup-codes", func(tr i18n.Runtime) ui.Node {
			return setupCodes(tr, []string{"aaaa-bbbb-cccc", "dddd-eeee-ffff"}, "", ui.Handler{}, ui.Handler{})
		}},

		// --- the armed/confirm states the UX pass added (ux_test.go's fixtures) ---
		{"arm-smartkey-rest", nodes(func(tr i18n.Runtime) []ui.Node {
			return smartKeySection(tr, smartProps{cfg: &pb.GetSmartConfigResponse{Configured: true, CanStoreSecrets: true}})
		})},
		{"arm-smartkey-armed", nodes(func(tr i18n.Runtime) []ui.Node {
			return smartKeySection(tr, smartProps{armed: armKeyClear,
				cfg: &pb.GetSmartConfigResponse{Configured: true, CanStoreSecrets: true}})
		})},
		{"arm-passphrase-rest", nodes(func(tr i18n.Runtime) []ui.Node {
			return passwordGroup(tr, passwordProps{passConfigured: true})
		})},
		{"arm-passphrase-armed", nodes(func(tr i18n.Runtime) []ui.Node {
			return passwordGroup(tr, passwordProps{armed: armPassClear, passConfigured: true})
		})},
		{"arm-unsubscribe-rest", nodes(func(tr i18n.Runtime) []ui.Node {
			return feedSettingsBody(tr, feedSettingsProps{s: &pb.FeedSettings{SourceId: "src-1", ResolvedTitle: "Feed one"}})
		})},
		{"arm-unsubscribe-armed", nodes(func(tr i18n.Runtime) []ui.Node {
			return feedSettingsBody(tr, feedSettingsProps{armed: armUnsubscribe + "src-1",
				s: &pb.FeedSettings{SourceId: "src-1", ResolvedTitle: "Feed one"}})
		})},
		{"arm-signout-armed", nodes(func(tr i18n.Runtime) []ui.Node {
			return signOutGroup(tr, sessionProps{armed: true})
		})},
		{"arm-signout-busy", nodes(func(tr i18n.Runtime) []ui.Node {
			return signOutGroup(tr, sessionProps{armed: true, busy: true})
		})},
		{"arm-signout-stranded", nodes(func(tr i18n.Runtime) []ui.Node {
			return signOutGroup(tr, sessionProps{stranded: true})
		})},
		{"set-myfeed-armed", nodes(func(tr i18n.Runtime) []ui.Node {
			return settingsMyFeed(tr, myFeedProps{profile: demoProfile(), confirmDelete: myFeedRowTopic + ":maxpro90s", confirmReset: myFeedCatTopics})
		})},
		{"list-first-run", func(tr i18n.Runtime) ui.Node {
			return listPane(tr, listProps{connected: true, feedsKnown: true})
		}},
		{"list-feeds-waiting", func(tr i18n.Runtime) ui.Node {
			return listPane(tr, listProps{connected: true, feedsKnown: true, feeds: 3})
		}},

		// --- the translate control through its states (translate_test.go) ---
		{"article-translate-idle", func(tr i18n.Runtime) ui.Node {
			return articlePane(tr, translatedFixture(articleTranslation{}, true))
		}},
		{"article-translate-busy", func(tr i18n.Runtime) ui.Node {
			return articlePane(tr, translatedFixture(articleTranslation{state: trBusy}, true))
		}},
		{"article-translate-done", func(tr i18n.Runtime) ui.Node {
			return articlePane(tr, translatedFixture(articleTranslation{state: trDone, source: "de",
				title: "Le titre", html: "<p>Le texte traduit.</p>"}, true))
		}},
		{"article-translate-partial", func(tr i18n.Runtime) ui.Node {
			return articlePane(tr, translatedFixture(articleTranslation{state: trDone, source: "de",
				title: "Le titre", html: "<p>Le texte traduit.</p>", partial: true}, true))
		}},
		{"set-smart-features-owner", nodes(func(tr i18n.Runtime) []ui.Node {
			return smartFeatureSection(tr, smartProps{owner: true,
				cfg: &pb.GetSmartConfigResponse{Configured: true, TranslateArticlesEnabled: true}})
		})},
		{"set-smart-languages", nodes(func(tr i18n.Runtime) []ui.Node {
			return smartLanguageSection(tr, smartProps{languages: languagesFixture()})
		})},

		// --- discover.go ---
		{"discover-list", func(tr i18n.Runtime) ui.Node {
			return discoverBody(tr, false, false, recs, "b.example", "c.example", "kept")
		}},
		{"discover-empty", func(tr i18n.Runtime) ui.Node { return discoverBody(tr, false, false, nil, "", "", "") }},
		{"discover-loading", func(tr i18n.Runtime) ui.Node { return discoverBody(tr, true, false, nil, "", "", "") }},
		{"discover-failed", func(tr i18n.Runtime) ui.Node { return discoverBody(tr, false, true, nil, "", "", "") }},
		{"discover-prefs-loading", func(tr i18n.Runtime) ui.Node {
			return discoverGatedBody(tr, false, false, false, false, nil, "", "", "")
		}},
		{"discover-gate", func(tr i18n.Runtime) ui.Node {
			return discoverGatedBody(tr, true, false, false, false, nil, "", "", "")
		}},
		{"discover-toggle", func(tr i18n.Runtime) ui.Node { return discoverSmartPlusToggle(tr, true, false) }},

		// --- slideshow.go, palette.go ---
		{"slide-hud", func(tr i18n.Runtime) ui.Node { return slideHud(tr, slideProps{paused: true, audio: true}) }},
		{"slide-body", func(tr i18n.Runtime) ui.Node {
			its := fixtureItems(2)
			return slideBody(tr, slideProps{open: true, it: its[0], body: its[0]})
		}},
		{"palette-empty", func(tr i18n.Runtime) ui.Node {
			return palette(tr, paletteProps{open: true, query: "zzz"})
		}},
	}
}

// TestGoldenScreens is TestGolden for the screens above.
func TestGoldenScreens(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range allGoldenCases() {
		seen[c.name] = true
	}
	for _, c := range screenGoldenCases() {
		if seen[c.name] {
			t.Fatalf("duplicate golden name %q", c.name)
		}
		seen[c.name] = true
		t.Run(c.name, func(t *testing.T) {
			got := renderView(t, c.build)
			if len(got) < 40 {
				t.Fatalf("%s rendered %d bytes; a golden of nothing proves nothing", c.name, len(got))
			}
			path := filepath.Join("testdata", "golden", c.name+".html")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("no golden for %s (run with -update to create it): %v", c.name, err)
			}
			if got != string(want) {
				t.Errorf("%s changed:\n%s", c.name, firstDifference(string(want), got))
			}
		})
	}
}
