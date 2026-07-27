//go:build js && wasm

// Command demo is ArticleFlux with the server inside it.
//
// It is the same client as client/app — the same reader, the same styles, the
// same state, the same generated gRPC stubs — with the tunnel replaced by an
// in-memory instance (client/demodata). Nothing is fetched, nothing is stored,
// and there is nothing to log into.
//
// It exists because this is a self-hosted application, and "try it" otherwise
// means installing a Go toolchain, cloning two repositories, building a wasm
// bundle and running a server. A static page removes every one of those steps
// for somebody who has not yet decided whether they care.
//
// Built by .github/workflows/pages.yml and served from GitHub Pages. Locally:
//
//	scripts/make.ps1 demo     (or: make demo)
//
// The corresponding NON-goal is worth stating, because it is the thing that
// would make this file grow: the demo is not a second product. When the reader
// needs a feature the demo cannot provide — Smart+ needs an OpenAI key, the page
// proxy needs a server — the demo says so through the same API the real server
// would use to say so, and the UI explains it with the copy it already has.
package main

import (
	"time"

	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/demodata"
	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/view"
)

// version is stamped at link time by the release workflow:
//
//	-ldflags "-X main.version=v1.0.0"
//
// It is handed to demodata rather than read from there so that the one place a
// build identifies itself is a linker flag on the binary being built, which is
// the only place that can be right for both a tagged release and somebody's
// laptop.
var version = "dev"

func main() {
	demodata.Version = version

	// The instance is built BEFORE the first render and handed to the view, so
	// the demo has no connecting state to paint. There is nothing to wait for:
	// the "server" is a few dozen structs in this module.
	//
	// time.Now, not a fixed date. Every stamp in the fixtures is relative to
	// this call, which is what keeps the newest article "34 minutes ago" on
	// every load rather than eight months old by the autumn.
	conn := demodata.New(time.Now)
	client := data.DialDemo(conn)

	// Styles before the first render, so there is never a frame of unstyled
	// content. Same call, same reason, same sheet as client/app.
	design.Sheet()

	ui.Render(ui.CreateElement(view.DemoRoot, view.DemoProps{Client: client}), "#app")

	// wasm has no runtime to return to: main returning would tear the module
	// down and every registered callback with it.
	select {}
}
