// Package envfile loads a `.env` file into the process environment.
//
// It exists because `.env.example` has been telling people to "copy to .env and
// fill in" since Tier 1, and nothing ever read the resulting file — the only
// consumer, `internal/tts`, reads `OPENAI_API_KEY` straight from the process
// environment. So the instruction was true only for someone who already knew to
// export it by hand, which is nobody following the instruction.
//
// Deliberately not a dependency. The format is four rules and the whole parser
// is eighty lines; pulling in a library to read `KEY=VALUE` would add a supply
// chain to a project that has been careful about having almost none.
//
// **The real environment always wins.** A variable already set is never
// overwritten by the file. That ordering is what keeps `.env` a development
// convenience rather than a production hazard: systemd sets its own environment
// through `EnvironmentFile=`, and a stale `.env` left in a deployment directory
// must not be able to override it.
package envfile

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
)

// Load reads path and sets any variable not already present in the environment.
//
// A missing file is not an error — the overwhelmingly common case is that there
// is no `.env`, and a server that refused to start without one would be a server
// that requires a file the documentation calls optional. Every other failure is
// reported: an unreadable or malformed `.env` is a configuration someone wrote
// on purpose and is entitled to be told about.
//
// The returned slice names the keys that were actually applied, so the caller
// can log what changed. It never contains a value — a log line that helpfully
// echoes an API key is how a secret ends up in a journal.
func Load(path string) (applied []string, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parse(f)
}

func parse(r io.Reader) ([]string, error) {
	var applied []string
	sc := bufio.NewScanner(r)
	// 1 MiB. The default 64 KiB is enough for any real .env, but a long
	// single-line value — a PEM, a base64 key — would otherwise fail as a scan
	// error rather than as a line nobody could see was too long.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// `export FOO=bar` is accepted because people paste shell lines in.
		line = strings.TrimPrefix(line, "export ")

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			// A line with no `=` is a typo, not a variable. Skipping it silently
			// is how a misspelled key becomes an afternoon.
			return applied, errors.New("envfile: line is not KEY=VALUE: " + truncate(line))
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return applied, errors.New("envfile: empty key in: " + truncate(line))
		}

		val = unquote(strings.TrimSpace(val))

		// Set only what is absent. See the package comment: the real environment
		// is the authority, and this file is a default.
		if _, present := os.LookupEnv(key); present {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return applied, err
		}
		applied = append(applied, key)
	}
	return applied, sc.Err()
}

// unquote strips one matched pair of surrounding quotes.
//
// Only single and double, only when they match, and no escape processing — the
// values here are passwords and API keys, and a parser that quietly rewrote a
// backslash inside one would corrupt exactly the values that must arrive
// verbatim. An UNQUOTED value keeps a trailing `# comment` too, for the same
// reason: `PASSWORD=hunter2 # temp` has a password of `hunter2 # temp`, and
// guessing otherwise silently changes a credential.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func truncate(s string) string {
	// Bounded, because this string goes into an error that goes into a log, and
	// the line it came from may be a secret somebody mistyped.
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}
