# Composer redesign — reviewed before implementation

The old wizard required five field submissions, a Done action, and repeated
acknowledgments. Its seven vertically stacked review buttons obscured the post.
The replacement must make a complete draft possible with one post message.

## Ten design review rounds

1. **Minimum effort.** Walked through a short post from an empty chat. Rejected
   a shorter wizard: it still requires several submissions. Final path:
   `/newpost`, one message containing title / summary / body, then Publish.
   `/newpost` can include that message inline. Category defaults visibly to the
   first available category; tags default to none. Neither blocks composition.
2. **Mobile clutter.** Walked through the supplied screenshots and a post with
   several additional paragraphs. Editing a card above the newest source would
   leave its buttons off screen. On new source messages, move the logical card
   below the source and delete the previous bot card after persisting its new ID.
   Native edits and button actions edit that card in place. No saved/Dones spam.
   Ready controls are one row: Publish, Preview, Options.
3. **Input ambiguity.** Tested headings, multiple paragraphs, code, blank lines,
   and missing fields. Use the first line for title (optional leading `# `), the
   next paragraph for summary, and the remainder for Markdown body. Blank lines
   separate summary from body. Keep the body verbatim. Reject incomplete full
   replacements without changing the prior content; show the error on the card.
4. **Native editing.** Traced editing the original post, editing an appended
   paragraph, and replaying those updates after restart. Persist source message
   IDs, text, and update IDs with the draft. Rebuild from those sources when an
   `edited_message` arrives. Associate an edit with its original draft, never
   whichever draft happens to be active. Invalid edits block publication until
   fixed while retaining the last valid content. No extra Edit-field buttons.
5. **Large posts and formatting.** Tested a long summary, HTML characters,
   Unicode, and a body over Telegram's rich-message limit. The normal card is a
   bounded HTML overview, with bold title and a quoted body excerpt. Preview
   replaces it with rendered rich Markdown. Rendering rejection or excessive
   size returns to the same card with a notice. `/download` explicitly sends the
   complete Markdown file; typing never triggers unsolicited attachments.
6. **Deletion and undo.** Checked the official API. Bots may delete their DM
   messages within Telegram's deletion limits, but ordinary DMs have no deletion
   updates. Keep the owner's source messages editable. Remove only tracked bot
   cards, best effort; never guess history IDs. Undo removes the last appended
   body chunk from the saved draft. Replacement is available when Telegram no
   longer allows editing the source. Deleting a source in Telegram does not
   silently discard its saved content.
7. **Existing drafts and restarts.** Traced the actual database's draft, paused
   drafts, old incomplete wizard states, and pending replacement content. Preserve
   existing title/summary/body/taxonomy/publication data. Normalize old wizard
   states into the composer; keep unfinished replacement text recoverable.
   Store the card ID/view so a restart edits the same card. Recreate a card only
   when Telegram definitively reports that it is missing or cannot be edited.
8. **Accidental actions and access.** Traced old buttons, two Publish taps,
   edits from another user/group, and changed metadata. Require owner DM access
   for every message, edit, and callback. Bind buttons to draft ID, saved revision,
   and current card ID. Stale clicks refresh the card and use a callback toast,
   rather than adding a chat message. Revalidate taxonomy before reserving a post.
9. **Failure recovery.** Walked through successful save followed by failed UI
   update, deletion rejection, disk failure, and uncertain channel delivery.
   Persist content before touching UI or removing old cards. Cleanup failures
   cannot undo a save. Keep publication reservations and explicit uncertain-send
   retry behavior. Publication progress and recoverable errors update the card;
   do not announce success while delivery is unresolved.
10. **Maintainability and final walkthrough.** Re-ran the empty-chat, long-post,
    native-edit, paused-draft, replacement, taxonomy, and publish traces against
    the revised design. Remove the sequential wizard rather than maintaining two
    flows. Use a small source parser in `post`, JSON persistence in `store`, and
    Telegram card/panel rendering in `telegram`; retain the two existing packages.
    Required verification: one-message draft, one live card, in-place edit,
    append/edit/undo, stale callbacks, restart, legacy recovery, missing card,
    invalid replacement, owner restriction, and publication failure recovery.

## Final interaction contract

```text
/newpost

My title

A short summary.

## First section
Markdown body goes here.
```

The bot keeps one current card per draft. Send additional messages to append to
the body; edit any tracked source message to correct its text. No Done step.
Publish writes the local Markdown file and optionally sends it to the configured
channel. Options contains category, tags, full replacement, and append undo;
taxonomy choices use compact, paginated buttons. `/replace` opens replacement
instructions on the same card; cancelling replacement preserves the draft.

`/drafts` and `/resume <id>` retain access to older drafts. Explicit help, draft
lists, channel configuration responses, and requested downloads may add messages;
routine drafting and corrections keep a single bot card. Historical wizard
messages with unrecorded IDs cannot be discovered or swept through the Bot API.

### Cancellation correction — October 1, 2026

The owner's clarification supersedes the earlier pause behavior: **Cancel draft**
and `/cancel` delete the active unfinished draft and remove its card. `/delete <id>`
deletes a chosen saved draft; `/delete` uses the active draft. Both operations
are immediate. Deleted drafts cannot be resumed. Starting another draft keeps
existing drafts that contain text; repeating `/newpost` reuses an empty draft.
In replacement mode, **Keep current post** abandons only that replacement.

