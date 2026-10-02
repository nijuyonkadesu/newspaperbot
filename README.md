![A blog draft being previewed in Telegram](docs/telegram-preview.jpg)

Write and publish in your bot DM: **one message → Publish**.

1. **Write.** First line: title. Next paragraph: summary. The rest: Markdown body.

   ```markdown
   /newpost A useful trick

   A short summary of the post.

   ## The idea
   Your Markdown content goes here.

   Category: concept
   Tags: code, my-new-tag
   ```

   Or send `/newpost`, then the post. The optional final two lines set one
   category and any tags; `Tags: -` means none. Existing names work without
   labels. Label new names; `*` marks them in preview and is never saved.

2. **Edit.** Edit your source message or send additions. One card updates in
   place. **Preview** renders content and taxonomy and stays enabled through
   edits. **Category** and **Tags** change selections; **Options** has replacement,
   download, undo, and cancellation.

3. **Keep multiple drafts.** `/newpost` starts another; `/drafts` lists them.
   Reply to a card/source to append there. Unthreaded text goes to the selected
   post. Source edits update their corresponding cards.

4. **Publish.** **Publish** commits Markdown to `content/tweets/` and updates
   taxonomy on `main`, then posts to the configured destination. Article number
   and date are assigned at publication. Progress stays on the card.

**Edit published articles**

1. `/posts` → **Edit #number**, or `/edit 100`. The card shows **Editing live**,
   **Today/Earlier**, and the original date. **Preview #number** only previews.
2. **Original message**, when available, opens a quote; tap it to edit the source.
   Reply to the edit card to append, or `/replace` to rewrite.
3. Appended messages accept the same two-line taxonomy footer; it updates
   selections and is removed from the body. Single-line additions stay content.
4. **Save changes** or `/save` updates Git and linked channel messages.
   **Number, date, and URL stay unchanged.** Missing channel associations are skipped.
5. **Discard changes** or `/cancel` keeps the published version. `/edit <number>`
   resumes pending edits; these stay separate from `/drafts`.

Editing remains available indefinitely. Drafts, pending edits, and preview
preferences survive restarts.

Reply to the `/posts` list with a number to jump; your reply is deleted.
**Refresh** reloads the range. Sending `/posts` again replaces the list.

**Commands**

| Command | Action |
| --- | --- |
| `/taxonomy` | Copy grouped categories/tags; pin the updating list. |
| `/resume <draft number>` | Resume a draft. |
| `/replace` | Replace the whole post. |
| `/download` | Download Markdown. |
| `/undo` | Remove last appended text; keep its chat message. |
| `/remove` · `/remove <message ID>` | Remove an addition by reply or ID. |
| `/cancel` | Delete draft / discard article edits. |
| `/delete <draft number>` | Delete a specific draft. |
| `/channels` | Show configured destination. |
| `/setchannel <@name or ID>` | Set channel/group for future posts. |
| `/unsetchannel` | Clear destination for future posts. |
| `/help` | Show usage. |

Setting a destination requires bot admin access and your membership.
**Review → Source** opens a quote; reply with `/remove` to drop the addition
and clear its chat messages when Telegram permits. `/undo` leaves messages and
doesn't revert edits, replacements, or taxonomy.
