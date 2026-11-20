//go:build js && wasm

package view

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
	usernamepolicy "github.com/monstercameron/ArticleFlux/internal/username"
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
// Step one takes a username — which is an email address, see internal/username
// for the rule and the reason — and a password, twice. Step two shows
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
	password := ui.UseState("")
	confirm := ui.UseState("")
	// What the BOXES are told to hold. Four fields, one rule, and it is login.go's
	// — see the seeds there for the whole account of why a controlled `value` is
	// a character-eater under fast typing. This screen creates the account that
	// every later sign-in has to match, so a password silently missing a
	// character here locks somebody out of an instance they just installed, and
	// the confirm field would have agreed with it.
	//
	// Empty, and they stay empty: nothing on this screen is ever prefilled (the
	// loopback prefill is the sign-in card's, and a username it offers is an
	// existing account's, which this rule deliberately never re-judges), and the
	// only app-driven write is the two password fields being cleared after a
	// refusal that is about the password.
	userSeed := ui.UseState("")
	passSeed := ui.UseState("")
	confirmSeed := ui.UseState("")
	busy := ui.UseState(false)
	errMsg := ui.UseState("")
	// The data-role of the field the message is about, or "" when it is nobody's
	// (the server did not answer). It is what lets the message be wired to the box
	// it names — aria-invalid, aria-describedby — and the caret go there: a
	// sentence under the button saying a username has to be something is a puzzle
	// the reader solves by guessing which of three boxes it meant.
	errField := ui.UseState("")
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

	// fail puts a message beside the field it is about and moves the caret there.
	fail := func(role, msg string) {
		errField.Set(role)
		errMsg.Set(msg)
		if role != "" {
			platform.FocusField(role)
		}
	}
	// Editing the field a message is about retires the message: it described the
	// old text, and leaving it up beside a corrected box says the correction did
	// not take.
	retire := func(role string) {
		if errField.Get() == role && errMsg.Get() != "" {
			errField.Set("")
			errMsg.Set("")
		}
	}

	submit := func() {
		if busy.Get() {
			return
		}
		// The fields, not the state behind them — see fieldOr (login.go) for
		// the autofill path that leaves the two disagreeing. Setup is filled by
		// a password manager as readily as login is: generating the password is
		// the moment a manager is most likely to be driving the form.
		u := strings.TrimSpace(fieldOr("setup-username", username.Get()))
		pw := fieldOr("setup-password", password.Get())
		cf := fieldOr("setup-confirm", confirm.Get())
		username.Set(u)
		password.Set(pw)
		confirm.Set(cf)
		// The server's rule, applied here first: the refusal then arrives with the
		// press that caused it instead of after a hashing pause and a round trip,
		// and it can say what an address looks like rather than echo a policy.
		// internal/username is the one definition; this is not a second copy.
		var userErr error
		if u != "" {
			userErr = usernamepolicy.Check(usernamepolicy.Normalise(u))
		}
		switch {
		case u == "":
			fail("setup-username", tr.T("setup", "errUsernameEmpty"))
			return
		case userErr != nil:
			fail("setup-username", usernameProblem(tr, userErr))
			return
		case pw == "":
			fail("setup-password", tr.T("setup", "errPasswordEmpty"))
			return
		case pw != cf:
			// Checked here rather than only at the server, because the server
			// never sees the second field: a mismatch is a typing mistake, and
			// the fastest place to catch one is where it was typed.
			fail("setup-confirm", tr.T("setup", "errMismatch"))
			return
		}
		busy.Set(true)
		errMsg.Set("")
		errField.Set("")
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
			// No second address: the username IS the address, and a box for it
			// beside the username was one more place to type the same thing.
			res, err := c.Setup(context.Background(), u, "", pw)
			ui.PostAsync(func() {
				busy.Set(false)
				if err != nil {
					role, msg := setupMessage(tr, err)
					fail(role, msg)
					// The passwords go only when the refusal is about THEM. It used
					// to empty both on any refusal, so correcting one character of
					// the username cost two twelve-character phrases — and a
					// password manager's generated one is not something to retype.
					if role == "setup-password" {
						password.Set("")
						confirm.Set("")
						// The seeds with them: the app is emptying these fields, so it
						// is one of the moments a write to the box is safe, and
						// leaving the seeds behind would let a later render restore
						// what ClearField just removed.
						passSeed.Set("")
						confirmSeed.Set("")
						platform.ClearField("setup-password")
						platform.ClearField("setup-confirm")
					}
					return
				}
				codes.Set(res.GetRecoveryCodes())
				step.Set(stepCodes)
				// The form this replaced held focus and is gone, so focus fell to
				// <body> on the one screen whose whole job is to be read and acted
				// on: the Copy button is the first thing to do with the codes, and
				// a screen reader is told where it now is (H13).
				platform.FocusField("setup-copy")
			})
		}()
	}

	onUser := ui.UseEvent(func(v string) { username.Set(v); retire("setup-username") })
	onPass := ui.UseEvent(func(v string) { password.Set(v); retire("setup-password") })
	onConfirm := ui.UseEvent(func(v string) { confirm.Set(v); retire("setup-confirm") })
	onSubmit := ui.UseEvent(func() { submit() })
	onDone := ui.UseEvent(func() {
		if c := clientRef.Get(); c != nil {
			p.onSuccess(c)
		}
	})
	// What the last Copy press achieved: copyNone before one, then copyOK or
	// copyFailed. The sheet says which, because a Copy that silently did nothing
	// — the ordinary case on a self-hosted instance reached over plain HTTP —
	// is a reader who believes their codes are on the clipboard and has nothing
	// to paste.
	copied := ui.UseState(copyNone)
	onCopy := ui.UseEvent(func() {
		platform.WriteClipboard(strings.Join(codes.Get(), "\n"), func(ok bool) {
			ui.PostAsync(func() {
				if ok {
					copied.Set(copyOK)
				} else {
					copied.Set(copyFailed)
				}
			})
		})
	})

	onKey := func(k platform.Key) {
		if k.Name != "Enter" {
			return
		}
		switch k.Role {
		case "setup-username", "setup-password", "setup-confirm":
			submit()
		}
	}
	ui.UseEffect(func() func() {
		l := platform.OnKeyDown(onKey)
		return l.Release
	}, []any{username.Get(), password.Get(), confirm.Get(), busy.Get(), int(step.Get())})

	if step.Get() == stepCodes {
		return setupCodes(tr, codes.Get(), copied.Get(), onCopy, onDone)
	}

	return html.Main(html.Props{Class: "login", Data: map[string]string{"phase": "setup"}},
		html.Form(html.Props{Class: "login-card",
			OnSubmit: suppressSubmit()},
			// The page's heading, as on the login card — see Login.
			html.H1(html.Props{Class: "login-mark"}, html.Text(tr.T("setup", "mark"))),
			html.P(html.Props{Class: "login-lede"}, html.Text(tr.T("setup", "lede"))),

			kitField(kitFieldProps{Look: kitFormCard, Label: tr.T("setup", "username"),
				Hint: tr.T("setup", "usernameHint"), HintClass: "login-foot", ID: "setup-username",
				Control: html.Input(kitWire("setup-username", tr.T("setup", "usernameHint"), html.Props{
					// type=email for the on-screen keyboard (an @ without a hunt) and
					// the browser's own idea of what goes here; the form never submits
					// natively, so its constraint bubbles never fire over ours.
					Class: "field login-input", Type: "email",
					Placeholder: tr.T("setup", "usernamePlaceholder"),
					// The seed, not the state: see the seeds' declaration.
					Value:   userSeed.Get(),
					OnInput: onUser,
					Data:    map[string]string{"role": "setup-username"},
					Aria:    faultAria(errField.Get(), "setup-username", "setup-error"),
					// The login card's set, for the login card's reasons: a
					// username is an identifier, so a phone capitalising its
					// first letter or "correcting" it makes an account whose
					// name the reader then cannot type back. And "go", because
					// Enter in any field on this card submits it (onKey).
					Raw: map[string]any{
						"autocomplete":   "username",
						"autocapitalize": "none",
						"spellcheck":     "false",
						"enterkeyhint":   "go",
					},
				})),
			}),

			faultSlot(errField.Get(), "setup-username", "setup-error", errMsg.Get()),

			kitField(kitFieldProps{Look: kitFormCard, Label: tr.T("setup", "password"),
				Hint: tr.T("setup", "passwordHint"), HintClass: "login-foot", ID: "setup-password",
				Control: html.Input(kitWire("setup-password", tr.T("setup", "passwordHint"), html.Props{
					Class: "field login-input", Type: "password",
					Value:   passSeed.Get(),
					OnInput: onPass,
					Data:    map[string]string{"role": "setup-password"},
					Aria:    faultAria(errField.Get(), "setup-password", "setup-error"),
					Raw:     map[string]any{"autocomplete": "new-password", "enterkeyhint": "go"},
				})),
			}),

			faultSlot(errField.Get(), "setup-password", "setup-error", errMsg.Get()),

			kitField(kitFieldProps{Look: kitFormCard, Label: tr.T("setup", "confirm"), ID: "setup-confirm",
				Control: html.Input(kitWire("setup-confirm", "", html.Props{
					Class: "field login-input", Type: "password",
					Value:   confirmSeed.Get(),
					OnInput: onConfirm,
					Data:    map[string]string{"role": "setup-confirm"},
					Aria:    faultAria(errField.Get(), "setup-confirm", "setup-error"),
					Raw:     map[string]any{"autocomplete": "new-password", "enterkeyhint": "go"},
				})),
			}),

			// Where the message goes: directly under the field it is about when there
			// is one (faultSlot), and here — one slot, kept in the document while empty
			// (errClass's reason) — when it is about the form as a whole, or about the
			// confirmation box, which is the last one before this. The message names its
			// field and that field points back at it (faultAria), so the sentence is read
			// from the box it is about whichever place it was drawn.
			setupFormSlot(errField.Get(), errMsg.Get()),

			html.Button(html.Props{
				Class:    "btn login-submit",
				Type:     "button",
				OnClick:  onSubmit,
				Disabled: busy.Get(),
			}, html.Text(setupSubmitLabel(tr, busy.Get()))),
		),
	)
}

