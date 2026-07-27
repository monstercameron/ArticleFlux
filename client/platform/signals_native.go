//go:build !(js && wasm)

// The native half of the signals observers. Every one is a no-op returning a
// zero value, for the reason given in platform_native.go: a native build has no
// DOM, and faking one would produce tests that pass against a fiction.
//
// NowMS is the exception and returns a real clock, because the outbox uses it to
// order and expire events and a monotonically zero clock would make every event
// look simultaneous — which is a fiction that would break logic rather than one
// that merely fails to exercise it.

package platform

import "time"

func NowMS() int64 { return time.Now().UnixMilli() }

const IdleAfterMS = 60_000

const RereadPx = 220

const MinSelectionChars = 12

func OnAttention(fn func(attentive bool)) Listener { return Listener{} }

func OnBackScroll(rootSelector, matchSelector string, fn func()) Listener { return Listener{} }

func OnTextSelection(fn func(chars int)) Listener { return Listener{} }

func OnPageHide(fn func()) Listener { return Listener{} }

func VisibleAttrs(rootSelector, attr string) []string { return nil }
