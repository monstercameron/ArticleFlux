package host

import (
	_ "embed"
	"encoding/json"
	"net/url"
	"strings"
	"unicode"
)

// The page → shell channel.
//
// # Why there is a script at all
//
// A26 says the application has no JavaScript, and the client keeps that: it
// is Go, and web/index.html carries the boot shim and nothing else. The two
// things the shell needs from the page — "the title changed" and "this link
// leaves the app" — are both things the client already does in the browser
// (document.title, window.open / target=_blank), and both are things the
// native window cannot observe: the pinned Wails does not expose WebView2's
// DocumentTitleChanged or NewWindowRequested events, so an unhandled
// target=_blank opens a WebView2 popup — a second, chromeless browser inside
// the app, holding the publisher's page — rather than the person's browser.
//
// So the SHELL injects a script into its own window, after each navigation,
// through Wails' ExecJS. It is not served, not in web/, not in the wasm bundle
// and not present in any browser; it is the desktop host's code, the same
// way the Wails runtime it rides on is. The proper replacement is the client
// asking the GWC desktop bridge directly (docs/DESKTOP.md → "Hooks"), at which
// point this file goes.
//
//go:embed shim.js
var shimSource string

// Shim is the script for a window showing origin. The origin is baked in so the
// script refuses to install itself on any other page the window might reach.
func Shim(origin string) string {
	o, _ := json.Marshal(origin)
	return strings.Replace(shimSource, "__ORIGIN__", string(o), 1)
}

// Message kinds the shim posts.
const (
	msgOpen  = "articleflux:open "
	msgTitle = "articleflux:title "
)

// MessageKind is what a page message asks for.
type MessageKind int

const (
	MsgUnknown MessageKind = iota
	MsgOpenExternal
	MsgTitle
)

// Message is a validated request from the page.
type Message struct {
	Kind  MessageKind
	Value string
}

// maxTitle bounds what the page can put in the title bar. An article title is
// rarely past 200 characters; a megabyte of one is a bug or a hostile feed.
const maxTitle = 300

// maxURL bounds what is handed to the shell's URL opener.
const maxURL = 8192

// ParseMessage validates one message against the origin the window loads.
//
// senderOrigin is where WebView2 says the message came from. A message from
// any other document — a publisher page the window somehow reached, a frame —
// is dropped, because every kind here acts outside the page: one opens the
// person's browser, the other writes the title bar of a trusted window.
func ParseMessage(origin, senderOrigin, raw string) Message {
	if !sameOriginURL(origin, senderOrigin) {
		return Message{}
	}
	switch {
	case strings.HasPrefix(raw, msgOpen):
		if u, ok := ExternalURL(origin, strings.TrimPrefix(raw, msgOpen)); ok {
			return Message{Kind: MsgOpenExternal, Value: u}
		}
	case strings.HasPrefix(raw, msgTitle):
		return Message{Kind: MsgTitle, Value: CleanTitle(strings.TrimPrefix(raw, msgTitle))}
	}
	return Message{}
}

// ExternalURL accepts http and https addresses on another origin.
//
// Anything else is refused rather than passed through: the opener behind this
// is ShellExecute, and a `file:`, `ms-settings:` or `javascript:` address is a
// different program, not a web page. Our own origin is refused because the
// person's browser has no launch cookie for it — it would open on the gate's
// "open the app instead" page.
func ExternalURL(origin, s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxURL {
		return "", false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	if sameOriginURL(origin, u.String()) {
		return "", false
	}
	return u.String(), true
}

// CleanTitle strips what a title bar should not show.
func CleanTitle(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxTitle {
		s = string(r[:maxTitle-1]) + "…"
	}
	if s == "" {
		return "ArticleFlux"
	}
	return s
}

func sameOriginURL(origin, s string) bool {
	o, err1 := url.Parse(origin)
	u, err2 := url.Parse(s)
	if err1 != nil || err2 != nil {
		return false
	}
	return o.Scheme != "" && strings.EqualFold(o.Scheme, u.Scheme) && strings.EqualFold(o.Host, u.Host)
}

// Commands the shell sends the page, each of which is something the client
// already does when the reader asks for it — so a menu item and the keyboard
// are the same code path inside the client, not two.
type Command string

const (
	// CmdAddFeed opens the add-a-feed dialog through the client's own address
	// grammar (client/view/route.go: `<place>/add`).
	CmdAddFeed Command = "add"
	// CmdSettings opens Settings at its first tab (`/settings/reading`).
	CmdSettings Command = "settings"
	// CmdRefresh is the reader's `r` shortcut (client/view/reader_keyboard.go).
	CmdRefresh Command = "refresh"
)

// Script is the ExecJS for a command. The shim defines the receiver; on a page
// it did not install itself on, this is a no-op.
func (c Command) Script() string {
	q, _ := json.Marshal(string(c))
	return "window.__articlefluxDesktop&&window.__articlefluxDesktop.run(" + string(q) + ");"
}
