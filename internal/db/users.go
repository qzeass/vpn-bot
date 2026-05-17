package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type User struct {
	ID                 int64
	TelegramID         int64
	Username           string
	Balance            float64
	VlessUUID          string
	XUIEmail           string
	IsBanned           bool
	FreeLimitBytes     int64
	UnlimitedExpiresAt int64
	ReferrerID         int64
	DailyUsedBytes     int64
	LastTotalBytes     int64
	LastResetDate      string
	CreatedAt          int64
}

func (u *User) HasUnlimited() bool {
	return u.UnlimitedExpiresAt > time.Now().Unix()
}

func (u *User) FreeLimitExceeded() bool {
	return u.DailyUsedBytes >= u.FreeLimitBytes
}

func (d *DB) GetUserByTelegramID(tgID int64) (*User, error) {
	row := d.conn.QueryRow(`
		SELECT id, telegram_id, username, balance, vless_uuid, xui_email, is_banned,
		       free_limit_bytes, unlimited_expires_at, referrer_id,
		       daily_used_bytes, last_total_bytes, last_reset_date, created_at
		FROM users WHERE telegram_id = ?`, tgID)
	return scanUser(row)
}

func (d *DB) GetUserByID(id int64) (*User, error) {
	row := d.conn.QueryRow(`
		SELECT id, telegram_id, username, balance, vless_uuid, xui_email, is_banned,
		       free_limit_bytes, unlimited_expires_at, referrer_id,
		       daily_used_bytes, last_total_bytes, last_reset_date, created_at
		FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func (d *DB) GetAllActiveUsers() ([]*User, error) {
	rows, err := d.conn.Query(`
		SELECT id, telegram_id, username, balance, vless_uuid, xui_email, is_banned,
		       free_limit_bytes, unlimited_expires_at, referrer_id,
		       daily_used_bytes, last_total_bytes, last_reset_date, created_at
		FROM users WHERE is_banned = 0 AND vless_uuid != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsers(rows)
}

func (d *DB) GetAllUsers() ([]*User, error) {
	rows, err := d.conn.Query(`
		SELECT id, telegram_id, username, balance, vless_uuid, xui_email, is_banned,
		       free_limit_bytes, unlimited_expires_at, referrer_id,
		       daily_used_bytes, last_total_bytes, last_reset_date, created_at
		FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsers(rows)
}

func (d *DB) CreateUser(tgID int64, username string, referrerID int64, freeLimitBytes int64) (*User, error) {
	now := nowUnix()
	today := time.Now().Format("2006-01-02")
	res, err := d.conn.Exec(`
		INSERT INTO users (telegram_id, username, balance, vless_uuid, xui_email, is_banned,
		                   free_limit_bytes, unlimited_expires_at, referrer_id,
		                   daily_used_bytes, last_total_bytes, last_reset_date, created_at)
		VALUES (?, ?, 0, '', '', 0, ?, 0, ?, 0, 0, ?, ?)`,
		tgID, username, freeLimitBytes, referrerID, today, now)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}
	id, _ := res.LastInsertId()
	return &User{
		ID:             id,
		TelegramID:     tgID,
		Username:       username,
		FreeLimitBytes: freeLimitBytes,
		ReferrerID:     referrerID,
		LastResetDate:  today,
		CreatedAt:      now,
	}, nil
}

func (d *DB) UpdateUserVPN(tgID int64, uuid, email string) error {
	_, err := d.conn.Exec(`UPDATE users SET vless_uuid = ?, xui_email = ? WHERE telegram_id = ?`,
		uuid, email, tgID)
	return err
}

func (d *DB) UpdateUserBalance(tgID int64, delta float64) error {
	_, err := d.conn.Exec(`UPDATE users SET balance = balance + ? WHERE telegram_id = ?`, delta, tgID)
	return err
}

func (d *DB) SetUserBalance(tgID int64, balance float64) error {
	_, err := d.conn.Exec(`UPDATE users SET balance = ? WHERE telegram_id = ?`, balance, tgID)
	return err
}

func (d *DB) SetUserBanned(tgID int64, banned bool) error {
	v := 0
	if banned {
		v = 1
	}
	_, err := d.conn.Exec(`UPDATE users SET is_banned = ? WHERE telegram_id = ?`, v, tgID)
	return err
}

func (d *DB) SetUserFreeLimitBytes(tgID int64, bytes int64) error {
	_, err := d.conn.Exec(`UPDATE users SET free_limit_bytes = ? WHERE telegram_id = ?`, bytes, tgID)
	return err
}

func (d *DB) SetGlobalFreeLimitBytes(bytes int64) error {
	_, err := d.conn.Exec(`UPDATE users SET free_limit_bytes = ?`, bytes)
	return err
}

func (d *DB) SetUserUnlimited(tgID int64, expiresAt int64) error {
	_, err := d.conn.Exec(`UPDATE users SET unlimited_expires_at = ? WHERE telegram_id = ?`, expiresAt, tgID)
	return err
}

func (d *DB) UpdateUserTrafficAndBalance(tgID int64, dailyUsed, lastTotal int64, balanceDelta float64) error {
	_, err := d.conn.Exec(`
		UPDATE users
		SET daily_used_bytes = ?, last_total_bytes = ?, balance = balance + ?
		WHERE telegram_id = ?`,
		dailyUsed, lastTotal, balanceDelta, tgID)
	return err
}

func (d *DB) ResetDailyTraffic(tgID int64, lastTotal int64, today string) error {
	_, err := d.conn.Exec(`
		UPDATE users SET daily_used_bytes = 0, last_reset_date = ?, last_total_bytes = ?
		WHERE telegram_id = ?`, today, lastTotal, tgID)
	return err
}

func (d *DB) GetReferrer(referrerID int64) (*User, error) {
	if referrerID == 0 {
		return nil, nil
	}
	row := d.conn.QueryRow(`
		SELECT id, telegram_id, username, balance, vless_uuid, xui_email, is_banned,
		       free_limit_bytes, unlimited_expires_at, referrer_id,
		       daily_used_bytes, last_total_bytes, last_reset_date, created_at
		FROM users WHERE id = ?`, referrerID)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

func (d *DB) CountUsers() (int64, error) {
	var n int64
	err := d.conn.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (d *DB) AddPayment(userID int64, amount float64, method string) error {
	_, err := d.conn.Exec(`
		INSERT INTO payments (user_id, amount, method, created_at) VALUES (?, ?, ?, ?)`,
		userID, amount, method, nowUnix())
	return err
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	var banned int
	err := row.Scan(
		&u.ID, &u.TelegramID, &u.Username, &u.Balance,
		&u.VlessUUID, &u.XUIEmail, &banned,
		&u.FreeLimitBytes, &u.UnlimitedExpiresAt, &u.ReferrerID,
		&u.DailyUsedBytes, &u.LastTotalBytes, &u.LastResetDate, &u.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	u.IsBanned = banned == 1
	return &u, nil
}

func scanUsers(rows *sql.Rows) ([]*User, error) {
	var users []*User
	for rows.Next() {
		var u User
		var banned int
		err := rows.Scan(
			&u.ID, &u.TelegramID, &u.Username, &u.Balance,
			&u.VlessUUID, &u.XUIEmail, &banned,
			&u.FreeLimitBytes, &u.UnlimitedExpiresAt, &u.ReferrerID,
			&u.DailyUsedBytes, &u.LastTotalBytes, &u.LastResetDate, &u.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		u.IsBanned = banned == 1
		users = append(users, &u)
	}
	return users, rows.Err()
}
