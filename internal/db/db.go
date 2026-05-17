package db

import (
        "database/sql"
        "fmt"
        "time"

        _ "github.com/ncruces/go-sqlite3/driver"
        _ "github.com/ncruces/go-sqlite3/embed"
)

type DB struct {
        conn *sql.DB
}

func New(path string) (*DB, error) {
        conn, err := sql.Open("sqlite3", path)
        if err != nil {
                return nil, fmt.Errorf("open sqlite: %w", err)
        }

        conn.SetMaxOpenConns(1)
        conn.SetMaxIdleConns(1)
        conn.SetConnMaxLifetime(0)

        pragmas := []string{
                "PRAGMA journal_mode=WAL;",
                "PRAGMA synchronous=NORMAL;",
                "PRAGMA foreign_keys=ON;",
                "PRAGMA busy_timeout=5000;",
                "PRAGMA cache_size=-2000;",
        }
        for _, p := range pragmas {
                if _, err := conn.Exec(p); err != nil {
                        return nil, fmt.Errorf("pragma %q: %w", p, err)
                }
        }

        d := &DB{conn: conn}
        if err := d.migrate(); err != nil {
                return nil, fmt.Errorf("migrate: %w", err)
        }

        return d, nil
}

func (d *DB) Close() error {
        return d.conn.Close()
}

func (d *DB) migrate() error {
        schema := `
CREATE TABLE IF NOT EXISTS users (
        id                    INTEGER PRIMARY KEY AUTOINCREMENT,
        telegram_id           INTEGER NOT NULL UNIQUE,
        username              TEXT NOT NULL DEFAULT '',
        balance               REAL NOT NULL DEFAULT 0,
        vless_uuid            TEXT NOT NULL DEFAULT '',
        xui_email             TEXT NOT NULL DEFAULT '',
        is_banned             INTEGER NOT NULL DEFAULT 0,
        free_limit_bytes      INTEGER NOT NULL DEFAULT 3221225472,
        unlimited_expires_at  INTEGER NOT NULL DEFAULT 0,
        referrer_id           INTEGER NOT NULL DEFAULT 0,
        daily_used_bytes      INTEGER NOT NULL DEFAULT 0,
        last_total_bytes      INTEGER NOT NULL DEFAULT 0,
        last_reset_date       TEXT NOT NULL DEFAULT '',
        created_at            INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS promo_codes (
        id                  INTEGER PRIMARY KEY AUTOINCREMENT,
        code                TEXT NOT NULL UNIQUE,
        promo_type          TEXT NOT NULL DEFAULT 'balance',
        balance_amount      REAL NOT NULL DEFAULT 0,
        subscription_days   INTEGER NOT NULL DEFAULT 30,
        max_activations     INTEGER NOT NULL DEFAULT 1,
        activations         INTEGER NOT NULL DEFAULT 0,
        channel_id          INTEGER NOT NULL DEFAULT 0,
        is_active           INTEGER NOT NULL DEFAULT 1,
        created_by_user_id  INTEGER NOT NULL DEFAULT 0,
        created_at          INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS promo_activations (
        id           INTEGER PRIMARY KEY AUTOINCREMENT,
        promo_id     INTEGER NOT NULL REFERENCES promo_codes(id),
        user_id      INTEGER NOT NULL,
        activated_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS ton_processed_txs (
        hash TEXT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS da_processed_donations (
        id INTEGER PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS payments (
        id         INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id    INTEGER NOT NULL,
        amount     REAL NOT NULL,
        method     TEXT NOT NULL,
        created_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS settings (
        key   TEXT PRIMARY KEY,
        value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_users_telegram_id ON users(telegram_id);
CREATE INDEX IF NOT EXISTS idx_promo_activations_user ON promo_activations(user_id, promo_id);
CREATE INDEX IF NOT EXISTS idx_payments_user ON payments(user_id);
CREATE INDEX IF NOT EXISTS idx_promos_creator ON promo_codes(created_by_user_id);
`
        if _, err := d.conn.Exec(schema); err != nil {
                return err
        }

        migrations := []string{
                `ALTER TABLE promo_codes ADD COLUMN promo_type TEXT NOT NULL DEFAULT 'balance'`,
                `ALTER TABLE promo_codes ADD COLUMN subscription_days INTEGER NOT NULL DEFAULT 30`,
                `ALTER TABLE promo_codes ADD COLUMN created_by_user_id INTEGER NOT NULL DEFAULT 0`,
        }
        for _, m := range migrations {
                d.conn.Exec(m)
        }

        return nil
}

func (d *DB) Conn() *sql.DB {
        return d.conn
}

func nowUnix() int64 {
        return time.Now().Unix()
}
