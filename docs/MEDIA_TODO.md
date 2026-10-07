# Media backlog

- Decide how captions, caption entities, photos, videos, and albums become blog content.
- One option is to download media, publish it with the Markdown, and generate stable repository-relative links.
- Another option is to retain the forwarded origin and add a source-attribution link instead of importing the media. Public channel posts can use `t.me/<username>/<message-id>`; accessible private channels can use `t.me/c/<channel-id>/<message-id>`. User and hidden-user forwards may not have a linkable origin.
- Define attribution text, album grouping, filenames, cleanup, and behavior when the original post is private or unavailable before implementing either option.

## Approaches reviewed

| Approach | Benefit | Limitation | Verdict |
| --- | --- | --- | --- |
| Link to the original Telegram post | Almost no storage or setup; keeps attribution. | Personal forwards have no post URL. Private posts need membership; deleted sources break the blog. | Useful attribution, insufficient as the whole feature. |
| Use Telegram as the media host | Reuse existing files; no new storage service. | Bot download URLs contain credentials and are temporary. File IDs are not public asset URLs; local API paths are not website URLs. | Suitable for bot previews, not published blog assets. |
| Commit every attachment alongside Markdown | One Git publication; files remain with their article. | Large videos inflate history. The current site renders images but has HTML disabled, so video players need site changes. | Good for bounded images, poor as an unrestricted media archive. |
| Upload everything to object storage/CDN | Stable public URLs; videos do not inflate Git. | Adds upload credentials, storage setup, retry/cleanup rules, and separate asset and article publication. | Best if large uploaded videos become essential. |
| Commit bounded images; link videos; retain source attribution | Uses the existing Git/image pipeline; no extra service or wizard. | Unlinkable videos need an explicit public URL or removal; images still consume Git space. | Recommended for the current workflow. |

## Recommended flow — proposed, not implemented

- Send or forward media to append at that point in the selected post; replies route to the linked draft/article.
- Photos become Markdown images. Cache bytes before publication; commit validated, size-limited, content-hashed files under `src/assets/images/posts/` with Markdown and taxonomy. Use `/assets/images/posts/<hash>.jpg` in Markdown: the current site build copies `src/assets/` to public `/assets/`, so this also works with a private Git repository and needs no new CDN configuration.
- Albums group by `media_group_id`, retain message order, and update the same card as items arrive. Wait for pending downloads and a short arrival quiet period before taking the publication snapshot. Telegram supplies no explicit album-complete marker: late members must enter the same article's pending edits, never auto-publish or attach to a different selected draft.
- Captions appear once beneath their media/group, with their links and supported formatting preserved. They do not act as title, summary, or category/tag footer. Caption text and image alt text remain separate.
- Preview and channel/group output reuse Telegram file IDs. Committed Markdown contains stable image paths and public video/source links, never bot file URLs or IDs.
- Videos use a supplied public URL or a public channel-origin post URL. Forwards from users/hidden users lack linkable post IDs. Private Telegram links are not usable attribution for a public blog.
- If a video has no public URL, show the issue on the existing card: reply to the video with its URL, or reply `/remove`. Do not silently omit it or publish an unresolved placeholder.
- `/remove` removes the targeted item; caption edits update its text. Manual deletion remains undetectable in normal Bot API DMs.
- Keep draft/article media separate until Publish/Save succeeds. Cancel clears uncommitted local assets; removing a committed asset does not erase Git history. Leave shared assets alone.

References: [Telegram message fields and origins](https://core.telegram.org/bots/api#message), [file URLs](https://core.telegram.org/bots/api#file), [local API files](https://core.telegram.org/bots/api#using-a-local-bot-api-server).
