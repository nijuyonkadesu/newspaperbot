# Media

Implemented:

- Photos and JPEG/PNG/WebP documents append to their linked draft/article; captions retain text links and formatting. Captions never set taxonomy.
- Draft JSON stores Telegram file IDs and caption metadata. SQLite schema is unchanged; no image bytes are stored there.
- Publish/Save retrieves new image files from Bot API, validates size/format/dimensions, and writes them under `src/assets/images/posts/` before the Git commit. Filenames derive from Telegram's stable unique file IDs.
- The site's existing asset-copy step serves these files under `/assets/images/posts/`. Images, Markdown, and generated taxonomy share one commit.
- Rich previews and channel/group messages reuse Telegram photo IDs. Image documents without thumbnails use a file block in Telegram.
- Public forwarded sources are linked. Videos and other attachments use public source/supplied links; unresolved links require review before publication.
- Albums retain message order and their original draft. Publish waits briefly for arrivals; later members become unsaved edits to the same article.
- `/remove` removes the item and its caption. Caption and supplied-link edits update the card; manual chat deletions remain undetectable.
- Failed image retrieval before any commit restores an editable post. Confirmed/uncertain commits keep the existing recovery path.

Local Bot API: when `getFile` returns an absolute path, the bot's service user must be able to read that file. Container storage must be accessible at that path on the host. Public API file paths use bounded HTTP downloads without redirects.

Still deferred:

- Import forwarded RichMessage blocks. They currently appear under Review.
- Host videos/play them directly in the website rather than link them.
- Add dedicated image alt-text editing. Captions remain separate from alt text.
- Reconstruct Telegram photo references for repository articles after resetting the bot database.

References: [Telegram file IDs](https://core.telegram.org/bots/api#file), [local API files](https://core.telegram.org/bots/api#using-a-local-bot-api-server), [rich media](https://core.telegram.org/bots/api#inputrichmessagemedia).
