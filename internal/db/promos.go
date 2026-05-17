package db

import (
	"database/sql"
	"errors"
	"fmt"
)

const (
	PromoTypeBalance      = "balance"
	PromoTypeSubscription = "subscription"
)

type PromoCode struct {
	ID               int64
	Code             string
	PromoType        string
	BalanceAmount    float64
	SubscriptionDays int64
	MaxActivations   int64
	Activations      int64
	ChannelID        int64
	IsActive         bool
	CreatedByUserID  int64
	CreatedAt        int64
}

func (p *PromoCode) IsUnlimited() bool {
	return p.MaxActivations == 0
}

func (d *DB) GetPromoCode(code string) (*PromoCode, error) {
	row := d.conn.QueryRow(`
		SELECT id, code, promo_type, balance_amount, subscription_days, max_activations,
		       activations, channel_id, is_active, created_by_user_id, created_at
		FROM promo_codes WHERE code = ?`, code)
	return scanPromo(row)
}

func (d *DB) CreatePromoCode(code, promoType string, balanceAmount float64, subscriptionDays, maxAct, channelID, createdByUserID int64) error {
	_, err := d.conn.Exec(`
		INSERT INTO promo_codes
		  (code, promo_type, balance_amount, subscription_days, max_activations,
		   activations, channel_id, is_active, created_by_user_id, created_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, 1, ?, ?)`,
		code, promoType, balanceAmount, subscriptionDays, maxAct, channelID, createdByUserID, nowUnix())
	return err
}

func (d *DB) DeletePromoCode(code string) error {
	_, err := d.conn.Exec(`UPDATE promo_codes SET is_active = 0 WHERE code = ?`, code)
	return err
}

func (d *DB) HasUserActivatedPromo(userID, promoID int64) (bool, error) {
	var n int
	err := d.conn.QueryRow(`
		SELECT COUNT(*) FROM promo_activations WHERE user_id = ? AND promo_id = ?`,
		userID, promoID).Scan(&n)
	return n > 0, err
}

func (d *DB) ActivatePromo(userID, promoID int64) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO promo_activations (promo_id, user_id, activated_at) VALUES (?, ?, ?)`,
		promoID, userID, nowUnix())
	if err != nil {
		return fmt.Errorf("insert activation: %w", err)
	}

	_, err = tx.Exec(`UPDATE promo_codes SET activations = activations + 1 WHERE id = ?`, promoID)
	if err != nil {
		return fmt.Errorf("increment activations: %w", err)
	}

	return tx.Commit()
}

func (d *DB) ListPromoCodes() ([]*PromoCode, error) {
	rows, err := d.conn.Query(`
		SELECT id, code, promo_type, balance_amount, subscription_days, max_activations,
		       activations, channel_id, is_active, created_by_user_id, created_at
		FROM promo_codes ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var promos []*PromoCode
	for rows.Next() {
		p, err := scanPromoRows(rows)
		if err != nil {
			return nil, err
		}
		promos = append(promos, p)
	}
	return promos, rows.Err()
}

func (d *DB) ListUserPromoCodes(userID int64) ([]*PromoCode, error) {
	rows, err := d.conn.Query(`
		SELECT id, code, promo_type, balance_amount, subscription_days, max_activations,
		       activations, channel_id, is_active, created_by_user_id, created_at
		FROM promo_codes WHERE created_by_user_id = ? ORDER BY created_at DESC LIMIT 20`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var promos []*PromoCode
	for rows.Next() {
		p, err := scanPromoRows(rows)
		if err != nil {
			return nil, err
		}
		promos = append(promos, p)
	}
	return promos, rows.Err()
}

func scanPromo(row *sql.Row) (*PromoCode, error) {
	var p PromoCode
	var isActive int
	err := row.Scan(&p.ID, &p.Code, &p.PromoType, &p.BalanceAmount, &p.SubscriptionDays,
		&p.MaxActivations, &p.Activations, &p.ChannelID, &isActive, &p.CreatedByUserID, &p.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	p.IsActive = isActive == 1
	return &p, nil
}

func scanPromoRows(rows *sql.Rows) (*PromoCode, error) {
	var p PromoCode
	var isActive int
	err := rows.Scan(&p.ID, &p.Code, &p.PromoType, &p.BalanceAmount, &p.SubscriptionDays,
		&p.MaxActivations, &p.Activations, &p.ChannelID, &isActive, &p.CreatedByUserID, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	p.IsActive = isActive == 1
	return &p, nil
}
