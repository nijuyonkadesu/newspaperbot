# Portfolio image captions

Center visible captions beneath their images, with smaller text and tighter
spacing. Keep alt text separate. Preserve caption links and Markdown formatting.

The bot exports explicit caption blocks. The portfolio renders them as figures
with centered captions. Raw HTML remains disabled.

The portfolio renderer is implemented on `feature/image-captions`, commit
`9f57200`, in `/home/shichika/redacted/portfolio-captions`. Deploy that change
alongside the bot's export support.

## Markdown convention

Use an explicit caption block immediately after a standalone image:

```markdown
![Descriptive alt text](/assets/images/posts/<image-1>.jpg)

:::caption
First caption with **emphasis** and a [link](https://example.com).
:::

![](/assets/images/posts/<image-2>.jpg)

:::caption
Second caption.
:::
```

An empty `![]` remains empty alt text. Captions do not supply alt text. Longer
delimiters are used when a caption contains a colon-only line.

## Portfolio renderer

- **`scripts/content/captions.mjs`:** pairs a standalone image with its caption
  block, using the existing Markdown-it parser. Formatting, links, multiple
  paragraphs, footnotes, image resolution, and asset copying are preserved.
  Unclosed/orphan blocks remain ordinary text; fenced-code delimiters stay literal.
- **`src/styles/blog.css`:** scopes caption layout to `.image-caption`, reusing
  existing article image borders, shadows, fonts, link styles, and theme colors:

```css
.prose figure.image-caption { margin: 2rem auto; }
.prose figure.image-caption > img { margin: 0 auto; }
.prose figure.image-caption > figcaption {
  margin-top: .75rem;
  text-align: center;
  font-size: .85em;
  color: var(--ink-soft);
}
.prose figure.image-caption > figcaption > :first-child { margin-top: 0; }
.prose figure.image-caption > figcaption > :last-child { margin-bottom: 0; }
```

Expected HTML:

```html
<figure class="image-caption">
  <img src="/assets/images/posts/…" alt="Descriptive alt text">
  <figcaption><p>Caption with its formatting and links.</p></figcaption>
</figure>
```

## Bot behavior

- Caption markers appear in committed/downloaded Markdown, including saved drafts.
  Telegram cards and channel/group posts show caption text without those markers,
  including when an article is reopened from Git.
- Only known image captions gain markers. Videos, attachments, and existing
  unmarked paragraphs retain their current layout.
- Album order and caption association are preserved; source attribution stays
  outside the figure. Caption links and formatting remain intact.
- Saved photo drafts use the new export format without a database migration.
  Original-message edits keep their article association after publication.
