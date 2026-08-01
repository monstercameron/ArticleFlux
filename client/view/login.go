//go:build js && wasm

package view

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
)

// Login is the credential screen.
//
// It is a separate component from Reader rather than a panel inside it, and that
// is not organisational tidiness. Reader holds forty-odd hooks and mounts a
// virtualised list; rendering it behind a login overlay would fetch a feed list
// the caller is not entitled to, get thirty Unauthenticated errors, and paint
// the furniture of an account nobody has proven they own. Root mounts one or the
// other, so an unauthenticated page never constructs the reader at all.
//
// Visually it is the app's own vocabulary — plum ground, the noise wash, Fraunces
// for the wordmark — because the first screen of a self-hosted reader should look
// like the reader and not like a bolted-on gate.
type loginProps struct {
	// tunnel is the URL to dial. Passed in so this component does not have to
	// re-derive it and disagree with Root.
	tunnel string
	// onSuccess hands the authenticated client up to Root, which swaps in the
	// reader. The connection is reused rather than redialled — a second tunnel
	// would count against the per-client connection cap for no reason.
	onSuccess func(*data.Client)
}

func Login(p loginProps) ui.Node {
	// The i18n Runtime, from the Provider Root mounts. A HOOK: once, at the
	// top, unconditionally — GWC matches hooks positionally. It is threaded
	// into the plain helpers below as a parameter rather than put on a props
	// struct, because Runtime carries func fields and a props struct holding
	// one compares unequal on every render, which would defeat the memo
	// bailout this pane depends on.
	tr := i18n.UseI18n()
	// Prefilled on a loopback origin, empty everywhere else.
	//
	// The case this exists for: a server started WITHOUT -dev on a development
	// machine, because you are working on the login screen itself or on something
	// behind it that needs a real session. Retyping the same two known strings
	// every reload is friction with nothing on the other side of it.
	//
	// The origin is the gate, and it is the right one. These are the documented
	// defaults from .env.example — not secrets, and not treated as any — but a
	// deployed instance must never prefill anything, and `platform.Origin()` is a
	// fact about where the PAGE was served from that no server response can
	// influence. A loopback origin cannot be reached from anywhere but this
	// machine, so the credentials cannot be shown to anyone who is not already
	// sitting at it.
	//
	// Note this is deliberately NOT gated on the server's dev_mode: in dev mode
	// there is no login screen to prefill, because Root goes straight to the
	// reader. This is for the case where a login IS required and the machine is
	// yours.
	devUser, devPass := devDefaults()
	username := ui.UseState(devUser)
	password := ui.UseState(devPass)
	// busy covers both the dial and the RPC. One flag rather than two because
	// from the reader's side they are the same event: "it is working on it".
	busy := ui.UseState(false)
	errMsg := ui.UseState("")
	// The client is dialled lazily on the first submit rather than on mount.
	// Opening a WebSocket for a screen someone may never submit is a connection
	// the server holds open for nothing, and this page is also what an
	// unauthenticated scanner sees.
	clientRef := ui.UseRef[*data.Client](nil)

	// Focus the username field once, on mount. A login screen whose first field
	// is not focused makes every reader reach for the mouse to do the one thing
	// the page exists for.
	focused := ui.UseRef(false)
	ui.UseEffect(func() func() {
		if !focused.Get() {
			focused.Set(true)
			platform.FocusField("login-username")
		}
		return nil
	}, []any{})

	submit := func() {
		if busy.Get() {
			return
		}
		u, pw := fieldOr("login-username", username.Get()), fieldOr("login-password", password.Get())
		// State is kept in step with what was read, because the fields are
		// controlled — `Value: username.Get()`. Submitting the field value
		// while leaving state behind would make the very next render write the
		// stale value back and blank a field the reader is looking at.
		username.Set(u)
		password.Set(pw)
		if u == "" || pw == "" {
			errMsg.Set(tr.T("login", "errEmpty"))
			return
		}
		busy.Set(true)
		errMsg.Set("")

		go func() {
			c := clientRef.Get()
			if c == nil {
				// onState is nil here: this screen has no connection indicator to
				// drive, and a login that flickered a "reconnecting" chip while
				// the socket came up would be reporting normal behaviour as a
				// fault.
				dialed, err := data.Dial(context.Background(), p.tunnel, nil)
				if err != nil {
					ui.PostAsync(func() {
						busy.Set(false)
						errMsg.Set(tr.T("login", "errDial", i18n.Args{"err": err.Error()}))
					})
					return
				}
				c = dialed
				clientRef.Set(c)
			}

			_, err := c.Login(context.Background(), u, pw)
			ui.PostAsync(func() {
				busy.Set(false)
				if err != nil {
					// The server's message is deliberately uniform for a bad
					// credential and specific for everything else (rate limiting,
					// the ambiguous-tenant case), so it is shown as given rather
					// than replaced with a guess about which one this was.
					errMsg.Set(loginMessage(tr, err))
					// The password is cleared and the username is not. Retyping a
					// username you got right is the small daily insult that makes
					// a login screen feel hostile.
					password.Set("")
					platform.ClearField("login-password")
					platform.FocusField("login-password")
					return
				}
				p.onSuccess(c)
			})
		}()
	}

	// Handlers are ui.UseEvent values created here, with the other hooks, and
	// NEVER inside the returned tree. GWC matches hooks positionally, so a
	// UseEvent evaluated inside a branch of the render binds to the wrong slot —
	// the failure the pane helpers in panes.go carry a comment about.
	onUser := ui.UseEvent(func(v string) { username.Set(v) })
	onPass := ui.UseEvent(func(v string) { password.Set(v) })
	onSubmit := ui.UseEvent(func() { submit() })

	// Enter submits from either field. A two-field form where Enter does nothing
	// is a form that fails the only interaction anyone attempts without looking.
	onKey := func(k platform.Key) {
		if k.Name == "Enter" && (k.Role == "login-username" || k.Role == "login-password") {
			submit()
		}
	}
	ui.UseEffect(func() func() {
		l := platform.OnKeyDown(onKey)
		return l.Release
	}, []any{username.Get(), password.Get(), busy.Get()})

	// The fields live inside a <form>, and that is functional rather than
	// semantic tidiness.
	//
	// Chrome says so out loud — "Password field is not contained in a form" — and
	// the consequence is that a password manager cannot reliably pair the
	// username with the password, offer to save the credential, or fill it later.
	// A reader who uses a manager is the reader most likely to have a password
	// worth having, so a login screen that defeats one pushes people towards a
	// worse password.
	//
	// No action and no submit handler: the button owns submission, and
	// `onsubmit: return false` stops the browser navigating if a stray Enter
	// reaches the form before the key handler does. That navigation would look
	// like the page reloading itself and losing what was typed.
	return html.Div(html.Props{Class: "login", Data: map[string]string{"phase": "login"}},
		html.Form(html.Props{Class: "login-card", Role: "main",
			Raw: map[string]any{"onsubmit": "return false"}},
			html.Div(html.Props{Class: "login-mark"}, html.Text(tr.T("login", "mark"))),
			html.P(html.Props{Class: "login-lede"},
				html.Text(tr.T("login", "lede"))),

			html.Div(html.Props{Class: "login-field"},
				html.Label(html.Props{Class: "login-label",
					Raw: map[string]any{"for": "login-username"}},
					html.Text(tr.T("login", "username"))),
				html.Input(html.Props{
					Class: "field login-input", Type: "text", ID: "login-username",
					Value:   username.Get(),
					OnInput: onUser,
					Data:    map[string]string{"role": "login-username"},
					// autocomplete matters more than it looks: without it a
					// password manager cannot offer to fill, and a reader who
					// uses one is the reader most likely to have a password worth
					// having.
					Raw: map[string]any{
						"autocomplete":   "username",
						"autocapitalize": "none",
						"spellcheck":     "false",
					},
				}),
			),

			html.Div(html.Props{Class: "login-field"},
				html.Label(html.Props{Class: "login-label",
					Raw: map[string]any{"for": "login-password"}},
					html.Text(tr.T("login", "password"))),
				html.Input(html.Props{
					Class: "field login-input", Type: "password", ID: "login-password",
					Value:   password.Get(),
					OnInput: onPass,
					Data:    map[string]string{"role": "login-password"},
					Raw:     map[string]any{"autocomplete": "current-password"},
				}),
			),

			// The error is rendered in a live region so a screen reader announces
			// a rejected login. Without it the only feedback is visual, and a
			// blind reader gets silence and an unchanged page.
			html.Div(html.Props{
				Class: errClass(errMsg.Get()),
				Role:  "alert",
				Aria:  map[string]string{"live": "polite"},
			}, html.Text(errMsg.Get())),

			html.Button(html.Props{
				Class:    "btn login-submit",
				Type:     "button",
				OnClick:  onSubmit,
				Disabled: busy.Get(),
			}, html.Text(submitLabel(tr, busy.Get()))),

			// No trailing punctuation after the code chip. The chip is padded, so
			// a period following it sits a visible gap away from the word and
			// reads as a stray mark rather than the end of a sentence.
			html.P(html.Props{Class: "login-foot"},
				html.Text(tr.T("login", "footPrefix")),
				html.Code(html.Props{}, html.Text(adduserCommand))),
		),
	)
}