Deletion clears the active selection in the same SQLite transaction as the row
removal. Publication reservations and exported posts remain protected. Tests
cover empty/ready/replacement cancellation, repeated creation/cancellation,
inactive deletion, repeated deletion, stale callbacks, native edits after a
restart, owner authorization, and Telegram refusing to delete an old card.

Deletion feedback now acknowledges successful `/delete` and `/cancel` commands
with a 👍 reaction on the owner's command message. If reactions are unavailable,
a brief confirmation is sent. `/delete` reports missing/absent drafts explicitly.
Tests verify the reaction target and emoji, rejection fallback, and failure
feedback without a false success reaction. Telegram's
[reaction method](https://core.telegram.org/bots/api#setmessagereaction) supports
standard emoji reactions by bots.

### Concurrent drafts and taxonomy input — October 1, 2026

The extended interaction was reviewed in ten passes before implementing the
taxonomy list and footer parser:

1. Direct Category/Tags controls belong on ready and preview cards; Options keeps
   replacement, downloads, undo, and cancellation.
2. Preview must include the selected category and every selected tag, safely
   escaped, without changing exported/channel content.
3. Eight choices per page fits 8 categories on one page and 20 tags on three;
   selecting tags across pages must preserve all earlier selections.
4. Draft buttons and native source edits target their own draft ID, independent
   of the default destination for unthreaded messages.
5. Replies to a current draft card or source append to that draft. Unknown replies
   must not silently write into another draft. Replace explicitly selects its
   target for the next unthreaded replacement message.
6. `/taxonomy` reuses one tracked message. Polling edits it only when its source
   changes; a missing/deleted list must not cause background message spam.
7. Footer recognition requires both category and tags. Labelled invalid values
   yield a clear correction; unmatched bare lines and code samples stay body text.
8. A recognized footer is removed from the body and stored as metadata. Appending
   or editing other body chunks must not reapply an old footer over button choices.
9. Source errors preserve saved drafts and the last valid pinned list. The polling
   task must never write draft data concurrently with owner edits.
10. Restart, stale buttons, reply targeting, independent publication, 8/20
    pagination, parsed preview, list changes, and unknown/fenced footers require
    integration coverage. Existing publication locks and deletion semantics stay.

Verification for this extension: 53 test functions pass, including native edits
and replies across two open drafts, independent publication and error feedback,
all 20 selected tags across three pages, source changes and read failures,
concurrent taxonomy refreshes, pinned-message ID preservation across restart,
deleted list recovery, explicit/bare/invalid/CRLF/code footers, and prevention of
old-footer reapplication. Race detection, vet, and the executable build pass.

Deployment on October 1, 2026 backed up SQLite, restarted the bot, and preserved
both saved drafts' content, source bindings, metadata, and publication state.
Live Telegram reads confirmed `/taxonomy` in the owner's command menu and the
commands Menu button. The list is created only when the owner requests it.

### Stable cards and persistent preview — October 1, 2026

The owner requested in-place updates for all content messages and a remembered
Preview mode. This supersedes the original review's decision to move cards below
new input. Rendering no longer has a move/resend option. Existing cards are
edited; missing/uneditable cards retain their existing recovery behavior.

Preview is a saved preference per draft, separate from temporary panel views.
Appends, native edits, undo, replacements, and restart retain it. Back from
Preview explicitly disables it. Rendering/validation failures show an overview
with feedback while retaining the preference, so corrected content restores the
preview without another click. Existing drafts saved in Preview are migrated
when decoded. Integration tests check stable message IDs, zero sends/deletions
during updates, source corrections and replacement recovery, restarts, explicit
Back, renderer fallback, and independent modes for concurrent drafts.

The owner also requested compact labels and catalog formatting in this update:
normal cards use `<code>#6</code>`, previews use Markdown inline code `#6`,
and taxonomy items each use `<code>` on a separate line. The two-line footer
example remains one copyable block. The tracked catalog message ID is retained,
so format updates preserve the owner's pin.

All 55 test functions pass for this update. Race checks, vet, and the executable
build pass. Tests verify compact inline IDs and taxonomy items as well as stable
message IDs and persistent preview behavior.

Deployment backed up SQLite and restarted the bot. All three saved drafts kept
their content, source bindings, metadata, publication state, and card IDs. The
tracked taxonomy message ID stayed the same, and its saved rendering hash
confirmed the inline monospace format was successfully applied to Telegram.

## Telegram constraints checked

- [Message edits](https://core.telegram.org/bots/api#editmessagetext) can update
  bot text/rich text and its inline keyboard.
- [Deletion](https://core.telegram.org/bots/api#deletemessage) has a 48-hour limit
  and permits deleting incoming/outgoing messages in private chats.
- [Updates](https://core.telegram.org/bots/api#update) include `edited_message`;
  deletion notifications are for connected business messages, not ordinary DMs.
- [Formatting](https://core.telegram.org/bots/api#formatting-options) and
  [rich messages](https://core.telegram.org/bots/api#rich-messages) supply bot-side
  rendering; the composer does not require Telegram's Premium visual editor.

## Implementation verification

All 35 tests pass, including the composer scenarios specified in round 10.
`go test -race ./...`, `go vet ./...`, and the executable build also pass.
Tests exercise a local Telegram API simulator; no test publishes to a real channel.

Deployment on September 30, 2026 restored the existing draft as a formatted card.
Its title, summary, body, category, tags, pending text, and publication fields
matched their pre-deployment hashes. SQLite backups are kept under ignored `.run/`.
Live Telegram reads confirmed the updated owner command menu and Menu button,
and the restarted process is polling without a webhook. Native owner-message
editing is covered by integration tests; a live client walkthrough remains manual.
