//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"

	"github.com/monstercameron/ArticleFlux/desktop/host"
)

// fatal reports a startup failure in a plain Win32 message box.
//
// Not a Wails dialog: most of these happen before Wails' event loop is
// running — the database refusing to open is the commonest — and a GUI program
// built with -H windowsgui has no console, so anything written to stderr
// instead would be a window that never appears and a process that exits with
// nobody told why.
//
// The database's own refusal is shown verbatim. store.Migrate's message for a
// database migrated by a newer build — which schema it is at, which this build
// knows, and to run a newer build or restore the pre-migration backup — is
// already the sentence a person needs, and paraphrasing it would lose the
// numbers that make it actionable.
func fatal(err error) {
	title := "ArticleFlux"
	body := err.Error()
	var se *host.StartError
	if errors.As(err, &se) {
		title = se.Title
		body = se.Err.Error()
	}
	fmt.Fprintln(os.Stderr, title+": "+body)
	t, _ := windows.UTF16PtrFromString(title)
	b, _ := windows.UTF16PtrFromString(body)
	_, _ = windows.MessageBox(0, b, t, windows.MB_OK|windows.MB_ICONERROR|windows.MB_SETFOREGROUND)
}
