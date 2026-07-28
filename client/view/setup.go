//go:build js && wasm

package view

import (
	"context"
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
)

// First-run setup: the screen that claims an instance (§7.11).
//
// Root mounts this instead of Login when WhoAmI answers `needs_setup`, which is
// only ever true while the server has no accounts at all. It is the same
// vocabulary as the login screen on purpose — same card, same wordmark, same
// field styling — because the first thing somebody sees should look like the
// application they were sent to rather than like an installer that runs before
// it.
//
// # Two steps, and the second one is the point
//
// Step one takes a username, an optional email and a password. Step two shows
// the recovery codes and will not proceed until they have been acknowledged.
//
// Splitting them is what stops the codes being lost. They exist in readable form
// exactly once, in the response to Setup — the server stores only hashes — so a
// screen that dropped straight into the reader would destroy them a beat after
// creating them, and the reader would discover that months later, locked out,
// with nothing to type. The acknowledgement is a deliberate speed bump in the
// one flow where speed is worth less than the thing it costs.
type setupProps struct {
	// tunnel is the URL to dial, passed in so this component and Root cannot
	// disagree about it.
	tunnel string
	// onSuccess hands the authenticated client up to Root. Setup returns a
	// session, so claiming the instance signs you in — asking somebody to type
	// the password they just chose into a login screen is asking them to prove
	// something they proved by choosing it.
	onSuccess func(*data.Client)
}

// setupStep is which half of the flow is showing.
type setupStep int

const (
	stepForm setupStep = iota
	stepCodes
)

func Setup(p setupProps) ui.Node {
	tr := i18n.UseI18n()

	step := ui.UseState(stepForm)
	username := ui.UseState("")
	email := ui.UseState("")
	password := ui.UseState("")
	confirm := ui.UseState("")
	busy := ui.UseState(false)
	errMsg := ui.UseState("")
	// The sheet, held between the two steps. State rather than a ref because the
	// second step renders from it.
	codes := ui.UseState[[]string](nil)
	// The client, dialled once and handed on. A ref so a re-render does not open
	// a second tunnel.
	clientRef := ui.UseRef[*data.Client](nil)

	focused := ui.UseRef(false)
	ui.UseEffect(func() func() {
		if !focused.Get() {
			focused.Set(true)
			platform.FocusField("setup-username")
		}
		return nil
	}, []any{})

	submit := func() {
		if busy.Get() {
			return
		}
		u := strings.TrimSpace(username.Get())
		pw, cf := password.Get(), confirm.Get()
		switch {
		case u == "" || pw == "":
			errMsg.Set(tr.T("setup", "errEmpty"))
			return
		case pw != cf:
			// Checked here rather than only at the server, because the server
			// never sees the second field: a mismatch is a typing mistake, and
			// the fastest place to catch one is where it was typed.
			errMsg.Set(tr.T("setup", "errMismatch"))
			return
		}
		busy.Set(true)
		errMsg.Set("")
		go func() {
			c := clientRef.Get()
			if c == nil {
				dialed, err := data.Dial(context.Background(), p.tunnel, nil)
				if err != nil {
					ui.PostAsync(func() {
						busy.Set(false)
						errMsg.Set(tr.T("setup", "errDial", i18n.Args{"err": err.Error()}))
					})
					return
				}
				c = dialed
				clientRef.Set(c)
			}
			res, err := c.Setup(context.Background(), u, strings.TrimSpace(email.Get()), pw)
			ui.PostAsync(func() {
				busy.Set(false)
				if err != nil {
					// The server's own words. Its refusals here are things a
					// person can act on — a password in the known-password list,
					// an address with no @, an instance somebody else just
					// claimed — and paraphrasing them into "setup failed" throws
					// away the only part that helps.
					errMsg.Set(loginMessage(tr, err))
					password.Set("")
					confirm.Set("")
					platform.ClearField("setup-password")
					platform.ClearField("setup-confirm")
					return
				}
				codes.Set(res.GetRecoveryCodes())
				step.Set(stepCodes)
			})
		}()
	}

	onUser := ui.UseEvent(func(v string) { username.Set(v) })
	onEmail := ui.UseEvent(func(v string) { email.Set(v) })
	onPass := ui.UseEvent(func(v string) { password.Set(v) })
	onConfirm := ui.UseEvent(func(v string) { confirm.Set(v) })
	onSubmit := ui.UseEvent(func() { submit() })
	onDone := ui.UseEvent(func() {
		if c := clientRef.Get(); c != nil {
			p.onSuccess(c)
		}
	})
	onCopy := ui.UseEvent(func() {
		platform.WriteClipboard(strings.Join(codes.Get(), "\n"))
	})

	onKey := func(k platform.Key) {
		if k.Name != "Enter" {
			return
		}
		switch k.Role {
		case "setup-username", "setup-email", "setup-password", "setup-confirm":
			submit()
		}
	}
	ui.UseEffect(func() func() {
		l := platform.OnKeyDown(onKey)
		return l.Release
	}, []any{username.Get(), password.Get(), confirm.Get(), busy.Get(), int(step.Get())})

	if step.Get() == stepCodes {
		return setupCodes(tr, codes.Get(), onCopy, onDone)
	}

	return html.Div(html.Props{Class: "login", Data: map[string]string{"phase": "setup"}},
		html.Form(html.Props{Class: "login-card", Role: "main",
			Raw: map[string]any{"onsubmit": "return false"}},
			html.Div(html.Props{Class: "login-mark"}, html.Text(tr.T("setup", "mark"))),
			html.P(html.Props{Class: "login-lede"}, html.Text(tr.T("setup", "lede"))),

			html.Div(html.Props{Class: "login-field"},
				html.Label(html.Props{Class: "login-label",
					Raw: map[string]any{"for": "setup-username"}},
					html.Text(tr.T("setup", "username"))),
				html.Input(html.Props{
					Class: "field login-input", Type: "text", ID: "setup-username",
					Value:   username.Get(),
					OnInput: onUser,
					Data:    map[string]string{"role": "setup-username"},
					Raw:     map[string]any{"autocomplete": "username"},
				}),
			),

			html.Div(html.Props{Class: "login-field"},
				html.Label(html.Props{Class: "login-label",
					Raw: map[string]any{"for": "setup-email"}},
					html.Text(tr.T("setup", "email"))),
				html.Input(html.Props{
					Class: "field login-input", Type: "email", ID: "setup-email",
					Value:   email.Get(),
					OnInput: onEmail,
					Data:    map[string]string{"role": "setup-email"},
					Raw:     map[string]any{"autocomplete": "email"},
				}),
				// Said next to the field rather than in a policy nobody opens:
				// this server has no mailer, so an address here is a label and
				// not a way back in. The codes on the next screen are the way
				// back in, and the two facts belong together.
				html.P(html.Props{Class: "login-foot"}, html.Text(tr.T("setup", "emailHint"))),
			),

			html.Div(html.Props{Class: "login-field"},
				html.Label(html.Props{Class: "login-label",
					Raw: map[string]any{"for": "setup-password"}},
					html.Text(tr.T("setup", "password"))),
				html.Input(html.Props{
					Class: "field login-input", Type: "password", ID: "setup-password",
					Value:   password.Get(),
					OnInput: onPass,
					Data:    map[string]string{"role": "setup-password"},
					Raw:     map[string]any{"autocomplete": "new-password"},
				}),
			),

			html.Div(html.Props{Class: "login-field"},
				html.Label(html.Props{Class: "login-label",
					Raw: map[string]any{"for": "setup-confirm"}},
					html.Text(tr.T("setup", "confirm"))),
				html.Input(html.Props{
					Class: "field login-input", Type: "password", ID: "setup-confirm",
					Value:   confirm.Get(),
					OnInput: onConfirm,
					Data:    map[string]string{"role": "setup-confirm"},
					Raw:     map[string]any{"autocomplete": "new-password"},
				}),
			),

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
			}, html.Text(setupSubmitLabel(tr, busy.Get()))),
		),
	)
}

