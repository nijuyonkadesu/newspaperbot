package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"tgblogbot/internal/post"
)

type Store struct{ db *sql.DB }

var ErrPublicationLocked = errors.New("this post is already prepared for publication and cannot be deleted as a draft")

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: "_busy_timeout=5000&_txlock=immediate"}
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS drafts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		data TEXT NOT NULL,
		number INTEGER UNIQUE
	);
	CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value INTEGER NOT NULL);
	CREATE TABLE IF NOT EXISTS publications (
		operation TEXT PRIMARY KEY,
		draft_id INTEGER NOT NULL UNIQUE,
		state TEXT NOT NULL,
		data TEXT NOT NULL
	);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) New(ctx context.Context, categories, tags []string) (post.Draft, error) {
	d := post.Draft{ComposerVersion: 1, Step: post.Compose, Categories: categories, AvailableTags: tags, Tags: []string{}, UpdatedAt: time.Now().UTC()}
	if len(categories) > 0 {
		d.Category = categories[0]
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	data, err := json.Marshal(d)
	if err != nil {
		return d, err
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO drafts(data) VALUES (?)", string(data))
	if err != nil {
		return d, err
	}
	d.ID, err = result.LastInsertId()
	if err != nil {
		return d, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES ('active',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, d.ID); err != nil {
		return d, err
	}
	return d, tx.Commit()
}

func (s *Store) Get(ctx context.Context, id int64) (post.Draft, error) {
	return decode(s.db.QueryRowContext(ctx, "SELECT id,data FROM drafts WHERE id=?", id))
}

func decode(row *sql.Row) (post.Draft, error) {
	var d post.Draft
	var data []byte
	var id int64
	if err := row.Scan(&id, &data); err != nil {
		return d, err
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return d, err
	}
	d.ID = id
	d.Normalize()
	return d, nil
}

func (s *Store) Save(ctx context.Context, d *post.Draft) error {
	d.UpdatedAt = time.Now().UTC()
	return s.SaveCard(ctx, d)
}

// SaveCard checkpoints presentation without invalidating the rendered buttons.
func (s *Store) SaveCard(ctx context.Context, d *post.Draft) error {
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE drafts SET data=? WHERE id=? AND COALESCE(number,0)=? AND COALESCE(json_extract(data,'$.GitOperation'),'')=?", string(data), d.ID, d.Number, d.GitOperation)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("draft changed or no longer exists; resume it again")
	}
	return nil
}

func (s *Store) List(ctx context.Context) ([]post.Draft, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,data FROM drafts ORDER BY id DESC LIMIT 20")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var drafts []post.Draft
	for rows.Next() {
		var d post.Draft
		var data []byte
		var id int64
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &d); err != nil {
			return nil, err
		}
		d.ID = id
		d.Normalize()
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}

func (s *Store) Setting(ctx context.Context, key string) (int64, error) {
	var value int64
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return value, err
}

func (s *Store) SetSetting(ctx context.Context, key string, value int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) Active(ctx context.Context) (post.Draft, error) {
	id, err := s.Setting(ctx, "active")
	if err != nil {
		return post.Draft{}, err
	}
	if id == 0 {
		return post.Draft{}, sql.ErrNoRows
	}
	return s.Get(ctx, id)
}

// Delete removes an unfinished draft and clears its active selection atomically.
// Reserved publications retain their number and recovery checkpoints.
func (s *Store) Delete(ctx context.Context, id int64) (post.Draft, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return post.Draft{}, err
	}
	defer tx.Rollback()
	d, err := decode(tx.QueryRowContext(ctx, "SELECT id,data FROM drafts WHERE id=?", id))
	if err != nil {
		return d, err
	}
	if d.Locked() {
		return d, ErrPublicationLocked
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM drafts WHERE id=? AND COALESCE(number,0)=0", id)
	if err != nil {
		return d, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return d, err
	}
	if count != 1 {
		return d, ErrPublicationLocked
	}
	if _, err := tx.ExecContext(ctx, "UPDATE settings SET value=0 WHERE key='active' AND value=?", id); err != nil {
		return d, err
	}
	return d, tx.Commit()
}

// Reserve freezes the post, number, export path, and channel in one transaction.
func (s *Store) Reserve(ctx context.Context, id, minimum, channel int64, dir string) (post.Draft, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return post.Draft{}, err
	}
	defer tx.Rollback()
	d, err := decode(tx.QueryRowContext(ctx, "SELECT id,data FROM drafts WHERE id=?", id))
	if err != nil {
		return d, err
	}
	if d.Number != 0 {
		return d, nil
	}
	if d.GitOperation != "" {
		return d, ErrPublicationLocked
	}
	if err := d.Validate(); err != nil {
		return d, err
	}
	var highest int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(number),0) FROM drafts").Scan(&highest); err != nil {
		return d, err
	}
	highest = max(highest, minimum)
	if highest == math.MaxInt64 {
		return d, errors.New("post numbers are exhausted")
	}
	d.Number = highest + 1
	d.PublishedAt = time.Now().UTC()
	d.UpdatedAt = d.PublishedAt
	d.Filename = filepath.Join(dir, strconv.FormatInt(d.Number, 10)+".md")
	d.ChannelID = channel
	if channel != 0 {
		d.Delivery = "pending"
	}
	data, err := json.Marshal(d)
	if err != nil {
		return d, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE drafts SET number=?,data=? WHERE id=?", d.Number, string(data), id); err != nil {
		return d, fmt.Errorf("reserve number: %w", err)
	}
	return d, tx.Commit()
}

// FromMessage resolves a source message or current card to its own draft.
func (s *Store) FromMessage(ctx context.Context, messageID int) (post.Draft, error) {
	return decode(s.db.QueryRowContext(ctx, `SELECT id,data FROM drafts WHERE EXISTS
 (SELECT 1 FROM json_each(drafts.data, '$.Sources') WHERE json_extract(value, '$.MessageID') = ?)
 OR json_extract(data, '$.ReplacementSource.MessageID') = ?
 OR json_extract(data, '$.CardID') = ?`, messageID, messageID, messageID))
}
