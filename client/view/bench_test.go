//go:build js && wasm

package view

// Render benchmarks for the three panes, so a performance claim about them is a
// number rather than an impression.
//
// Run with -benchmem and read allocs/op and B/op. ns/op is reported too, but
// this suite runs under Node on whatever machine it is given, and on a shared
// one the clock measures the neighbours as much as the code.
//
//	GOOS=js GOARCH=wasm go test -exec=... ./client/view -run '^$' -bench . -benchmem
//
// Two shapes of each:
//
//   - Build: the tree construction alone — what Reader pays on every render for
//     a pane that is not a memoised component, which for the list is every
//     scroll frame. This is the number the allocation work moves.
//   - SSR: construction plus serialisation, through the same path the golden
//     snapshots use. Slower and noisier, but it is the only way to render a
//     component (railPane calls a hook, so it cannot be built outside a render).

import (
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/testkit/render"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// benchRuntime is mustRuntime for a benchmark: a real Runtime on the English
// catalog, captured once.
func benchRuntime(b *testing.B) i18n.Runtime {
	b.Helper()
	var tr i18n.Runtime
	if _, err := ui.RenderToString(i18n.Provider(i18n.ProviderProps{
		CurrentLocale:  i18n.DefaultLocale,
		FallbackLocale: i18n.DefaultLocale,
		Bundle:         i18n.Bundle(),
		Child: ui.CreateElement(func() ui.Node {
			tr = i18n.UseI18n()
			return html.Text("")
		}),
	})); err != nil {
		b.Fatal(err)
	}
	return tr
}

func benchSSR(b *testing.B, build func(tr i18n.Runtime) ui.Node) {
	b.Helper()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ui.RenderToString(i18n.Provider(i18n.ProviderProps{
			CurrentLocale:  i18n.DefaultLocale,
			FallbackLocale: i18n.DefaultLocale,
			Bundle:         i18n.Bundle(),
			Child: ui.CreateElement(func() ui.Node {
				return build(i18n.UseI18n())
			}),
		})); err != nil {
			b.Fatal(err)
		}
	}
}

// benchList is a populated list of n rows as Reader renders it mid-scroll: a
// 900px viewport a third of the way down, with a row selected.
func benchList(n int) listProps {
	items := fixtureItems(n)
	return listProps{
		items: items, connected: true, total: n, unread: n / 2, hasMore: true,
		sel:       scope{Title: "All feeds"},
		current:   items[n/3],
		iconHosts: iconHostsOf(fixtureFeeds(150)),
		conn:      data.Live,
		viewport:  900,
		scrollTop: float64(n/3) * ItemRowHeight,
	}
}

func benchListBuild(b *testing.B, n int) {
	tr := benchRuntime(b)
	p := benchList(n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = listPane(tr, p)
	}
}

func BenchmarkListPaneBuild50(b *testing.B)  { benchListBuild(b, 50) }
func BenchmarkListPaneBuild500(b *testing.B) { benchListBuild(b, 500) }

func BenchmarkListPaneSSR50(b *testing.B) {
	p := benchList(50)
	benchSSR(b, func(tr i18n.Runtime) ui.Node { return listPane(tr, p) })
}

func BenchmarkListPaneSSR500(b *testing.B) {
	p := benchList(500)
	benchSSR(b, func(tr i18n.Runtime) ui.Node { return listPane(tr, p) })
}

// BenchmarkItemRow is one row, the unit the list's cost is a multiple of.
func BenchmarkItemRow(b *testing.B) {
	tr := benchRuntime(b)
	items := fixtureItems(10)
	hosts := iconHostsOf(fixtureFeeds(150))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = itemRow(tr, items[i%len(items)], i%10 == 0, hosts, -1, "")
	}
}

// BenchmarkRailSSR150 is the sidebar at the size this instance actually runs at:
// 150 feeds across three categories, two tags, a category open.
func BenchmarkRailSSR150(b *testing.B) {
	p := railProps{
		feeds: fixtureFeeds(150), tags: fixtureTags(), folders: fixtureFolders(),
		total: 400, ranked: 30, openCats: "cat-1",
		sel: scope{SourceID: "src-40", Title: "Feed number 40"},
	}
	benchSSR(b, func(tr i18n.Runtime) ui.Node { return railPane(p) })
}

// The article pane with a long body (300 paragraphs, about 6,600 words) and a
// second item still loading behind it.
func BenchmarkArticlePaneBuild(b *testing.B) {
	tr := benchRuntime(b)
	p := fixtureArticle(300)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = articlePane(tr, p)
	}
}

func BenchmarkArticlePaneSSR(b *testing.B) {
	p := fixtureArticle(300)
	benchSSR(b, func(tr i18n.Runtime) ui.Node { return articlePane(tr, p) })
}

// BenchmarkChromeBuild is what the shell's always-present chrome costs per
// Reader render while nothing in it has changed: the shortcut sheet (closed),
// the tab bar and the palette (closed). Reader re-renders on every scroll frame
// of the list, so this is paid at frame rate unless it bails out.
func BenchmarkChromeBuild(b *testing.B) {
	tr := benchRuntime(b)
	for _, c := range []struct {
		name  string
		build func() ui.Node
	}{
		{"help", func() ui.Node { return helpSheet(tr, false) }},
		{"tabbar", func() ui.Node { return tabBar(tr, viewList, scope{Title: "All feeds"}) }},
		{"palette", func() ui.Node { return palette(tr, paletteProps{}) }},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = c.build()
			}
		})
	}
}

// BenchmarkRailRerenderOneUnreadChange is the rail's commonest re-render,
// measured through GWC's own reconciler on its mock DOM: one article is read,
// so one feed's unread count drops — a new slice holding one cloned feed, the
// way adjustUnread writes it — and the rail renders again. Every other feed is
// the same pointer it was.
func BenchmarkRailRerenderOneUnreadChange(b *testing.B) {
	feeds0 := fixtureFeeds(150)
	var setFeeds func([]*pb.Feed)
	parent := func() ui.Node {
		feeds := ui.UseState(feeds0)
		setFeeds = feeds.Set
		return ui.CreateElement(railPane, railProps{
			feeds: feeds.Get(), tags: fixtureTags(), folders: fixtureFolders(),
			total: 400, openCats: "cat-1",
		})
	}
	f := render.New(b)
	f.Render(i18n.Provider(i18n.ProviderProps{
		CurrentLocale:  i18n.DefaultLocale,
		FallbackLocale: i18n.DefaultLocale,
		Bundle:         i18n.Bundle(),
		Child:          ui.CreateElement(parent),
	}))
	cur := feeds0
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		next := append([]*pb.Feed(nil), cur...)
		j := i % len(next)
		c := cloneFeed(next[j])
		c.UnreadCount = int32(i % 5)
		next[j] = c
		cur = next
		ui.PostAsync(func() { setFeeds(next) })
		f.Flush()
	}
}
