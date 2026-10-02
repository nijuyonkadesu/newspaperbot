![A blog draft being previewed in Telegram](docs/telegram-preview.jpg)

**Your bot DM.** Buttons look like <kbd>Preview</kbd>. Bot messages below are excerpts.

**Draft → publish**

<table>
<tr><th>You send / tap</th><th>Bot · one card, updated in place</th></tr>
<tr>
<td>
<pre>/newpost A useful trick&#10;&#10;A short summary.&#10;&#10;## The idea&#10;Keep it simple.&#10;&#10;concept&#10;go</pre>
</td>
<td>
<blockquote>
<strong>A useful trick</strong><br>
A short summary.
<blockquote>## The idea<br>Keep it simple.</blockquote>
<code>#1</code> · <em>concept · go</em>
</blockquote>
<kbd>Publish</kbd> <kbd>Preview</kbd> <kbd>Options</kbd><br>
<kbd>Category</kbd> <kbd>Tags</kbd>
</td>
</tr>
<tr>
<td>Tap <kbd>Preview</kbd></td>
<td>
<blockquote>
<h3>A useful trick</h3>
A short summary.
<h4>The idea</h4>
Keep it simple.
<hr>
<code>#1</code><br>
<strong>Category:</strong> concept<br>
<strong>Tags:</strong> go
</blockquote>
<kbd>Publish</kbd> <kbd>Back</kbd> <kbd>Options</kbd><br>
<kbd>Category</kbd> <kbd>Tags</kbd>
</td>
</tr>
<tr>
<td>Edit your original message:<br><code>Keep it simple.</code> → <code>Keep it readable.</code></td>
<td>
<blockquote>
<h3>A useful trick</h3>
A short summary.
<h4>The idea</h4>
Keep it readable.
<hr>
<code>#1</code><br>
<strong>Category:</strong> concept<br>
<strong>Tags:</strong> go
</blockquote>
<em>Same card. Preview stays on.</em>
</td>
</tr>
<tr>
<td>Tap <kbd>Publish</kbd><br>or send <code>/publish</code></td>
<td>
<blockquote>
<strong>Live</strong> · <code>#269</code> · Today · 2026-10-02<br><br>
<strong>A useful trick</strong><br>
A short summary.<br><br>
Committed to main<br>
Destination · sent
</blockquote>
<kbd>Edit</kbd> <kbd>Preview</kbd> <kbd>Download</kbd>
</td>
</tr>
</table>

Put these optional lines **at the end of your post**:

```text
Category: concept
Tags: go, my-new-tag
```

That selects **one category and two tags**. This format works for existing and
new names. `Tags: -` means no tags.

When you tap <kbd>Publish</kbd>, the bot:

1. Assigns the article number and date.
2. Commits the post and updated category/tag lists to Git.
3. Sends the post to your configured channel/group, if any.

**Published article → edit → save**

<table>
<tr><th>You send / tap</th><th>Bot / chat change</th></tr>
<tr>
<td><code>/posts</code></td>
<td>
<blockquote>
<strong>Live articles</strong> · <code>#269–#268</code><br><br>
<strong>Today</strong><br><br>
<code>#269</code> · 2026-10-02<br>
<strong>A useful trick</strong><br><br>
<strong>Earlier</strong><br><br>
<code>#268</code> · 2024-07-25<br>
<strong>An older post</strong>
</blockquote>
<kbd>Preview #269</kbd> <kbd>Edit #269</kbd><br>
<kbd>Preview #268</kbd> <kbd>Edit #268</kbd><br>
<kbd>Refresh</kbd>
</td>
</tr>
<tr>
<td>Tap <kbd>Edit #269</kbd><br>or send <code>/edit 269</code></td>
<td>
<blockquote>
<strong>Editing live</strong> · <code>#269</code> · Today · 2026-10-02<br><br>
<strong>A useful trick</strong><br>
A short summary.
<blockquote>## The idea<br>Keep it readable.</blockquote>
<em>concept · go</em>
</blockquote>
<kbd>Save changes</kbd> <kbd>Preview</kbd> <kbd>Options</kbd><br>
<kbd>Category</kbd> <kbd>Tags</kbd><br>
<kbd>Original message</kbd> <kbd>Discard changes</kbd>
</td>
</tr>
<tr>
<td>
Reply to the edit card:
<pre>One more detail.&#10;&#10;Category: personal&#10;Tags: go, my-new-tag</pre>
Then tap <kbd>Preview</kbd>.
</td>
<td>
<blockquote>
<h3>A useful trick</h3>
A short summary.
<h4>The idea</h4>
Keep it readable.<br><br>
One more detail.
<hr>
<strong>Editing live</strong> · <code>#269</code> · Today · 2026-10-02<br>
<strong>Category:</strong> personal<br>
<strong>Tags:</strong> go, my-new-tag*
</blockquote>
<kbd>Save changes</kbd> <kbd>Back</kbd> <kbd>Options</kbd><br>
<kbd>Category</kbd> <kbd>Tags</kbd><br>
<kbd>Discard changes</kbd>
</td>
</tr>
<tr>
<td>Tap <kbd>Save changes</kbd><br>or send <code>/save</code></td>
<td>
<blockquote>
<strong>Live</strong> · <code>#269</code> · Today · 2026-10-02<br><br>
<strong>A useful trick</strong><br>
A short summary.<br><br>
<strong>Status</strong> · Repository updated · channel updated
</blockquote>
<em>Same article number, date, and URL. Git and linked channel messages updated.</em>
</td>
</tr>
<tr>
<td>While editing: <kbd>Discard changes</kbd><br>or <code>/cancel</code></td>
<td>Pending edits discarded; published version kept.</td>
</tr>
</table>

