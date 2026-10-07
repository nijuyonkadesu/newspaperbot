package telegram

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"newspaperbot/internal/linkpreview"
)

func TestRichTextEscapesSyntaxWithoutHTMLReencoding(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{`it's "quoted"`, `it's "quoted"`},
		{`R&D`, `R\&D`},
		{`<u>text</u>`, `\<u\>text\</u\>`},
		{`\*literal*`, `\\\*literal\*`},
		{`&#39;`, `\&\#39;`},
	} {
		if got := escapeRichText(tc.text); got != tc.want {
			t.Fatalf("%q: got %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestURLPreviewApostrophesAndQuotesStayText(t *testing.T) {
	const description = "For interviews, it's not what you've done, it's how you explain what you've done."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<meta property="og:title" content="A &#34;quoted&#34; title"><meta property="og:description" content="For interviews, it&#39;s not what you&#39;ve done, it&#39;s how you explain what you&#39;ve done.">`))
	}))
	t.Cleanup(server.Close)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	d := h.ready("### Keep this heading\n[site](" + server.URL + ")")
	h.click("preview")
	h.waitPreviews()
	markdown := h.api.live()[0].RichMarkdown
	if strings.Contains(markdown, "&#") || strings.Contains(markdown, `&\#`) ||
		!strings.Contains(markdown, `**[A "quoted" title]`) ||
		!strings.Contains(markdown, strings.TrimSuffix(description, ".")) ||
		!strings.HasPrefix(markdown, d.RichMarkdown()+"\n\n---\n\n") {
		t.Fatal("decoded metadata was re-encoded or native article content changed", markdown)
	}
}
