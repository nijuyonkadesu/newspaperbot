# tgblogbot

A Go Telegram bot for writing blog posts in your private chat, saving drafts in
SQLite, exporting Markdown to the working directory, and optionally publishing
to a channel. Uses `go-telegram/bot` and `mattn/go-sqlite3`; no frontend or database
server is needed.

## Run

Requires Go 1.24.2 or later, CGO enabled, and a C compiler such as GCC.

Create a bot with [@BotFather](https://t.me/BotFather), then set its token and your
numeric Telegram user ID. The ID is a number, not your username. You can find it
in the `message.from.id` field of a private-chat update from
[getUpdates](https://core.telegram.org/bots/api#getupdates), before starting the
bot; keep the token private.

```bash
export BOT_TOKEN='your-bot-token'
export OWNER_ID='your-numeric-user-id'
go run ./cmd/tgblogbot
```

If credentials are already saved in `.env`, load them before starting:

```bash
set -a
. ./.env
set +a
go run ./cmd/tgblogbot
```

The Go executable reads environment variables; it does not load `.env` itself.

Run from this repository directory to use the included fixtures. Relative source
paths and the default `blogbot.db` resolve against the working directory; exports
always go into that directory. Only one process should use this bot/database.

Send `/start` to the bot in your private chat. Authoring commands, text input, and
buttons are restricted to `OWNER_ID` in that private chat; groups, channels,
inline requests, and other users are ignored.

The bot registers command suggestions and a **Menu** button for your DM at
startup. `/start` refreshes them as well. If Telegram still shows an older menu,
reopen the chat after sending `/start`.

## Write a post

Send `/newpost`, then send the whole post in **one message**:

```markdown
My title

A short summary.

## First section
The Markdown body goes here.
```

You can also put `/newpost` on the first line of that same message. A leading
`# ` on the title is optional. The title is the first line, the summary is the
next paragraph, and everything after its blank line is the Markdown body.

The draft is saved immediately and ready to publish. The bot keeps **one current
card per draft**, with **Publish · Preview · Options** in the first row and
**Category · Tags** directly below. Choose one category and toggle multiple tags.
Eight categories fit on one page; 20 tags use three pages of eight choices,
preserving selections across pages. Category defaults to the first available
category and tags to none. Draft cards identify the post with a compact monospace `#<id>`. There is no Done step.

- **Edit the message you sent** to update title, summary, or body. The card updates
  in place, including after restarting the bot.
- **Send more messages** to append to the body, separated by blank lines. Editing
  an appended message changes that addition instead of appending it again.
- **Preview** renders Markdown, the selected category, and every selected tag on the same card. **Back** restores the compact
  overview. Long or unrenderable previews keep the overview with a short notice;
  `/download` explicitly sends the complete `.md` file.
- **Publish** saves Markdown in the working directory and optionally posts to
  your configured channel. Publication status replaces the card, without an
  extra success message.
- `/undo` removes the last appended body message from the saved draft. It keeps
  your Telegram source messages intact.
- `/replace` (also in Options) accepts a whole new post in one message. The old
  post stays saved until the replacement validates. An incomplete replacement can
  be fixed by editing its message; **Keep current post** keeps the original.

**Cancel draft** is available on the initial prompt and under Options; `/cancel`
does the same. It deletes the entire unfinished draft from SQLite and removes
its bot card. `/delete <id>` deletes a specific saved draft; `/delete` deletes
the active draft. Repeated `/newpost` reuses an existing empty draft.
Successful `/delete` and `/cancel` commands receive a 👍 reaction on the command
message. If Telegram rejects the reaction, the bot sends a short deletion
confirmation. `/delete` replies when there is no active or matching saved draft.

`/drafts` lists the latest 20 drafts
and posts; `/resume <id>` selects a saved draft and updates its existing card. Starting
another draft preserves older ones that contain text. Cancellation and deletion
are immediate; deleted drafts cannot be resumed. Prepared/published posts keep
their publication records and cannot be deleted as drafts. Native edits always affect their original
saved draft, even if another draft is active. Every draft's buttons work without
resuming it first. Reply to its source message or current card to append there;
replying with `/preview`, `/publish`, `/undo`, `/download`, `/replace`, `/cancel`,
or `/delete` also targets that draft. Unknown reply targets are rejected rather
than appended elsewhere. Unthreaded messages target the draft last selected by
`/newpost`, `/resume`, or **Replace post**. Published posts remain locked.

Post submissions, appended messages, native edits, and button actions update the
existing draft card in place. A replacement card is created only if the tracked
card is missing or Telegram says it cannot be edited. Preview is remembered per
draft across content changes, replacements, and restarts; Back from the preview
returns to the overview. Temporary Options/Category/Tags panels return to the
chosen mode. Invalid or unrenderable content shows a brief fallback without
forgetting Preview; corrected content renders automatically. If Telegram refuses deletion of an old card, the bot
removes its controls instead. Historical wizard messages whose IDs were never
recorded cannot be located through the Bot API. Telegram does not send deletion
updates for ordinary DMs: deleting your source message does **not** delete its
saved content. Use Undo or Replace for that. See Telegram's
[deletion limits](https://core.telegram.org/bots/api#deletemessage) and
[update types](https://core.telegram.org/bots/api#update).

The selected card and other recent unfinished cards are refreshed on startup,
preserving their views. Old wizard drafts are
normalized without dropping saved fields; unfinished body replacements remain
recoverable under Options. `/done` still works as a compatibility alias for Back,
but it is no longer part of the menu or authoring flow.

Send **Markdown source as ordinary text**. The stored body is the text Telegram
supplies, including code indentation and trailing newlines. Telegram's client
may turn formatting into entities rather than literal Markdown; this bot does
not translate those entities into Markdown.

Free accounts can use the authoring flow and ordinary message editing. Overview
cards use [HTML formatting](https://core.telegram.org/bots/api#formatting-options),
while previews and channel posts use Telegram's
[Rich Markdown API](https://core.telegram.org/bots/api#rich-messages). The bot does
not require Telegram's Premium visual Rich Text Editor. Use an up-to-date client.

The ten design review rounds completed before implementing this redesign are
recorded in [docs/UX_REVIEW.md](docs/UX_REVIEW.md).

## Metadata and output

The default local fixtures are:

| Environment variable | Default source | JSON shape |
| --- | --- | --- |
| `BLOG_NUMBER_SOURCE` | `testdata/blog-number.json` | `{"last_post_number":42}` |
| `CATEGORIES_SOURCE` | `testdata/categories.json` | `["development","personal"]` |
| `TAGS_SOURCE` | `testdata/tags.json` | `["go","telegram","sqlite"]` |
| `DB_PATH` | `blogbot.db` | SQLite file path |

Each metadata source may be a local filename or an HTTP(S) URL returning the same
JSON. Remote requests have a 10-second timeout; each source is limited to 1 MiB.
Category/tag names must be unique, non-empty, trimmed, and contain no commas or
newlines. At least one category is required; an empty tag list is valid.

Categories and tags are read when starting a draft, submitting/editing a full
post message, opening a picker, and publishing. The blog number is read only
when preparing publication. If selected categories/tags have disappeared, choose
current values using the Category and Tags buttons before publishing. A failed metadata fetch leaves the draft intact and does not allocate
a publication number.

### Copyable list and optional footer

Send `/taxonomy` for one reusable list containing every category and tag, each
as inline monospace text on its own line. Pin that bot message yourself. Repeating the command
updates the same message. Once created, the list is checked every 30 seconds and
at startup; source changes edit it in place and preserve its pin. Failed source
reads retain the last valid list. Deleting the list stops background updates once
the next attempted edit detects its absence; `/taxonomy` creates it again.

You can set metadata without opening pickers by putting these optional lines at
the end of the **whole post message**, outside code blocks:

```text
Category: personal
Tags: go, sqlite
```

Use `Tags: -` for none. Names match the catalog exactly; label casing is ignored.
The final two lines also work without labels when the category and all
comma-separated tags match the catalog. Unmatched bare lines stay in the body;
labelled invalid values require correction. Code fences and indented code are
preserved. Recognized footers are removed from the body and saved as metadata,
used by the preview and exported frontmatter. Native edits to that source can
change them later. Appends never reapply old footers over your button choices.
A temporary catalog read failure uses the draft's last saved choices.

The first publication is `43.md` with the unmodified fixtures and no existing
numbered exports. Numbers advance above the source number, SQLite reservations,
and numbered Markdown files already present in the working directory. A
transaction reserves each number; interrupted publications reuse their reserved
number. Files are created atomically, and an existing file with different bytes
is never overwritten.

Exports use **provisional JSON frontmatter** followed by the original Markdown
body. `testdata/sample.md` shows the format. This can be changed to match the real
blog later. Generated posts, SQLite files, and `.env` are ignored by Git.

## Channel publishing

Add the bot to the channel as an administrator with permission to post. You must
also be a member of the channel. Configure it from your DM:

```text
/setchannel @channelname
/setchannel -1001234567890
/unsetchannel
```

Setting a channel replaces the previous destination. Changes apply to future
publications; a post whose number has already been reserved retains its original
content, timestamp, filename, and channel. Published posts are locked against
editing; channel-message editing and repository pushes are not implemented.

The bot publishes title, summary, and content as a rich Markdown message. If the
message exceeds Telegram's rich-message limits or Telegram rejects its rendering,
the bot sends a plain summary followed by the complete `.md` document.

File export and channel delivery are recorded independently. A rejected channel
send can be retried with `/publish`; confirmed message IDs prevent resending
completed parts. If a crash or network failure leaves delivery uncertain, the
bot stops automatic retries and asks you to inspect the channel before choosing
**I checked the channel — retry**. That explicit retry may duplicate an
unconfirmed message. Telegram does not provide an idempotency key for these sends.

To recover an interrupted export, `/resume <id>` followed by `/publish` continues
it. If another file appeared at the reserved path with different content, move
that conflicting file yourself before retrying; the bot will not replace it.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Tests use temporary databases/directories and in-memory HTTP transports for
Telegram and metadata, so they require no listening sockets or external network.
They do not send real messages. A live smoke test requires your bot token: use your configured owner
account to create, cancel/delete, resume saved drafts, edit source messages, preview, and publish a draft,
then test the configured channel.

The packages separate Telegram interaction (`internal/telegram`), draft and
Markdown behavior (`internal/post`), persistence (`internal/store`), and source
loading (`internal/metadata`). `cmd/tgblogbot` wires them together. File writing is
injected into the application so a repository publisher can replace it later.