// The outcomes of the sheet's Copy button.
const (
	copyNone   = ""
	copyOK     = "copied"
	copyFailed = "failed"
)

// setupCodes is the second step: the sheet, and the only chance to keep it.
func setupCodes(tr i18n.Runtime, sheet []string, copied string, onCopy, onDone ui.Handler) ui.Node {
	lines := make([]ui.Node, 0, len(sheet))
	for _, c := range sheet {
		lines = append(lines, html.Li(html.Props{Class: "setup-code"}, html.Text(c)))
	}
	copyLabel := tr.T("setup", "codesCopy")
	note := ""
	switch copied {
	case copyOK:
		copyLabel = tr.T("setup", "codesCopied")
		note = tr.T("setup", "codesCopiedNote")
	case copyFailed:
		note = tr.T("setup", "codesCopyFailed")
	}
	return html.Main(html.Props{Class: "login", Data: map[string]string{"phase": "setup-codes"}},
		html.Div(html.Props{Class: "login-card"},
			html.H1(html.Props{Class: "login-mark"}, html.Text(tr.T("setup", "codesMark"))),
			html.P(html.Props{Class: "login-lede"}, html.Text(tr.T("setup", "codesLede"))),

			html.Ul(html.Props{Class: "setup-codes",
				Aria: map[string]string{"label": tr.T("setup", "codesAria")}}, lines...),

			html.P(html.Props{Class: "login-foot"}, html.Text(tr.T("setup", "codesWarn"))),

			// The outcome sits BESIDE the button it answers. Under the main button it
			// read as that button's result — somebody who pressed Copy looked at
			// Copy for the answer and saw nothing change but a word. It is a polite
			// status, always in the document and empty until Copy is pressed, so the
			// outcome is announced when it arrives (errClass's reason).
			html.Div(html.Props{Class: "set-actions setup-copyrow"},
				html.Button(html.Props{
					Class: "chip", Type: "button", OnClick: onCopy,
					Data: map[string]string{"role": "setup-copy", "copied": copied},
				}, html.Text(copyLabel)),
				kitLive(kitLiveProps{Class: "login-foot setup-copy-note", Role: kitRoleStatus, Live: kitLivePolite,
					Data: map[string]string{"bad": boolAttr(copied == copyFailed)}},
					html.Text(note)),
			),
			html.Button(html.Props{
				Class: "btn login-submit", Type: "button", OnClick: onDone,
				Data: map[string]string{"role": "setup-done"},
			}, html.Text(tr.T("setup", "codesDone"))),
		),
	)
}