// fieldOr reads the input carrying data-role=role, falling back to the state
// behind it when the field is not in the document.
//
// # Why the field wins
//
// A password manager, and Chrome's own autofill, writes the value straight into
// the element. Several of those paths dispatch no `input` event this component
// can hear — the same synthetic-event problem platform.OnKeyDown carries a
// comment about — so state stays empty while the screen visibly shows a filled
// username and password. Submitting state then sends two empty strings and the
// reader is told the credentials they can SEE are wrong.
//
// Measured before this existed: writing both values with no input event and
// submitting produced "invalid username or password" on Enter AND on the
// button. So this is not a keyboard bug, and fixing it in the key handler would
// have left the same trap under the mouse.
//
// The fallback is not decoration: on the render before the field is in the
// document, FieldValue returns "" and the prefilled dev default lives only in
// state.
func fieldOr(role, fallback string) string {
	if v := platform.FieldValue(role); v != "" {
		return v
	}
	return fallback
}

// adduserCommand is a command line, not copy. It stays out of the catalog on
// purpose: a translator who "improves" it hands the reader an instruction that
// does not work.
const adduserCommand = "articleflux adduser"

// The account `serve -dev` creates on first run, and the values documented in
// .env.example as ARTICLEFLUX_DEV_USER / ARTICLEFLUX_DEV_PASSWORD.
//
// Duplicated here rather than fetched from the server, and that is the only
// honest option: an endpoint that hands out a working credential to an
// unauthenticated caller is a backdoor no matter how carefully it is gated. They
// are constants in main.go and constants here, and .env.example says so in both
// directions. If the defaults change, this changes with them — a stale prefill
// costs one failed sign-in and a corrected guess, which is the cheapest failure
// mode available.
// Must stay in step with the same constants in cmd/articleflux/admin.go, and
// must satisfy the 12-character minimum that `init` enforces — the previous
// value was eleven, so the documented dev password was one `init` refused.
const (
	devUsername = "cam"
	devPassword = "articleflux-dev"
)

