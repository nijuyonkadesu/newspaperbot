# Portfolio publishing

Set `PORTFOLIO_GIT_TOKEN` to a GitHub token with read/write access to the private
portfolio repository. A fine-grained token needs Contents: read and write on
that repository. `PORTFOLIO_REPO_URL` defaults to
`https://github.com/nijuyonkadesu/portfolio.git`; the branch is `main`.

The bot requires Git, Node 22+, and npm in addition to its Go build requirements.
It clones once into `PORTFOLIO_CACHE_DIR` (default `.run/portfolio`) and reuses
that checkout and npm dependencies. This directory is a **bot-owned cache**;
do not use a personal working checkout or edit files there. A process lock
prevents two instances from using the cache simultaneously.

Credentials are passed only to Git through temporary process environment
configuration. They are absent from remote URLs, command arguments, Git config,
commit contents, and npm's environment. `.env`, databases, binaries, and the
default cache are ignored by the bot repository. Load `.env` before launch;
the executable reads the environment, not the file.

## Catalog and input

The remote's `content/tweets` filenames determine the next number. Categories
come from `category.yaml`, and `tag.yaml` maps categories to tags. The older
`category.txt` (one category per line) and `tag.txt` (YAML mapping) are also
supported for compatibility with the previous generator revision.
The bot follows the repository's generator when choosing which files to commit;
it does not rename references or modify that generator.

Each catalog snapshot comes from one fetched `origin/main` revision. Fetching
runs every 30 seconds and before publishing. Owner actions use an in-memory
snapshot, so a network failure retains the last valid catalog and does not block
composition. `/taxonomy` continues updating its existing pinned message.

Groups suggest tags; they do not limit allowed combinations. A tag can belong
to several categories. Tag pickers show associated tags first, followed by all
other tags, without duplicates. One category and any number of tags are allowed.

Markdown input remains unchanged. Explicit final lines can introduce new values:

```text
Category: a new category
Tags: existing-tag, new-tag
```

New names appear with `*` on the draft card and preview. This marker is display
text only and never becomes part of frontmatter. Categories preserve spelling
and case and must occupy one line. Tags must be lowercase letters/numbers
separated by single hyphens, matching the portfolio's validation. `Tags: -`
means none. Bare two-line footers still require existing category/tag names;
unknown bare lines remain body text to avoid misinterpreting prose. Appends
remain verbatim body additions; native edits to the original full message can
change its footer.

## Publish and recovery

Publish freezes the draft's content, date, and channel in a durable SQLite job.
The card is edited in place to show progress. Other drafts remain editable while
Git and npm run in a background worker. Queued posts are locked, including
against deletion. Completed channel deliveries keep their existing checkpoints.

For each publication, the worker:

1. Fetches `main` and checks whether this operation already landed there.
2. Builds `content/tweets/NNN-slug.md` from the newest remote numbering. Numbers
   have at least three digits. Slugs are lowercase ASCII words separated by
   hyphens; duplicates get a numeric suffix. A title with no ASCII words uses
   `note-NNN`. The publication date is frozen when queued.
3. Writes YAML frontmatter: `type: tweet`, `title`, `slug`, quoted `YYYY-MM-DD`
   date, `summary`, `category`, and `tags`, followed by the saved Markdown body.
4. Installs dependencies with `npm ci --ignore-scripts` when the lockfile changes,
   then runs the **original repository's** `npm run taxonomy:sync`. This validates
   all finalized notes and generates both references. Unexpected tracked file
   changes stop publication.
5. Commits the note and both reference paths together, recording a stable
   `Blogbot-Operation` trailer. Unchanged references need no diff.
6. Fetches and rebases before a normal push directly to `main`. The generator
   runs again after rebase to catch semantic numbering conflicts and refresh
   references. Generated-file conflicts or numeric collisions rebuild from the
   latest remote, with at most three attempts. Force pushes are never used.
7. Records the confirmed remote commit, then delivers to the configured channel.

SQLite checkpoints the operation and candidate commit before pushing. After a
lost response or restart, the worker checks the remote operation trailer before
creating another note. Pending jobs resume automatically on startup. Exhausted
or failed jobs show **Retry publish**; retries keep the same frozen content and
operation. A restart during rebase aborts the interrupted rebase in the owned
cache and reconstructs the unpublished work from SQLite.

Git publication and channel delivery are separate. A committed post whose
channel delivery fails can continue publishing without another repository
commit. Uncertain Telegram sends still require checking the channel before an
explicit retry. Startup does not automatically resend those uncertain messages.
Old local-file publications retain their original recovery path and are never
silently moved into the portfolio repository.

## Local development

Leave both `PORTFOLIO_GIT_TOKEN` and `PORTFOLIO_REPO_URL` unset to use the original
JSON fixtures and working-directory exporter. Repository integration tests use
temporary bare Git repositories and a dependency-free Node fixture, so they do
not contact GitHub or Telegram. Run `go test -race ./...` and `go vet ./...`.

The production taxonomy command has also been checked against the fetched
portfolio checkout. No sample note was committed or pushed to the live remote
during development.