// setupCodes is the second step: the sheet, and the only chance to keep it.
func setupCodes(tr i18n.Runtime, sheet []string, onCopy, onDone ui.Handler) ui.Node {
	lines := make([]ui.Node, 0, len(sheet))
	for _, c := range sheet {
		lines = append(lines, html.Li(html.Props{Class: "setup-code"}, html.Text(c)))
	}
	return html.Div(html.Props{Class: "login", Data: map[string]string{"phase": "setup-codes"}},
		html.Div(html.Props{Class: "login-card", Role: "main"},
			html.Div(html.Props{Class: "login-mark"}, html.Text(tr.T("setup", "codesMark"))),
			html.P(html.Props{Class: "login-lede"}, html.Text(tr.T("setup", "codesLede"))),

			html.Ul(html.Props{Class: "setup-codes",
				Aria: map[string]string{"label": tr.T("setup", "codesAria")}}, lines...),

			html.P(html.Props{Class: "login-foot"}, html.Text(tr.T("setup", "codesWarn"))),

			html.Div(html.Props{Class: "set-actions"},
				html.Button(html.Props{
					Class: "chip", Type: "button", OnClick: onCopy,
					Data: map[string]string{"role": "setup-copy"},
				}, html.Text(tr.T("setup", "codesCopy"))),
				html.Button(html.Props{
					Class: "btn login-submit", Type: "button", OnClick: onDone,
					Data: map[string]string{"role": "setup-done"},
				}, html.Text(tr.T("setup", "codesDone"))),
			),
		),
	)
}

func setupSubmitLabel(tr i18n.Runtime, busy bool) string {
	if busy {
		return tr.T("setup", "working")
	}
	return tr.T("setup", "submit")
}
