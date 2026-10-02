package telegram

import (
	"strings"
	"testing"
)

// This literal fixture pins the rich Markdown from the original draft layout.
// It deliberately does not obtain the expected result from previewMarkdown.
func TestDraftPreviewRetainsOriginalRichMarkdown(t *testing.T) {
	const source = `Just how superfluous microslop azure updates are

Imagine using microslop winhoes. being treated as a 3rd class citizen is nothing new when it comes to microslop

Link to doc: check it bro ok amazing!!!
### Special treatment
Go install linux

normal content in another paragraph. So, help yourself by installing [linux](https://linux.com). Hi kebin

development
go`
	const want = `# Just how superfluous microslop azure updates are

Imagine using microslop winhoes. being treated as a 3rd class citizen is nothing new when it comes to microslop

Link to doc: check it bro ok amazing!!!
### Special treatment
Go install linux

normal content in another paragraph. So, help yourself by installing [linux](https://linux.com). Hi kebin

---

` + "`#1`" + `

**Category:** development

**Tags:** go`
	h := newHarness(t)
	h.send("/newpost")
	h.send(source)
	cardID := h.active().CardID
	h.click("preview")
	assertRich := func(expected string) {
		t.Helper()
		calls := h.api.snapshot()
		call := calls[len(calls)-1]
		if call.Method != "editMessageText" || call.MessageID != cardID || call.RichMarkdown != expected || call.Text != "" || call.ParseMode != "" {
			t.Fatalf("draft rich preview changed: method=%s text=%q parse_mode=%q markdown=%q", call.Method, call.Text, call.ParseMode, call.RichMarkdown)
		}
		if d := h.active(); d.CardID != cardID || !d.Preview || d.View != "preview" || d.Notice != "" || d.Category != "development" || strings.Join(d.Tags, ",") != "go" {
			t.Fatal("rich preview changed its card, preference, or taxonomy")
		}
	}
	assertRich(want)
	h.send("One more paragraph.")
	assertRich(strings.Replace(want, "\n\n---\n\n", "\n\nOne more paragraph.\n\n---\n\n", 1))
	h.restart()
	if err := h.app.RestoreCard(t.Context(), h.bot); err != nil {
		t.Fatal(err)
	}
	assertRich(strings.Replace(want, "\n\n---\n\n", "\n\nOne more paragraph.\n\n---\n\n", 1))
}

func TestUnsupportedRichPreviewRetainsOriginalFailureBehavior(t *testing.T) {
	h := newHarness(t)
	d := h.ready("### Special treatment\nGo install [linux](https://linux.com)")
	h.api.richUnavailable = true
	h.click("preview")
	got := h.active()
	message := h.api.messages[d.CardID]
	if got.CardID != d.CardID || len(h.api.live()) != 1 || !got.Preview || got.View != "preview" || got.Content != d.Content || !strings.Contains(got.Notice, "/download") || !strings.Contains(message.Text, "Telegram could not render this Markdown") {
		t.Fatal("rejected rich message was silently presented as a successful rendered preview")
	}
	if !strings.Contains(message.Text, "### Special treatment") || strings.Contains(message.Text, `<a href="https://linux.com">`) {
		t.Fatal("draft rich preview was replaced by the removed HTML renderer")
	}
}
