//go:build js && wasm

package view

import (
	"context"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
	"github.com/monstercameron/ArticleFlux/internal/authn"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
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
	// resetToken is the credential from a `/reset?token=…` link, already read
	// out of the address and already stripped from it by Root (see
	// resetTokenFrom). Non-empty means this screen opens in recovery mode with
	// the token filled in.
	//
	// Passed as a prop rather than read here, because Root is where the address
	// is decided and a component that reached back into `location` would be a
	// second opinion about the same fact — and the wrong one, since by the time
	// this renders the token is deliberately no longer in the URL.
	resetToken string
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
	// What the BOXES are told to hold, which is not the same state as what has
	// been typed into them.
	//
	// `value` is a property the reconciler writes whenever the prop differs from
	// the previous render's, and it compares against that previous PROP rather
	// than against what the input actually contains. A render that resolves
	// after the next keystroke therefore puts its own older string back, and the
	// character typed in between is gone from the DOM — not merely from state.
	// Measured on the search box at 80ms/key (see reader.go's searchSeed); these
	// fields are typed at 15ms/key by the keyboard suite and by every password
	// manager, which is the same bug with less room.
	//
	// It matters more here than anywhere else in the application. `submit` reads
	// the FIELDS rather than state precisely so that what is on screen is what
	// is sent — so a character the reconciler deleted is a character deleted
	// from the credential, and the reader is told their password is wrong. That
	// is the exact conclusion this screen's own test file says a reader jumps
	// to, and it is unfalsifiable from their side.
	//
	// The seeds move only where the app has something to say: the loopback
	// prefill they are created with, and the password being cleared after a
	// rejected attempt. Nobody is typing at either moment.
	userSeed := ui.UseState(devUser)
	passSeed := ui.UseState(devPass)
	// busy covers both the dial and the RPC. One flag rather than two because
	// from the reader's side they are the same event: "it is working on it".
	busy := ui.UseState(false)
	errMsg := ui.UseState("")
	// The client is dialled lazily on the first submit rather than on mount.
	// Opening a WebSocket for a screen someone may never submit is a connection
	// the server holds open for nothing, and this page is also what an
	// unauthenticated scanner sees.
	clientRef := ui.UseRef[*data.Client](nil)

	// --- account recovery (§7.2) ---------------------------------------------
	//
	// A mode on this component rather than a route of its own. Recovery is the
	// same screen answering a different question, it shares the dialled client
	// and the error slot, and a separate route would need its own copy of the
	// dial, the busy flag and the seed discipline above — three things whose
	// duplicates would drift.
	//
	// The state below is declared unconditionally, like every hook here: GWC
	// matches hooks positionally, so a UseState behind `if recovering` binds to
	// whatever slot the other branch left. That is the failure the pane helpers
	// carry a comment about, and a login screen is a bad place to rediscover it.
	//
	// A reset link opens straight into it with the token already in the box, so
	// the reader's only remaining job is the one nobody else can do: choose a
	// password. `recoverSubmit` routes on the credential's SHAPE rather than on
	// how it arrived, so the token needs no flag travelling beside it — a pasted
	// link and a followed one take the same path from here.
	recovering := ui.UseState(p.resetToken != "")
	code := ui.UseState(p.resetToken)
	newPass := ui.UseState("")
	codeSeed := ui.UseState(p.resetToken)
	newPassSeed := ui.UseState("")
	// notice is the "3 codes left" line, and it is deliberately not errMsg: it
	// is shown on SUCCESS, styled as information, and reusing the error slot for
	// it would tell a reader who just recovered their account that something
	// went wrong.
	notice := ui.UseState("")
	// recovered holds the screen after a successful redemption, and it exists
	// because the sentence above was never readable.
	//
	// The notice was set in the same callback that called `onSuccess`, and
	// `onSuccess` swaps Root's phase to the reader — so the card carrying the
	// line was unmounted in the same frame it was written into. Nobody ever saw
	// it, including the reader down to their LAST code, who is the one person
	// the count is for: they are one lost password away from having no way back
	// into the account at all, and they were told so for zero frames.
	//
	// So the handover waits for a deliberate press. One extra click on the
	// rarest screen in the application, in exchange for the one moment where
	// "print a new sheet" is both true and actionable.
	recovered := ui.UseState(false)

	// Focus the username field once, on mount. A login screen whose first field
	// is not focused makes every reader reach for the mouse to do the one thing
	// the page exists for.
	focused := ui.UseRef(false)
	ui.UseEffect(func() func() {
		if !focused.Get() {
			focused.Set(true)
			// A reset link has already supplied the code and does not need the
			// username, so the first EMPTY field is the new password — putting
			// the caret in a box that is already filled would make the one
			// remaining step the one thing the reader has to go looking for.
			if p.resetToken != "" {
				platform.FocusField("recover-password")
				return nil
			}
			platform.FocusField("login-username")
		}
		return nil
	}, []any{})

	submit := func() {
		if busy.Get() {
			return
		}
		u, pw := fieldOr("login-username", username.Get()), fieldOr("login-password", password.Get())
		// State is kept in step with what was read. The boxes are no longer bound
		// to it (see the seeds), so this can no longer blank a field the reader
		// is looking at — but state is what every other reader of these values
		// sees, and leaving it behind after a submit would mean the component
		// disagreeing with the screen about what was just sent.
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
					// The seed too, and this is the second of the two moments it
					// is allowed to move: the field is being emptied by the app,
					// not by whoever is at the keyboard. ClearField does the DOM
					// write; this stops a later render restoring the seed's old
					// value over it.
					passSeed.Set("")
					platform.ClearField("login-password")
					platform.FocusField("login-password")
					return
				}
				p.onSuccess(c)
			})
		}()
	}

	// recoverSubmit spends a recovery code or a reset link and signs the reader
	// back in.
	//
	// It reads the fields rather than state for the same reason submit does: a
	// password manager filling the new-password box may dispatch no `input`
	// event, and submitting state would send an empty string and refuse a
	// perfectly good recovery.
	recoverSubmit := func() {
		if busy.Get() {
			return
		}
		secretIn := fieldOr("recover-code", code.Get())
		pw := fieldOr("recover-password", newPass.Get())
		u := fieldOr("login-username", username.Get())
		code.Set(secretIn)
		newPass.Set(pw)
		username.Set(u)

		if secretIn == "" || pw == "" {
			errMsg.Set(tr.T("login", "errRecoverEmpty"))
			return
		}
		// Which credential this is decides which RPC answers it. The shape rule
		// lives in internal/authn beside the generators, so the client cannot
		// disagree with the server about what a code looks like.
		isCode := authn.LooksLikeRecoveryCode(secretIn)
		if isCode && u == "" {
			// Only the code path needs a username: a reset token names its own
			// account. Saying which field is missing beats "fill in the form" on
			// a screen with three of them.
			errMsg.Set(tr.T("login", "errRecoverNoUser"))
			platform.FocusField("login-username")
			return
		}

		busy.Set(true)
		errMsg.Set("")
		notice.Set("")

		go func() {
			c := clientRef.Get()
			if c == nil {
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

			var remaining int32
			var err error
			if isCode {
				var res *pb.RedeemRecoveryCodeResponse
				res, err = c.Recover(context.Background(), u, secretIn, pw)
				if res != nil {
					remaining = res.GetCodesRemaining()
				}
			} else {
				_, err = c.RecoverWithResetToken(context.Background(),
					authn.ExtractResetToken(secretIn), pw)
				// A reset does not touch the sheet, so there is no count to
				// report and the -1 sentinel suppresses the line entirely.
				remaining = -1
			}

			ui.PostAsync(func() {
				busy.Set(false)
				if err != nil {
					errMsg.Set(loginMessage(tr, err))
					// The code is cleared and the username is not, mirroring what
					// a failed login does with the password: a code that was
					// refused is one to re-read off the sheet, and leaving the
					// wrong one in the box invites submitting it again.
					code.Set("")
					codeSeed.Set("")
					platform.ClearField("recover-code")
					platform.FocusField("recover-code")
					return
				}
				// Told BEFORE the reader is handed to the app, and the handover
				// WAITS for them — see `recovered`. Setting the notice and
				// calling onSuccess in the same callback is what made this
				// invisible: the screen holding the sentence was replaced in the
				// frame that wrote it.
				//
				// The reset-token path has no count to report (remaining is the
				// -1 sentinel) and nothing to hold the screen for, so it hands
				// over immediately, exactly as a login does.
				if remaining < 0 {
					p.onSuccess(c)
					return
				}
				notice.Set(remainingNotice(tr, remaining))
				recovered.Set(true)
			})
		}()
	}

	// handOver finishes a recovery the reader has acknowledged.
	//
	// The client is the one `recoverSubmit` dialled and authenticated; it is
	// read back from the ref rather than captured, because the press happens in
	// a later render than the RPC that produced it.
	handOver := func() {
		if c := clientRef.Get(); c != nil {
			p.onSuccess(c)
		}
	}

	// Handlers are ui.UseEvent values created here, with the other hooks, and
	// NEVER inside the returned tree. GWC matches hooks positionally, so a
	// UseEvent evaluated inside a branch of the render binds to the wrong slot —
	// the failure the pane helpers in panes.go carry a comment about.
	//
	// That applies to the recovery handlers too, which is why they are created
	// unconditionally here rather than inside the branch that renders them.
	onUser := ui.UseEvent(func(v string) { username.Set(v) })
	onPass := ui.UseEvent(func(v string) { password.Set(v) })
	onSubmit := ui.UseEvent(func() { submit() })
	onCode := ui.UseEvent(func(v string) { code.Set(v) })
	onNewPass := ui.UseEvent(func(v string) { newPass.Set(v) })
	onRecoverSubmit := ui.UseEvent(func() { recoverSubmit() })
	onRecoverContinue := ui.UseEvent(func() { handOver() })
	onShowRecover := ui.UseEvent(func() {
		recovering.Set(true)
		recovered.Set(false)
		errMsg.Set("")
		notice.Set("")
	})
	onBackToLogin := ui.UseEvent(func() {
		recovering.Set(false)
		recovered.Set(false)
		errMsg.Set("")
		notice.Set("")
		// The typed code does not survive the trip. It is a single-use credential
		// and leaving it in a hidden field is the sort of thing that ends up in a
		// screenshot; re-typing one code is not a hardship next to that.
		code.Set("")
		codeSeed.Set("")
		newPass.Set("")
		newPassSeed.Set("")
	})

	// Enter submits from every field, on whichever screen is up. A form where
	// Enter does nothing is a form that fails the only interaction anyone
	// attempts without looking.
	//
	// **The mode is checked before the role**, and that ordering is the bug this
	// carries a comment about. The recovery card reuses `login-username` — on
	// purpose, so a password manager pairs it with the new password — so a
	// handler that dispatched on the role alone sent Enter from the recovery
	// screen's first field into the LOGIN submit: a sign-in attempt with the old
	// password the reader is standing there because they do not have. The other
	// two fields were not in the list at all, so Enter in the code box did
	// nothing whatsoever.
	onKey := func(k platform.Key) {
		if k.Name != "Enter" {
			return
		}
		switch enterActionFor(recovering.Get(), recovered.Get(), k.Role) {
		case enterSignIn:
			submit()
		case enterRecover:
			recoverSubmit()
		case enterContinue:
			handOver()
		}
	}
	// `recovering` and `recovered` are in the deps for the same reason the field
	// values are: this listener closes over the two submit functions, and a
	// dependency list that cannot see the mode is one that re-registers on every
	// keystroke and never on the switch between them.
	ui.UseEffect(func() func() {
		l := platform.OnKeyDown(onKey)
		return l.Release
	}, []any{username.Get(), password.Get(), code.Get(), newPass.Get(),
		busy.Get(), recovering.Get(), recovered.Get()})

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
	if recovering.Get() {
		return recoverCard(recoverCardProps{
			tr: tr, busy: busy.Get(), errMsg: errMsg.Get(), notice: notice.Get(),
			recovered: recovered.Get(),
			userSeed:  userSeed.Get(), codeSeed: codeSeed.Get(), passSeed: newPassSeed.Get(),
			onUser: onUser, onCode: onCode, onPass: onNewPass,
			onSubmit: onRecoverSubmit, onBack: onBackToLogin,
			onContinue: onRecoverContinue,
		})
	}

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
					// userSeed, not username: the box owns its own text while
					// somebody is typing into it. See the seeds' declaration.
					Value:   userSeed.Get(),
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
					Value:   passSeed.Get(),
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

			// The way back in for somebody who cannot sign in, which until §7.3b
			// the application did not have — the sheet of codes Setup printed was
			// never redeemable by anything.
			//
			// A button rather than an anchor: it switches a mode on this component
			// and navigates nowhere, and an <a href="#"> that does neither is the
			// thing that breaks middle-click and the back button.
			html.Button(html.Props{
				Class: "login-alt", Type: "button", OnClick: onShowRecover,
				Data: map[string]string{"role": "login-recover"},
			}, html.Text(tr.T("login", "recoverLink"))),

			// No trailing punctuation after the code chip. The chip is padded, so
			// a period following it sits a visible gap away from the word and
			// reads as a stray mark rather than the end of a sentence.
			html.P(html.Props{Class: "login-foot"},
				html.Text(tr.T("login", "footPrefix")),
				html.Code(html.Props{}, html.Text(adduserCommand))),
		),
	)
}

