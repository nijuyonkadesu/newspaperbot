package telegram

import "testing"

func TestChannelPublicationKeepsRichMarkdownWithoutKeyboard(t *testing.T) {
	const body = "### Named colors\nLarksurp boquet, King lime.\n\nhttps://names.color.pizza/"
	const want = "# Title\n\nSummary\n\n" + body
	h := newHarness(t)
	h.send("/setchannel @first")
	h.ready(body)
	h.click("preview")
	h.send("/publish")
	d := h.active()
	if !d.Exported || d.Delivery != "sent" || d.DocumentMode || d.SummaryMessageID != 0 || d.ContentMessageID == 0 {
		t.Fatalf("rich publication became a summary/document delivery: %+v", d)
	}
	if h.api.count("sendMessage", true) != 0 || h.api.count("sendDocument", true) != 0 || h.api.count("sendRichMessage", true) != 1 {
		t.Fatal("publication sent a plain message or attachment instead of one rich message")
	}
	for _, call := range h.api.snapshot() {
		if call.ChatID == d.ChannelID && call.Method == "sendRichMessage" && (call.RichMarkdown != want || call.Text != "" || call.ParseMode != "") {
			t.Fatalf("channel lost its Markdown structure: %+v", call)
		}
	}
}

func TestRejectedRichChannelPostStaysPendingWithoutAttachments(t *testing.T) {
	h := newHarness(t)
	h.send("/setchannel @first")
	h.ready("### Section\n**Body**")
	h.api.fail("sendRichMessage", 400)
	h.send("/publish")
	d := h.active()
	if !d.Exported || d.Delivery != "pending" || d.DocumentMode || d.ContentMessageID != 0 || d.Notice == "" {
		t.Fatalf("rejection lost its retryable rich delivery: %+v", d)
	}
	if h.api.count("sendMessage", true) != 0 || h.api.count("sendDocument", true) != 0 {
		t.Fatal("a rejected rich post silently became a plain summary or attachment")
	}
	h.restart()
	h.api.fail("sendRichMessage", 0)
	h.send("/publish")
	if d := h.active(); d.Delivery != "sent" || d.DocumentMode || d.ContentMessageID == 0 {
		t.Fatalf("retry did not finish the original rich publication: %+v", d)
	}
	if h.api.count("sendRichMessage", true) != 2 || h.api.count("sendMessage", true) != 0 || h.api.count("sendDocument", true) != 0 {
		t.Fatal("retry changed the publication format or duplicated a successful send")
	}
}
