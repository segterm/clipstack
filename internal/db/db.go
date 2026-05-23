package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"clipstack/internal/proto"

	_ "modernc.org/sqlite"
)

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

	d := &DB{db: sqldb}
	if err := d.migrate(); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return d, nil
}

func (d *DB) Close() {
	d.db.Close()
}

// ── Migration ─────────────────────────────────────────────────────────────────

func (d *DB) migrate() error {
	var version int
	d.db.QueryRow("PRAGMA user_version").Scan(&version)
	if version < 1 {
		if err := d.migrateV1(); err != nil {
			return err
		}
		version = 1
	}
	if version < 2 {
		return d.migrateV2()
	}
	return nil
}

func (d *DB) migrateV2() error {
	if _, err := d.db.Exec(`ALTER TABLE clips ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	_, err := d.db.Exec(`PRAGMA user_version = 2`)
	return err
}

// migrateV1 переходит на 3НФ-схему с отдельной таблицей content.
// Если существует старая схема — данные переносятся с вычислением хэшей.
func (d *DB) migrateV1() error {
	// Читаем старые данные до транзакции, пока таблица ещё существует.
	type oldRow struct {
		content   string
		pinned    int
		createdAt string
	}
	var oldRows []oldRow

	if d.columnExists("clips", "content") {
		rows, err := d.db.Query(`SELECT content, pinned, created_at FROM clips`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var r oldRow
				rows.Scan(&r.content, &r.pinned, &r.createdAt)
				oldRows = append(oldRows, r)
			}
		}
	}

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Сносим старую схему.
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_created`,
		`DROP INDEX IF EXISTS idx_pinned`,
		`DROP TABLE IF EXISTS clips`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}

	// Создаём новую схему.
	for _, stmt := range []string{
		`CREATE TABLE content (
			hash TEXT PRIMARY KEY,
			text TEXT NOT NULL
		)`,
		`CREATE TABLE clips (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			content_hash TEXT    NOT NULL UNIQUE REFERENCES content(hash),
			note         TEXT,
			section      INTEGER NOT NULL DEFAULT 0,
			pinned       INTEGER NOT NULL DEFAULT 0,
			created_at   TEXT    NOT NULL,
			updated_at   TEXT    NOT NULL
		)`,
		`CREATE INDEX idx_updated ON clips(updated_at DESC)`,
		`CREATE INDEX idx_pinned  ON clips(pinned DESC, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}

	// Переносим данные из старой схемы.
	for _, r := range oldRows {
		h := hashContent(r.content)
		if _, err := tx.Exec(`INSERT OR IGNORE INTO content(hash, text) VALUES(?, ?)`, h, r.content); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO clips(content_hash, pinned, created_at, updated_at) VALUES(?, ?, ?, ?)`,
			h, r.pinned, r.createdAt, r.createdAt,
		); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	_, err = d.db.Exec(`PRAGMA user_version = 1`)
	return err
}

func (d *DB) columnExists(table, column string) bool {
	rows, err := d.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			continue
		}
		if name == column {
			return true
		}
	}
	return false
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func hashContent(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func scanItems(rows *sql.Rows) ([]proto.Item, error) {
	defer rows.Close()
	var items []proto.Item
	for rows.Next() {
		var it proto.Item
		var pinned int
		var hidden int
		if err := rows.Scan(&it.ID, &it.Content, &pinned, &it.CreatedAt, &it.Note, &hidden); err != nil {
			return nil, err
		}
		it.Pinned = pinned != 0
		it.Hidden = hidden != 0
		items = append(items, it)
	}
	return items, rows.Err()
}

// ── Public API ────────────────────────────────────────────────────────────────

func (d *DB) Insert(content string) error {
	hash := hashContent(content)
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT OR IGNORE INTO content(hash, text) VALUES(?, ?)`, hash, content); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO clips(content_hash, created_at, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(content_hash) DO UPDATE SET updated_at = excluded.updated_at`,
		hash, now, now,
	); err != nil {
		return err
	}

	return tx.Commit()
}

func (d *DB) List(limit, offset int) ([]proto.Item, error) {
	rows, err := d.db.Query(`
		SELECT c.id, ct.text, c.pinned, c.updated_at, COALESCE(c.note, ''), c.hidden
		FROM clips c JOIN content ct ON c.content_hash = ct.hash
		ORDER BY c.pinned DESC, c.updated_at DESC
		LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}

func (d *DB) Search(query string, limit, offset int) ([]proto.Item, error) {
	rows, err := d.db.Query(`
		SELECT c.id, ct.text, c.pinned, c.updated_at, COALESCE(c.note, ''), c.hidden
		FROM clips c JOIN content ct ON c.content_hash = ct.hash
		WHERE LOWER(ct.text) LIKE '%' || LOWER(?) || '%'
		ORDER BY c.pinned DESC, c.updated_at DESC
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

func (d *DB) SetNote(id int64, note string) error {
	_, err := d.db.Exec(`UPDATE clips SET note = ? WHERE id = ?`, note, id)
	return err
}

func (d *DB) SetHidden(id int64, hidden bool) error {
	v := 0
	if hidden {
		v = 1
	}
	_, err := d.db.Exec(`UPDATE clips SET hidden = ? WHERE id = ?`, v, id)
	return err
}

func (d *DB) GetContent(id int64) (string, error) {
	var text string
	err := d.db.QueryRow(`
		SELECT ct.text FROM clips c
		JOIN content ct ON c.content_hash = ct.hash
		WHERE c.id = ?`, id,
	).Scan(&text)
	return text, err
}
