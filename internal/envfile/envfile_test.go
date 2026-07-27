package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSetsAbsentKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(strings.Join([]string{
		"# a comment",
		"",
		"ARTICLEFLUX_TEST_PLAIN=value",
		"  ARTICLEFLUX_TEST_SPACED  =  spaced  ",
		`ARTICLEFLUX_TEST_DQUOTE="double quoted"`,
		"ARTICLEFLUX_TEST_SQUOTE='single quoted'",
		"export ARTICLEFLUX_TEST_EXPORT=exported",
		"ARTICLEFLUX_TEST_EMPTY=",
		"ARTICLEFLUX_TEST_EQUALS=a=b=c",
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"ARTICLEFLUX_TEST_PLAIN", "ARTICLEFLUX_TEST_SPACED", "ARTICLEFLUX_TEST_DQUOTE",
		"ARTICLEFLUX_TEST_SQUOTE", "ARTICLEFLUX_TEST_EXPORT", "ARTICLEFLUX_TEST_EMPTY",
		"ARTICLEFLUX_TEST_EQUALS",
	} {
		t.Setenv(k, "") // registers cleanup
		os.Unsetenv(k)
	}

	applied, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(applied) != 7 {
		t.Errorf("applied %d keys, want 7: %v", len(applied), applied)
	}

	for k, want := range map[string]string{
		"ARTICLEFLUX_TEST_PLAIN":  "value",
		"ARTICLEFLUX_TEST_SPACED": "spaced",
		"ARTICLEFLUX_TEST_DQUOTE": "double quoted",
		"ARTICLEFLUX_TEST_SQUOTE": "single quoted",
		"ARTICLEFLUX_TEST_EXPORT": "exported",
		"ARTICLEFLUX_TEST_EMPTY":  "",
		// Only the FIRST `=` separates. A password containing one is a password,
		// not a parse error.
		"ARTICLEFLUX_TEST_EQUALS": "a=b=c",
	} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// The property the whole design rests on: a stale .env in a deployment directory
// must never override what systemd set.
func TestRealEnvironmentWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path,
		[]byte("ARTICLEFLUX_TEST_WINS=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTICLEFLUX_TEST_WINS", "from-environment")

	applied, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("ARTICLEFLUX_TEST_WINS"); got != "from-environment" {
		t.Errorf("the file overwrote the environment: got %q", got)
	}
	for _, k := range applied {
		if k == "ARTICLEFLUX_TEST_WINS" {
			t.Error("an already-set key was reported as applied")
		}
	}
}

// A missing file is the common case and must not be an error, or a server would
// require a file its own documentation calls optional.
func TestMissingFileIsNotAnError(t *testing.T) {
	applied, err := Load(filepath.Join(t.TempDir(), "nope.env"))
	if err != nil {
		t.Errorf("missing .env returned %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("applied %v from a file that does not exist", applied)
	}
}

func TestMalformedLineIsReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path,
		[]byte("GOOD=1\nthis line has no equals sign\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("a line with no '=' was accepted; a misspelled key must not be silent")
	}
}

// Values are taken verbatim. A parser that stripped a trailing `# ...` would
// silently change a password that happens to contain one.
func TestValuesAreVerbatim(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path,
		[]byte("ARTICLEFLUX_TEST_HASH=hunter2 # not a comment\n"+
			`ARTICLEFLUX_TEST_BACKSLASH=a\nb`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("ARTICLEFLUX_TEST_HASH")
	os.Unsetenv("ARTICLEFLUX_TEST_BACKSLASH")
	t.Setenv("ARTICLEFLUX_TEST_HASH", "")
	t.Setenv("ARTICLEFLUX_TEST_BACKSLASH", "")
	os.Unsetenv("ARTICLEFLUX_TEST_HASH")
	os.Unsetenv("ARTICLEFLUX_TEST_BACKSLASH")

	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("ARTICLEFLUX_TEST_HASH"); got != "hunter2 # not a comment" {
		t.Errorf("hash value = %q; an unquoted value keeps everything after the '='", got)
	}
	if got := os.Getenv("ARTICLEFLUX_TEST_BACKSLASH"); got != `a\nb` {
		t.Errorf("backslash value = %q; no escape processing", got)
	}
}
