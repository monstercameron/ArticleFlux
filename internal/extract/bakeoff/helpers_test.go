package bakeoff

import (
	"io"
	"net/url"

	"golang.org/x/net/html"
)

func mustParse(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	return u
}

func renderNode(w io.Writer, n *html.Node) error { return html.Render(w, n) }
