package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"clipstack/internal/proto"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS clips (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    content    TEXT NOT NULL UNIQUE,
    pinned     INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_created ON clips(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_pinned  ON clips(pinned DESC, created_at DESC);
`

type DB struct {
	db *sql.DB
}

func Open() (*DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}

	dir := filepath.Join(home, ".local", "share", "clipstack")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}

	path := filepath.Join(dir, "history.db") + "?_journal_mode=WAL&_busy_timeout=5000"
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	sqldb.SetMaxOpenConns(1)

	if _, err := sqldb.Exec(schema); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}

	return &DB{db: sqldb}, nil
}

func (d *DB) Close() {
	d.db.Close()
}

func (d *DB) Insert(content string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.Exec(
		`INSERT INTO clips(content, created_at) VALUES(?, ?)
         ON CONFLICT(content) DO UPDATE SET created_at = excluded.created_at`,
		content, now,
	)
	return err
}

func (d *DB) List(limit, offset int) ([]proto.Item, error) {
	rows, err := d.db.Query(
		`SELECT id, content, pinned, created_at FROM clips
         ORDER BY pinned DESC, created_at DESC
         LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}

func (d *DB) Search(query string, limit, offset int) ([]proto.Item, error) {
	rows, err := d.db.Query(
		`SELECT id, content, pinned, created_at FROM clips
         WHERE LOWER(content) LIKE '%' || LOWER(?) || '%'
         ORDER BY pinned DESC, created_at DESC
         LIMIT ? OFFSET ?`,
		query, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}

func (d *DB) SetPinned(id int64, pinned bool) error {
	v := 0
	if pinned {
		v = 1
	}
	_, err := d.db.Exec(`UPDATE clips SET pinned = ? WHERE id = ?`, v, id)
	return err
}

func (d *DB) Delete(id int64) error {
	_, err := d.db.Exec(`DELETE FROM clips WHERE id = ?`, id)
	return err
}

func (d *DB) GetContent(id int64) (string, error) {
	var content string
	err := d.db.QueryRow(`SELECT content FROM clips WHERE id = ?`, id).Scan(&content)
	return content, err
}

func scanItems(rows *sql.Rows) ([]proto.Item, error) {
	defer rows.Close()
	var items []proto.Item
	for rows.Next() {
		var it proto.Item
		var pinned int
		if err := rows.Scan(&it.ID, &it.Content, &pinned, &it.CreatedAt); err != nil {
			return nil, err
		}
		it.Pinned = pinned != 0
		items = append(items, it)
	}
	return items, rows.Err()
}
