![A blog draft being previewed in Telegram](docs/telegram-preview.jpg)

Draft and publish a post from your private chat with the bot: **one message, then one Publish tap**.

1. **Write the whole post in one message.**

   ```markdown
   /newpost A useful trick

   A short summary of the post.

   ## The idea
   Your Markdown content goes here.

   Category: concept
   Tags: code, my-new-tag
   ```

   You can also send `/newpost` first, then send the post. The title is the first
   line, the summary is the next paragraph, and the remainder is the Markdown
   body. The bot saves your draft and keeps one card for it.

   The optional final two lines set one category and multiple comma-separated
   tags. They are removed from the body. Use `Tags: -` for none. New names show
   `*` on the card and preview; the marker is never part of the saved name.

   Existing names also work without the `Category:` and `Tags:` labels. Use the
   labels for new names so trailing prose isn't mistaken for metadata.

2. **Edit and preview without starting over.**

   Edit your original Telegram message to change the title, summary, body, or
   footer. The same card updates in place. Further messages append to the body;
   editing an appended message changes that addition.

   Tap **Preview** to see the formatted post, category, and tags. Preview stays
   enabled through edits and restarts; **Back** returns to the compact view.
   **Category** and **Tags** are directly on the card. Choose one category and
   toggle any number of tags. Suggested tags appear first, but you can select
   tags from any category.

   **Options** lets you replace the whole post, download it, undo the last
   addition, or cancel the draft.

3. **Keep several drafts going.**

   Send `/newpost` for another draft. Older drafts stay saved, and editing their
   original messages updates the corresponding cards.

   Reply to a draft's card or source message to append specifically there.
   Unthreaded text goes to the draft most recently selected with `/newpost`,
   `/resume`, or **Replace post**. Each draft's buttons work independently.

4. **Publish when you're ready.**

   Tap **Publish**. The card shows progress while you can keep working on other
   drafts. The post is saved as numbered Markdown in `content/tweets/`, with
   your selected category and tags. New names join the taxonomy, and the post
   and updated references land together on the repository's `main` branch.

   If you've configured a channel, the bot posts there after the repository
   publication succeeds. The card shows the result without another success
   message. A queued or published post is locked against editing and deletion.

   If publication pauses, tap **Retry publish**. The bot checks whether the
   previous attempt already landed before continuing. If channel delivery is
   uncertain, check the channel before choosing the explicit retry button.

Useful commands while writing:

- `/taxonomy` — get a copyable list of categories and tags. Pin it yourself;
  the same message updates when the available names change.
- `/drafts` · `/resume <id>` — list saved drafts and select one.
- `/preview` · `/download` — preview the active draft or get its Markdown file.
- `/replace` · `/undo` — replace the whole post or remove the last body addition.
- `/cancel` — delete the entire active unfinished draft and remove its card.
- `/delete <id>` — delete a specific unfinished draft; `/delete` uses the active
  draft. Successful deletion gets a 👍 reaction or a brief confirmation.
- `/setchannel @channel` — set or change the destination. Add the bot as an
  administrator with permission to post, and be a member yourself. Changes apply
  to future publications; `/unsetchannel` disables channel posting.

Drafts survive restarts. Deleting a source message in Telegram does not remove
its saved content; use `/undo`, `/replace`, or `/cancel` instead.
