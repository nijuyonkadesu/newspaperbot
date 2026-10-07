package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"newspaperbot/internal/post"
)

func (s *Store) GetArticle(ctx context.Context, number int64) (post.Draft, error) {
	return decode(s.db.QueryRowContext(ctx, "SELECT id,slot,data FROM drafts WHERE number=?", number))
}

// StartRevision resumes existing work, or refreshes a live article from Git.
// Destination IDs belong to the original publication, not the current setting.
func (s *Store) StartRevision(ctx context.Context, article post.Article) (post.Draft, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return post.Draft{}, err
	}
	defer tx.Rollback()
	d, err := decode(tx.QueryRowContext(ctx, "SELECT id,slot,data FROM drafts WHERE number=?", article.Number))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	if d.Revision == nil {
		if d.ID != 0 && (!d.Exported || d.ChannelID != 0 && d.Delivery != "sent") {
			return d, errors.New("finish the original publication before editing")
		}
		live := article.Draft
		live.ID, live.CardID, live.Preview = d.ID, d.CardID, d.Preview
		live.SourceReplyID, live.SourceReplyToID = d.SourceReplyID, d.SourceReplyToID
		live.Images, live.LateMedia = d.Images, d.LateMedia
		live.ChannelID, live.Delivery, live.DocumentMode = d.ChannelID, d.Delivery, d.DocumentMode
		live.SummaryMessageID, live.ContentMessageID = d.SummaryMessageID, d.ContentMessageID
		live.Categories, live.AvailableTags, live.TagGroups = d.Categories, d.AvailableTags, d.TagGroups
		if post.SameArticle(d, live) {
			live.Sources, live.BaseContent, live.LastMessageID = d.Sources, d.BaseContent, d.LastMessageID
			live.MessageIssues = d.MessageIssues
		}
		live.Revision = &post.Revision{Original: article.Original}
		live.UpdatedAt = time.Now().UTC()
		d = live
		d.ResetView()
		data, err := json.Marshal(d)
		if err != nil {
			return d, err
		}
		if d.ID == 0 {
			result, err := tx.ExecContext(ctx, "INSERT INTO drafts(number,data) VALUES (?,?)", d.Number, string(data))
			if err != nil {
				return d, err
			}
			d.ID, err = result.LastInsertId()
			if err != nil {
				return d, err
			}
		} else if _, err := tx.ExecContext(ctx, "UPDATE drafts SET data=? WHERE id=?", string(data), d.ID); err != nil {
			return d, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES ('active',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, d.ID); err != nil {
		return d, err
	}
	return d, tx.Commit()
}

func (s *Store) DiscardRevision(ctx context.Context, id int64) (post.Draft, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return post.Draft{}, err
	}
	defer tx.Rollback()
	d, err := decode(tx.QueryRowContext(ctx, "SELECT id,slot,data FROM drafts WHERE id=?", id))
	if err != nil {
		return d, err
	}
	if d.Revision == nil || d.Revision.Applied || d.Locked() && !d.Revision.Conflict {
		return d, errors.New("changes are already queued; finish saving first")
	}
	original, err := post.ReadArticle(d.Filename, d.Revision.Original)
	if err != nil {
		return d, err
	}
	d.Title, d.Summary, d.Content = original.Title, original.Summary, original.Content
	d.Category, d.Tags, d.Frontmatter = original.Category, original.Tags, original.Frontmatter
	d.Revision, d.Sources, d.MessageIssues, d.ReplacementSource = nil, nil, nil, nil
	d.PublishRequestedAt, d.AlbumUpdatedAt, d.LateMedia = time.Time{}, time.Time{}, nil
	d.GitOperation, d.GitState, d.View, d.Notice, d.Invalid, d.BaseContent = "", "done", "", "", "", ""
	d.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(d)
	if err != nil {
		return d, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE drafts SET data=? WHERE id=?", string(data), id); err != nil {
		return d, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE settings SET value=0 WHERE key='active' AND value=?", id); err != nil {
		return d, err
	}
	return d, tx.Commit()
}
