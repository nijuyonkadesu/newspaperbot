package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"newspaperbot/internal/post"
)

// Publication owns the frozen content and Git checkpoints. Draft cards can
// change presentation while a worker publishes, without overwriting this job.
type Publication struct {
	Operation      string
	Draft          post.Draft
	State          string // queued, failed, pushed, done
	CommitSHA      string
	Number         int64
	Filename, Slug string
	Error          string
	Conflict       bool
}

func (s *Store) Queue(ctx context.Context, id, channel int64) (post.Draft, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return post.Draft{}, err
	}
	defer tx.Rollback()
	d, err := decode(tx.QueryRowContext(ctx, "SELECT id,slot,data FROM drafts WHERE id=?", id))
	if err != nil {
		return d, err
	}
	if d.Number != 0 && d.Revision == nil {
		return d, errors.New("this post already has a publication number")
	}
	if d.Revision != nil && (d.Revision.Applied || d.Revision.Conflict) {
		return d, errors.New("this revision cannot be queued again")
	}
	if d.GitOperation != "" {
		if d.GitState != "failed" {
			return d, nil
		}
		if _, err := tx.ExecContext(ctx, "UPDATE publications SET state='queued' WHERE operation=?", d.GitOperation); err != nil {
			return d, err
		}
	} else {
		if err := d.ValidatePortfolio(); err != nil {
			return d, err
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return d, err
		}
		d.GitOperation = hex.EncodeToString(random[:])
		d.Slot = 0
		if d.Revision == nil {
			d.PublishedAt = time.Now().UTC()
			d.Portfolio, d.ChannelID = true, channel
			if channel != 0 {
				d.Delivery = "pending"
			}
		}
		job := Publication{Operation: d.GitOperation, Draft: d, State: "queued"}
		data, err := json.Marshal(job)
		if err != nil {
			return d, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO publications(operation,draft_id,state,data) VALUES(?,?,'queued',?)
			ON CONFLICT(draft_id) DO UPDATE SET operation=excluded.operation,state=excluded.state,data=excluded.data`, job.Operation, id, string(data)); err != nil {
			return d, err
		}
	}
	d.GitState, d.Notice, d.View = "queued", "Publishing…", ""
	if d.Revision != nil {
		d.Notice = "Saving changes…"
	}
	d.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(d)
	if err != nil {
		return d, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE drafts SET slot=NULL,data=? WHERE id=?", string(data), id); err != nil {
		return d, err
	}
	return d, tx.Commit()
}

func (s *Store) SavePublication(ctx context.Context, job *Publication) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE publications SET state=?,data=? WHERE operation=?", job.State, string(data), job.Operation)
	return err
}

func (s *Store) FailPublication(ctx context.Context, job *Publication) (post.Draft, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return post.Draft{}, err
	}
	defer tx.Rollback()
	d, err := decode(tx.QueryRowContext(ctx, "SELECT id,slot,data FROM drafts WHERE id=?", job.Draft.ID))
	if err != nil {
		return d, err
	}
	if d.GitOperation != job.Operation {
		return d, errors.New("publication operation changed")
	}
	d.GitState, d.Notice = "failed", "Could not confirm the commit on main. Retry publish checks the remote before continuing."
	if d.Revision != nil {
		d.Notice = "Changes saved locally · retry saving"
		if job.Conflict {
			d.Revision.Conflict, d.Notice = true, "Article changed on main · download your changes or discard and reload"
		}
	}
	d.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(d)
	if err != nil {
		return d, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE drafts SET data=? WHERE id=?", string(data), d.ID); err != nil {
		return d, err
	}
	job.State = "failed"
	data, err = json.Marshal(job)
	if err != nil {
		return d, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE publications SET state='failed',data=? WHERE operation=?", string(data), job.Operation); err != nil {
		return d, err
	}
	return d, tx.Commit()
}

func (s *Store) PendingPublications(ctx context.Context) ([]Publication, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT state,data FROM publications WHERE state IN ('queued','pushed') ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Publication
	for rows.Next() {
		var job Publication
		var data []byte
		var state string
		if err := rows.Scan(&state, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &job); err != nil {
			return nil, err
		}
		job.State = state
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// FinishPublication atomically checkpoints the remote commit and draft. It
// merges into the latest card, rather than restoring its frozen presentation.
func (s *Store) FinishPublication(ctx context.Context, job *Publication) (post.Draft, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return post.Draft{}, err
	}
	defer tx.Rollback()
	d, err := decode(tx.QueryRowContext(ctx, "SELECT id,slot,data FROM drafts WHERE id=?", job.Draft.ID))
	if err != nil {
		return d, err
	}
	if d.GitOperation != job.Operation {
		return d, errors.New("publication operation changed")
	}
	d.Number, d.Filename, d.Slug, d.CommitSHA = job.Number, job.Filename, job.Slug, job.CommitSHA
	d.Exported, d.GitState, d.Notice = true, "done", ""
	if d.Revision != nil {
		d.Revision.Applied = true
		d.Delivery = "pending"
	}
	d.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(d)
	if err != nil {
		return d, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE drafts SET number=?,data=? WHERE id=?", d.Number, string(data), d.ID); err != nil {
		return d, err
	}
	job.State = "done"
	data, err = json.Marshal(job)
	if err != nil {
		return d, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE publications SET state='done',data=? WHERE operation=?", string(data), job.Operation); err != nil {
		return d, err
	}
	return d, tx.Commit()
}