Publish/Save adds new names to the category/tag lists; `*` is never stored.

**More chat examples**

| You send / tap | Bot / chat change |
| --- | --- |
| Reply to a card: `One more detail.`<br><br>`Category: personal`<br>`Tags: go` | Adds `One more detail.` to the body. Sets category to `personal` and tags to `go`; the two footer lines are removed from the body. |
| Reply with just `personal` | Adds the word `personal` to the body; category stays unchanged. |
| <kbd>Original message</kbd>, if shown → tap the quoted message | Opens the message you originally sent. Edit it to update this post's card. |
| `/edit 268` for an older article | **Editing live** · `#268` · Earlier · 2024-07-25<br>Articles have no editing deadline. |
| Reply to the `/posts` output with `268` | List jumps to `#268`; your reply disappears. |
| <kbd>Refresh</kbd> / `/posts` again | Reload current range / replace list with newest articles. |
| <kbd>Preview #268</kbd> → <kbd>Back</kbd> | Read article → return to list; no edit started. |
| `/newpost` while draft `#1` has text | `#2`<br>Send title, summary, and Markdown body in **one message**.<br><kbd>Cancel draft</kbd><br><br>Draft `#1` stays saved; reply to either card to append there. |
| `/drafts` | **Saved drafts** · `/resume <number>`<br>`1 · A useful trick`<br>`2 · Untitled` |
| `/resume 1`, including after a restart | Reopens saved draft `#1`.<br>**A useful trick**<br>`#1` · *concept · go*<br><kbd>Publish</kbd> <kbd>Preview</kbd><br><br>Further unthreaded text goes here. |
| `/edit 269` after a restart | Resumes saved, unfinished edits to article `#269`. Article edits aren't listed in `/drafts`. |
| `/replace` | `#1`<br>Send title, summary, and Markdown body in **one message**.<br><kbd>Keep current post</kbd> <kbd>Cancel draft</kbd><br><br>Send a valid replacement to update the card. |
| `/undo` after appending `One more detail.` | Addition leaves the card; its chat message stays. |
| Reply to that addition: `/remove` | Addition removed; source and command cleared where allowed. |
| **Review** → <kbd>Source 1</kbd> → reply `/remove` | Open the failing source, then remove it. Deleted source: `/remove 42`. |
| `/cancel` on draft `#1` / `/delete 2` | Delete the entire selected draft / delete draft `#2`. |
| `/help` | Usage and command list. |

**Download a draft or article**

Reply with `/download` to a post's own card to get **that post's Markdown**.
Downloads include your pending article edits.

| You send / tap | Bot sends |
| --- | --- |
| `/resume 1` → `/download` | `draft-1.md` — saved draft `#1`. |
| `/edit 269` → change the text → `/download` | `269-a-useful-trick.md` — includes edits you haven't saved to Git yet. |
| Reply to draft `#2`'s card: `/download` | `draft-2.md`, even while you're working on draft `#1`. |
| On a draft/edit card: <kbd>Options</kbd> → <kbd>Download</kbd> | That card's Markdown file. Published cards have <kbd>Download</kbd> directly. |
| `/posts` → <kbd>Preview #269</kbd> → <kbd>Edit #269</kbd> → `/download` | Article `#269`'s Markdown file. |

Plain `/download` uses the post selected with `/newpost`, `/resume`, `/edit`,
or <kbd>Replace post</kbd>. Previewing another post doesn't switch that selection.

**Taxonomy and destination**

| You send | Bot |
| --- | --- |
| `/taxonomy` | **Categories**<br><br>**1.** `concept`<br>`go`<br><br>**2.** `personal`<br>`go` · `my-new-tag`<br><br>**Post footer**<br>`Category: concept`<br>`Tags: go` |
| `/setchannel @my_blog` | Publishing destination set to My Blog. |
| `/channels` | **Channels**<br>**My Blog** · @my_blog · `-1001234567890` |
| `/unsetchannel` | Publishing destination cleared for future posts. |

Pin `/taxonomy`; the bot updates that message as names change. Tag groups are
hints, not restrictions. Setting a destination requires bot admin access and
your membership; it applies to future publications.

> **Warning:** Media attachments and their captions are currently ignored.
> They aren't included in previews, Markdown files, or published posts.
