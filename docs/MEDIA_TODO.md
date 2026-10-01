# Media backlog

- Decide how captions, caption entities, photos, videos, and albums become blog content.
- One option is to download media, publish it with the Markdown, and generate stable repository-relative links.
- Another option is to retain the forwarded origin and add a source-attribution link instead of importing the media. Public channel posts can use `t.me/<username>/<message-id>`; accessible private channels can use `t.me/c/<channel-id>/<message-id>`. User and hidden-user forwards may not have a linkable origin.
- Define attribution text, album grouping, filenames, cleanup, and behavior when the original post is private or unavailable before implementing either option.