func setupSubmitLabel(tr i18n.Runtime, busy bool) string {
	if busy {
		return tr.T("setup", "working")
	}
	return tr.T("setup", "submit")
}

// setupMessage turns a refusal into something the person can act on.
//
// It exists because loginMessage does not, and using it here cost a real
// signup: every refusal this endpoint makes for a REASON — a password in the
// known-password list, an address with no @, a username of one character — is
// InvalidArgument, which loginMessage maps to "Couldn't sign in. Check the
// server is running and try again." The server had said precisely what was
// wrong, the client threw it away, and the reader was sent to look at a server
// that was working perfectly.
//
// So the codes that carry a server-composed explanation resolve to it, via the
// same ErrorDetail key every other refusal in the app uses. Only a genuinely
// unexplained failure falls back, and it falls back to setup's own words rather
// than to a sentence about signing in, which is not what anybody on this screen
// is doing.
func setupMessage(tr i18n.Runtime, err error) (role, msg string) {
	if err == nil {
		return "", ""
	}
	st, ok := status.FromError(err)
	if !ok {
		return "", tr.T("setup", "errGeneric")
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded:
		return "", tr.T("setup", "errUnreachable")
	case codes.InvalidArgument, codes.FailedPrecondition,
		codes.ResourceExhausted, codes.Unauthenticated, codes.AlreadyExists:
		// Which box the server was talking about is in the key it classified the
		// refusal with, so the message can name it and the caret can go there.
		switch serverKey(err) {
		case "srv.badUsername", "srv.setupUsername", "srv.setupEmail":
			return "setup-username", tr.T("setup", "errAt", i18n.Args{
				"field": tr.T("setup", "atUsername"), "msg": serverText(tr, err)})
		case "srv.weakPassword":
			return "setup-password", tr.T("setup", "errAt", i18n.Args{
				"field": tr.T("setup", "atPassword"), "msg": serverText(tr, err)})
		}
		return "", serverText(tr, err)
	default:
		return "", tr.T("setup", "errGeneric")
	}
}

