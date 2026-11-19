// Package host is the desktop shell's server half: where the data lives, who
// may open it, which port it answers on, and the gate in front of the reader.
//
// It imports no window toolkit. Everything here is testable with `go test`
// on a machine with no display, which is the point of the split — the part
// with logic in it is the part a test can reach, and cmd/articleflux-desktop
// is left holding only the wiring between this and Wails.
package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppDirName is the folder under %LOCALAPPDATA%.
const AppDirName = "ArticleFlux"

// DBName is the database's file name inside the data directory. The same
// name the server uses by default, so a desktop data directory is also a
// directory `articleflux serve -db` can be pointed at.
const DBName = "articleflux.db"

// ResolveDataDir decides where the reader's data lives.
//
// %LOCALAPPDATA%, not %APPDATA%. Roaming profiles copy %APPDATA% to every
// machine the person signs in to, and this directory holds a SQLite database
// with a live WAL plus several hundred megabytes of caches — none of which
// should be synchronised file-by-file by a profile service that knows nothing
// about either.
//
// The environment variable is read first and os.UserCacheDir second (which on
// Windows is the same folder) so a test can point the whole shell at a
// temporary directory by setting one variable, and never touch the real one.
// An explicit override wins over both: `-data` exists for a person who keeps
// their reading on another drive.
func ResolveDataDir(override string) (string, error) {
	if s := strings.TrimSpace(override); s != "" {
		abs, err := filepath.Abs(s)
		if err != nil {
			return "", fmt.Errorf("data directory %q: %w", s, err)
		}
		return abs, nil
	}
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		var err error
		if base, err = os.UserCacheDir(); err != nil || base == "" {
			return "", errors.New("cannot find a per-user data folder: %LOCALAPPDATA% is not set; pass -data <dir>")
		}
	}
	return filepath.Join(base, AppDirName), nil
}

// Paths are the files the shell itself names. Everything else in the data
// directory — secrets.key, proxy.key, speech.key, the *-cache directories, the
// log spill — is written by internal/app beside the database, which is why
// pointing DBPath here is enough to put all of it in the profile.
type Paths struct {
	Dir     string
	DB      string
	Lock    string
	WebView string
}

// PathsIn lays the data directory out.
func PathsIn(dir string) Paths {
	return Paths{
		Dir:  dir,
		DB:   filepath.Join(dir, DBName),
		Lock: filepath.Join(dir, "desktop.lock"),
		// WebView2's own profile — cookies, the Service Worker's cache, the
		// client's per-device localStorage. It is not a second store of ours:
		// it is the browser's, and it lives here rather than under the
		// executable's name in %APPDATA% so that deleting this one folder
		// really does reset the app.
		WebView: filepath.Join(dir, "webview2"),
	}
}

// Ensure creates the data directory if this is the first run.
func (p Paths) Ensure() error {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("creating the data directory %q: %w", p.Dir, err)
	}
	return nil
}
