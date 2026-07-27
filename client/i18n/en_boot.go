package i18n

// The splash screen's copy — the four strings in web/index.html that are shown
// BEFORE the wasm module exists.
//
// They cannot be read from this catalog at the moment they are needed: the
// bootstrap runs before Go does. So they are mirrored into localStorage by the
// running application whenever the language changes, exactly the way
// client/view/theme.go already mirrors the splash's colours — and for the same
// reason. Left alone, a reader running the interface in French would get an
// English splash on every single load, which is the one flash a splash screen
// exists to prevent.
//
// Keep them SHORT and free of markup: the shim assigns them with textContent,
// and they are pipe-separated in one storage value.
func init() {
	text(DefaultLocale, "boot", map[string]string{
		"loading": "Loading…",
		"help":    "Reload to try again. If it keeps failing, check that the server is running.",
		// {err} is the browser's own message. Appended rather than paraphrased,
		// for the reason every other {err} in this catalog is.
		"failed": "Couldn't start the reader: {err}",
		// Download progress, shown only when the load is slow enough to be worth
		// explaining. {got} and {total} arrive already formatted as "4.1 MB".
		"progress":   "{got} of {total}",
		"downloaded": "{got} downloaded",
	})
}