// usernameProblem is what is wrong with a username, in words that say what to
// type instead. The rule's own messages are for logs and the sync API; the
// reader is told what an address looks like.
func usernameProblem(tr i18n.Runtime, err error) string {
	switch {
	case errors.Is(err, usernamepolicy.ErrDisplayName):
		return tr.T("setup", "errUsernameBare")
	case errors.Is(err, usernamepolicy.ErrTooLong):
		return tr.T("setup", "errUsernameLong")
	default:
		return tr.T("setup", "errUsernameMail")
	}
}

// faultSlot draws a refusal directly under the field it is about, and nothing
// anywhere else — the form-level slot (setupFormSlot) stands down while it is
// here, so there is one element with the id and one live region speaking.
//
// A message under the button, three boxes from the one it names, was a sentence
// the reader had to carry back up the card; under the box it is the box's own
// caption, and the caret is already there.
func faultSlot(fault, role, id, msg string) ui.Node {
	if !kitHasWord(fault, role) || msg == "" {
		return nil
	}
	return loginAlert(id, msg)
}

// setupFormSlot is the card's own slot: the one a refusal lands in when it is
// about no field above, or about the last of them.
func setupFormSlot(fault, msg string) ui.Node {
	if kitHasWord(fault, "setup-username") || kitHasWord(fault, "setup-password") {
		return loginAlert("", "")
	}
	return loginAlert("setup-error", msg)
}

// faultAria is what the field a message is about carries: it is invalid, and the
// message is its description. Nothing for every other field — an aria-invalid on
// a box that is fine tells a screen reader to go and look for a problem.
func faultAria(fault, role, errID string) map[string]string {
	if !kitHasWord(fault, role) {
		return nil
	}
	return map[string]string{"invalid": "true", "describedby": errID}
}