// devDefaults returns the prefill for a loopback origin, and empty strings
// otherwise.
//
// Loopback is matched on the HOST of the page origin, not on a substring.
// `strings.Contains(origin, "localhost")` would happily prefill on
// `https://localhost.attacker.example`, which is a real domain someone can own.
func devDefaults() (user, pass string) {
	if !isLoopbackOrigin(platform.Origin()) {
		return "", ""
	}
	return devUsername, devPassword
}

func isLoopbackOrigin(origin string) bool {
	host := origin
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	// Strip the port. IPv6 arrives bracketed — [::1]:9000 — so the closing
	// bracket, not the first colon, is what ends the host.
	if strings.HasPrefix(host, "[") {
		if i := strings.IndexByte(host, ']'); i >= 0 {
			host = host[:i+1]
		}
	} else if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	switch host {
	case "localhost", "127.0.0.1", "[::1]", "::1":
		return true
	}
	return false
}

func submitLabel(tr i18n.Runtime, busy bool) string {
	if busy {
		return tr.T("login", "working")
	}
	return tr.T("login", "submit")
}

// errClass hides the error slot when there is nothing to say, rather than
// removing it from the tree. A live region that is added to the document at the
// moment it gains content is a live region some screen readers never announce.
func errClass(msg string) string {
	if msg == "" {
		return "login-error is-empty"
	}
	return "login-error"
}

// loginMessage turns a gRPC error into something worth reading, in the reader's
// language.
//
// The server's own refusals now arrive with a catalog key attached (see
// serverText), so "invalid username or password" is translated rather than
// passed through as English. What is NOT translated, and cannot be, is a
// transport failure: gRPC composes those itself and its text describes a socket
// ("connection error: desc = transport is closing"), which tells a reader
// nothing and looks like their password broke the server. Those two codes get
// this app's own sentence instead.
func loginMessage(tr i18n.Runtime, err error) string {
	if err == nil {
		return ""
	}
	st, ok := status.FromError(err)
	if !ok {
		return tr.T("login", "errGeneric")
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded:
		return tr.T("login", "errUnreachable")
	case codes.Unauthenticated, codes.ResourceExhausted, codes.FailedPrecondition:
		// The three the server writes for a person: the uniform bad-credential
		// message, the rate limit, and the ambiguous-tenant case. Resolved
		// through the catalog when the server named a key, and passed through as
		// its English when it did not.
		return serverText(tr, err)
	default:
		return tr.T("login", "errGeneric")
	}
}