// recoverCardProps is the recovery screen's whole input.
//
// A plain struct of values and ui.UseEvent handles, and deliberately NO
// i18n.Runtime field — a Runtime carries func fields, so a props struct holding
// one compares unequal on every render and defeats the memo bailout. It is
// passed as an ordinary parameter instead, which is the rule for every plain
// helper in this package.
type recoverCardProps struct {
	tr             i18n.Runtime
	busy           bool
	errMsg, notice string
	// recovered swaps the whole card for the confirmation — see Login's own
	// `recovered` for why the handover waits for a press.
	recovered          bool
	userSeed, codeSeed string
	passSeed           string
	// ui.Handler, not any: these come from ui.UseEvent and the reconciler
	// requires the concrete type. Typing them here is what makes a handler wired
	// to the wrong slot a compile error rather than a button that does nothing.
	onUser, onCode, onPass ui.Handler
	onSubmit, onBack       ui.Handler
	onContinue             ui.Handler
}

// recoverCard renders the "get back in" screen.
//
// A plain function rather than a component: it has no state and no hooks of its
// own — everything it shows and everything it calls is owned by Login, which is
// what keeps the hook order in one place and out of a branch.
func recoverCard(p recoverCardProps) ui.Node {
	tr := p.tr
	if p.recovered {
		return recoveredCard(p)
	}
	return html.Div(html.Props{Class: "login", Data: map[string]string{"phase": "recover"}},
		html.Form(html.Props{Class: "login-card", Role: "main",
			Raw: map[string]any{"onsubmit": "return false"}},
			html.Div(html.Props{Class: "login-mark"}, html.Text(tr.T("login", "mark"))),
			html.H1(html.Props{Class: "login-title"}, html.Text(tr.T("login", "recoverTitle"))),
			html.P(html.Props{Class: "login-lede"}, html.Text(tr.T("login", "recoverLede"))),

			// The username comes first and reuses the login field's id, so a
			// password manager still pairs it with the new password below and
			// offers to update the stored credential — which is exactly what a
			// reader wants after changing it, and exactly what they will not do
			// by hand.
			html.Div(html.Props{Class: "login-field"},
				html.Label(html.Props{Class: "login-label",
					Raw: map[string]any{"for": "login-username"}},
					html.Text(tr.T("login", "username"))),
				html.Input(html.Props{
					Class: "field login-input", Type: "text", ID: "login-username",
					Value:   p.userSeed,
					OnInput: p.onUser,
					Data:    map[string]string{"role": "login-username"},
					Raw: map[string]any{
						"autocomplete":   "username",
						"autocapitalize": "none",
						"spellcheck":     "false",
					},
				}),
			),

			html.Div(html.Props{Class: "login-field"},
				html.Label(html.Props{Class: "login-label",
					Raw: map[string]any{"for": "recover-code"}},
					html.Text(tr.T("login", "recoverCode"))),
				html.Input(html.Props{
					Class: "field login-input", Type: "text", ID: "recover-code",
					Value:   p.codeSeed,
					OnInput: p.onCode,
					Data:    map[string]string{"role": "recover-code"},
					// autocomplete off, and this is the one field on either screen
					// where that is right: a recovery code is single-use, so a
					// manager offering last month's spent code back is offering a
					// credential that is guaranteed not to work.
					Raw: map[string]any{
						"autocomplete":   "off",
						"autocapitalize": "characters",
						"spellcheck":     "false",
					},
				}),
				html.P(html.Props{Class: "login-hint"},
					html.Text(tr.T("login", "recoverCodeHint"))),
			),

			html.Div(html.Props{Class: "login-field"},
				html.Label(html.Props{Class: "login-label",
					Raw: map[string]any{"for": "recover-password"}},
					html.Text(tr.T("login", "recoverPassword"))),
				html.Input(html.Props{
					Class: "field login-input", Type: "password", ID: "recover-password",
					Value:   p.passSeed,
					OnInput: p.onPass,
					Data:    map[string]string{"role": "recover-password"},
					// new-password, not current-password: it tells a manager to
					// offer a generated one and to update the saved entry rather
					// than fill the old value into the box being used to replace it.
					Raw: map[string]any{"autocomplete": "new-password"},
				}),
			),

			html.Div(html.Props{
				Class: errClass(p.errMsg),
				Role:  "alert",
				Aria:  map[string]string{"live": "polite"},
			}, html.Text(p.errMsg)),

			// Separate from the error slot and announced politely: this is good
			// news with a number in it, and a screen reader that skipped it would
			// drop the one prompt telling somebody to print a new sheet.
			html.Div(html.Props{
				Class: noticeClass(p.notice),
				Aria:  map[string]string{"live": "polite"},
			}, html.Text(p.notice)),

			html.Button(html.Props{
				Class:    "btn login-submit",
				Type:     "button",
				OnClick:  p.onSubmit,
				Disabled: p.busy,
				Data:     map[string]string{"role": "recover-submit"},
			}, html.Text(recoverSubmitLabel(tr, p.busy))),

			html.Button(html.Props{
				Class: "login-alt", Type: "button", OnClick: p.onBack,
				Data: map[string]string{"role": "recover-back"},
			}, html.Text(tr.T("login", "backToLogin"))),
		),
	)
}

