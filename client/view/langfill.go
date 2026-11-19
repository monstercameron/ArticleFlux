//go:build js && wasm

package view

import (
	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// langFill is where the on-the-fly interface translation (plan.md §22.16c) has
// got to, for the language in force.
//
// Root owns the loop — it is the component that has to re-render when a batch
// lands, because only the Provider it mounts can invalidate the memoised panes
// — and hands this down as plain data, so the language picker can say what is
// true without owning a second loop that would race the first for the same
// keys.
type langFill struct {
	// locale is the language this describes. Empty means English, which has
	// nothing to fill.
	locale string
	// done and total are this BUNDLE's coverage, counted from the catalog the
	// screen actually renders from (langCoverage) rather than from the server's
	// counts: the server's `translated` can include keys this build has since
	// re-worded, and the reader is asking about the words in front of them.
	done, total int
	// running is true while the loop is still asking.
	running bool
	// owner is whether this device asked the server to translate the rest — the
	// difference between "the rest is on its way" and "only an administrator
	// can do that".
	owner bool
	// idle is the server's reason for not filling ("not_owner", "no_key",
	// "unsupported"), once the loop has stopped.
	idle string
	// failed is a loop that ended on a transport error. The strings that had
	// already landed stay; the rest keep rendering in English.
	failed bool
}

// complete reports a language with nothing left in English.
func (f langFill) complete() bool { return f.total > 0 && f.done >= f.total }

// langCoverage counts how much of this build's English a locale has an
// accepted translation for.
func langCoverage(locale string) (done, total int) {
	total = len(i18n.Export(i18n.DefaultLocale))
	if locale == "" || locale == i18n.DefaultLocale {
		return total, total
	}
	return total - len(i18n.Missing(locale)), total
}

// isOwnerRole mirrors the server's owner check (grpcsrv's ownerRoles), for the
// one decision the client makes with it: whether to ask the server to SPEND on
// filling a language. Asking without the role is harmless — the server ignores
// the flag and says "not_owner" — so a stale copy of this list costs a wasted
// flag, never a bill.
//
// "owner" is the demo's word for its one account (client/demodata), which has
// no key either way; it is listed so the demo's language picker reads the
// same as an owner's instance with no key rather than as a member's.
func isOwnerRole(role string) bool {
	switch role {
	case "superadmin", "admin", "owner":
		return true
	}
	return false
}

// percentOf is a share for a chip, rounded DOWN so a language with one string
// left never claims 100, and up from zero so one with a handful done never
// claims none.
func percentOf(done, total int) int {
	if total <= 0 || done <= 0 {
		return 0
	}
	p := done * 100 / total
	if p == 0 {
		return 1
	}
	if p >= 100 && done < total {
		return 99
	}
	return p
}