// enterTarget is what pressing Enter does from one field, on one screen.
type enterTarget int

const (
	enterNothing enterTarget = iota
	enterSignIn
	enterRecover
	enterContinue
)

// enterActionFor decides which action Enter runs.
//
// # Why this is a pure function and not four lines inside the key handler
//
// It was four lines inside the key handler, and it was wrong in a way nothing
// could see. The handler dispatched on the field's ROLE alone, and the recovery
// card deliberately reuses `login-username` — so a password manager pairs it
// with the new password below. Enter from that field therefore ran the LOGIN
// submit while the recovery screen was up: an attempt to sign in with the very
// password the reader is on that screen because they do not have. The other two
// recovery fields were not in the list at all, so Enter in the code box did
// nothing whatsoever.
//
// Neither failure is visible from outside the component, and neither would show
// up in a rendered-HTML assertion — which is what the rest of this package's
// tests can see. Pulling the decision out makes the whole table assertable
// without a browser, in the same spirit as route.go's string codec.
//
// **The mode is read before the role**, which is the entire fix: which screen is
// up decides what Enter means, and the field only refines it.
func enterActionFor(recovering, recovered bool, role string) enterTarget {
	if recovering {
		// After a successful recovery the card holds a notice and one button.
		// Enter is what that button answers to, from anywhere on the screen —
		// there is no field left to be in.
		if recovered {
			return enterContinue
		}
		switch role {
		case "login-username", "recover-code", "recover-password":
			return enterRecover
		}
		return enterNothing
	}
	switch role {
	case "login-username", "login-password":
		return enterSignIn
	}
	return enterNothing
}

// recoveredCard is the screen a reader sees for as long as they want to look at
// it, between a spent recovery code and the reader.
//
// It is the whole card rather than a line added to the form because the form is
// finished: the code has been redeemed, the password has been changed, and
// leaving three filled fields on screen invites somebody to press the button
// again with a credential that no longer exists. What is left is one sentence
// and one way forward.
//
// data-phase says `recovered`, distinct from `recover`, so a test can tell "the
// form is up" from "the form succeeded" without reading the copy — the same
// reason bootSplash carries one.
func recoveredCard(p recoverCardProps) ui.Node {
	tr := p.tr
	return html.Div(html.Props{Class: "login", Data: map[string]string{"phase": "recovered"}},
		html.Div(html.Props{Class: "login-card", Role: "main"},
			html.Div(html.Props{Class: "login-mark"}, html.Text(tr.T("login", "mark"))),
			html.H1(html.Props{Class: "login-title"}, html.Text(tr.T("login", "recoveredTitle"))),

			// Announced, not merely shown. A reader who cannot see the screen is
			// as entitled to "this was your last code" as one who can, and this
			// is the only place the sentence is ever said.
			html.P(html.Props{
				Class: noticeClass(p.notice),
				Role:  "status",
				Aria:  map[string]string{"live": "polite"},
			}, html.Text(p.notice)),

			html.P(html.Props{Class: "login-hint"},
				html.Text(tr.T("login", "recoveredHint"))),

			html.Button(html.Props{
				Class:   "btn login-submit",
				Type:    "button",
				OnClick: p.onContinue,
				Data:    map[string]string{"role": "recover-continue"},
			}, html.Text(tr.T("login", "recoveredContinue"))),
		),
	)
}

// noticeClass mirrors errClass: the slot stays in the tree and is hidden when
// empty, because a live region added to the document at the moment it gains
// content is one some screen readers never announce.
func noticeClass(msg string) string {
	if msg == "" {
		return "login-notice is-empty"
	}
	return "login-notice"
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

// remainingNotice says how much of the sheet is left.
//
// The last one gets its own sentence rather than "1 recovery codes left". Not
// only because the grammar is wrong: somebody down to their final code has no
// recovery at all after the next time, and that deserves a sentence that says
// so instead of a number they have to reason about while already rattled.
func remainingNotice(tr i18n.Runtime, n int32) string {
	if n <= 1 {
		return tr.T("login", "recoverLastCode")
	}
	return tr.T("login", "recoverRemaining", i18n.Args{"n": strconv.Itoa(int(n))})
}

func recoverSubmitLabel(tr i18n.Runtime, busy bool) string {
	if busy {
		return tr.T("login", "recoverWorking")
	}
	return tr.T("login", "recoverSubmit")
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
