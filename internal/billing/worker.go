package billing

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/qzeass/vpn-bot/internal/config"
	"github.com/qzeass/vpn-bot/internal/db"
	"github.com/qzeass/vpn-bot/internal/payments"
	"github.com/qzeass/vpn-bot/internal/xui"
)

var digitRe = regexp.MustCompile(`\d{5,12}`)

type NotifyFunc func(telegramID int64, msg string)

type Worker struct {
	db              *db.DB
	xui             *xui.Client
	cfg             *config.Config
	ton             *payments.TONChecker
	da              *payments.DonationAlertsClient
	notify          NotifyFunc
	processed       map[string]struct{}
	daProcessed     map[int64]struct{}
}

func New(database *db.DB, xuiClient *xui.Client, cfg *config.Config, tonChecker *payments.TONChecker, daClient *payments.DonationAlertsClient, notify NotifyFunc) *Worker {
	return &Worker{
		db:          database,
		xui:         xuiClient,
		cfg:         cfg,
		ton:         tonChecker,
		da:          daClient,
		notify:      notify,
		processed:   make(map[string]struct{}),
		daProcessed: make(map[int64]struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	billingInterval := time.Duration(w.cfg.Billing.WorkerIntervalMin) * time.Minute
	tonInterval := time.Duration(w.cfg.Billing.TONCheckerIntervalSec) * time.Second
	daInterval := time.Duration(w.cfg.Billing.DACheckerIntervalSec) * time.Second

	billingTicker := time.NewTicker(billingInterval)
	tonTicker := time.NewTicker(tonInterval)
	daTicker := time.NewTicker(daInterval)
	midnightTicker := time.NewTicker(1 * time.Minute)

	defer billingTicker.Stop()
	defer tonTicker.Stop()
	defer daTicker.Stop()
	defer midnightTicker.Stop()

	if err := w.loadProcessedHashes(ctx); err != nil {
		log.Printf("[billing] failed to load processed hashes: %v", err)
	}
	if err := w.loadProcessedDAIDs(ctx); err != nil {
		log.Printf("[billing] failed to load processed DA IDs: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-billingTicker.C:
			if err := w.runBilling(ctx); err != nil {
				log.Printf("[billing] billing run error: %v", err)
			}
		case <-tonTicker.C:
			if err := w.runTONCheck(ctx); err != nil {
				log.Printf("[billing] TON check error: %v", err)
			}
		case <-daTicker.C:
			if w.da.Enabled() {
				if err := w.runDACheck(ctx); err != nil {
					log.Printf("[billing] DA check error: %v", err)
				}
			}
		case <-midnightTicker.C:
			w.checkMidnightReset(ctx)
		}
	}
}

func (w *Worker) loadProcessedHashes(ctx context.Context) error {
	rows, err := w.db.Conn().QueryContext(ctx, `SELECT hash FROM ton_processed_txs`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return err
		}
		w.processed[hash] = struct{}{}
	}
	return rows.Err()
}

func (w *Worker) loadProcessedDAIDs(ctx context.Context) error {
	rows, err := w.db.Conn().QueryContext(ctx, `SELECT id FROM da_processed_donations`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		w.daProcessed[id] = struct{}{}
	}
	return rows.Err()
}

func (w *Worker) runBilling(ctx context.Context) error {
	trafficMap, err := w.xui.GetAllClientTraffic()
	if err != nil {
		return fmt.Errorf("get all traffic: %w", err)
	}

	users, err := w.db.GetAllActiveUsers()
	if err != nil {
		return fmt.Errorf("get active users: %w", err)
	}

	today := time.Now().Format("2006-01-02")

	for _, user := range users {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if user.HasUnlimited() {
			continue
		}

		currentTotal, ok := trafficMap[user.XUIEmail]
		if !ok {
			continue
		}

		var lastTotal int64
		if user.LastResetDate != today {
			lastTotal = currentTotal
			if err := w.db.ResetDailyTraffic(user.TelegramID, currentTotal, today); err != nil {
				log.Printf("[billing] reset daily traffic for %d: %v", user.TelegramID, err)
			}
			continue
		} else {
			lastTotal = user.LastTotalBytes
		}

		delta := currentTotal - lastTotal
		if delta <= 0 {
			continue
		}

		newDailyUsed := user.DailyUsedBytes + delta
		balanceDelta := 0.0

		if newDailyUsed > user.FreeLimitBytes {
			overDelta := newDailyUsed - user.FreeLimitBytes
			if user.DailyUsedBytes < user.FreeLimitBytes {
				overDelta = newDailyUsed - user.FreeLimitBytes
			} else {
				overDelta = delta
			}
			overGB := float64(overDelta) / 1e9
			cost := overGB * w.cfg.Billing.OverdraftCostPerGB
			balanceDelta = -cost
		}

		newBalance := user.Balance + balanceDelta
		if err := w.db.UpdateUserTrafficAndBalance(user.TelegramID, newDailyUsed, currentTotal, balanceDelta); err != nil {
			log.Printf("[billing] update traffic for %d: %v", user.TelegramID, err)
			continue
		}

		if newBalance <= 0 && newDailyUsed > user.FreeLimitBytes {
			if err := w.xui.SetClientEnabled(user.VlessUUID, user.XUIEmail, false); err != nil {
				log.Printf("[billing] disable client %d: %v", user.TelegramID, err)
			}
			w.notify(user.TelegramID, "VPN отключен: баланс исчерпан и бесплатный лимит превышен. Пополните баланс для восстановления доступа.")
		}
	}

	return nil
}

func (w *Worker) checkMidnightReset(ctx context.Context) {
	now := time.Now()
	if now.Hour() != 0 || now.Minute() != 0 {
		return
	}

	trafficMap, err := w.xui.GetAllClientTraffic()
	if err != nil {
		log.Printf("[billing] midnight reset: get traffic: %v", err)
		return
	}

	users, err := w.db.GetAllActiveUsers()
	if err != nil {
		log.Printf("[billing] midnight reset: get users: %v", err)
		return
	}

	today := now.Format("2006-01-02")

	for _, user := range users {
		select {
		case <-ctx.Done():
			return
		default:
		}

		currentTotal := trafficMap[user.XUIEmail]

		if err := w.db.ResetDailyTraffic(user.TelegramID, currentTotal, today); err != nil {
			log.Printf("[billing] midnight reset for %d: %v", user.TelegramID, err)
			continue
		}

		if user.IsBanned {
			continue
		}

		if !user.HasUnlimited() && user.Balance <= 0 && user.DailyUsedBytes > user.FreeLimitBytes {
			if err := w.xui.SetClientEnabled(user.VlessUUID, user.XUIEmail, true); err != nil {
				log.Printf("[billing] midnight re-enable %d: %v", user.TelegramID, err)
			} else {
				w.notify(user.TelegramID, "VPN восстановлен. Суточный лимит сброшен в 00:00.")
			}
		}

		if !user.HasUnlimited() && user.UnlimitedExpiresAt > 0 {
			dayAgo := now.Unix() - 86400
			if user.UnlimitedExpiresAt >= dayAgo && user.UnlimitedExpiresAt <= now.Unix() {
				w.notify(user.TelegramID, "Безлимитная подписка истекла. Вы переведены на бесплатный тариф.")
			}
		}
	}
}

func (w *Worker) runTONCheck(ctx context.Context) error {
	tonPayments, _, err := w.ton.GetNewTONTransactions(0)
	if err != nil {
		return fmt.Errorf("TON transactions: %w", err)
	}

	usdtPayments, err := w.ton.GetNewJettonTransfers(w.processed)
	if err != nil {
		log.Printf("[billing] USDT transfers error: %v", err)
	}

	allPayments := append(tonPayments, usdtPayments...)

	for _, p := range allPayments {
		if _, seen := w.processed[p.Hash]; seen {
			continue
		}

		var exists int
		err := w.db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM ton_processed_txs WHERE hash = ?`, p.Hash).Scan(&exists)
		if err != nil || exists > 0 {
			w.processed[p.Hash] = struct{}{}
			continue
		}

		user, err := w.db.GetUserByID(p.UserID)
		if err == sql.ErrNoRows || user == nil {
			w.processed[p.Hash] = struct{}{}
			w.saveTXHash(ctx, p.Hash)
			continue
		}
		if err != nil {
			log.Printf("[billing] get user by id %d: %v", p.UserID, err)
			continue
		}

		rounded := math.Floor(p.AmountRUB*100) / 100

		if err := w.db.UpdateUserBalance(user.TelegramID, rounded); err != nil {
			log.Printf("[billing] update balance for %d: %v", user.TelegramID, err)
			continue
		}

		if err := w.db.AddPayment(user.ID, rounded, "ton_crypto"); err != nil {
			log.Printf("[billing] add payment for %d: %v", user.ID, err)
		}

		if err := w.applyReferralBonus(user, rounded); err != nil {
			log.Printf("[billing] referral bonus for %d: %v", user.TelegramID, err)
		}

		if !user.IsBanned && user.Balance <= 0 && user.DailyUsedBytes > user.FreeLimitBytes {
			if err := w.xui.SetClientEnabled(user.VlessUUID, user.XUIEmail, true); err != nil {
				log.Printf("[billing] re-enable vpn for %d: %v", user.TelegramID, err)
			}
		}

		currency := "TON"
		if p.IsUSDT {
			currency = "USDT"
		}
		w.notify(user.TelegramID, fmt.Sprintf("Баланс пополнен на %.2f руб. (оплата через %s)", rounded, currency))

		w.processed[p.Hash] = struct{}{}
		w.saveTXHash(ctx, p.Hash)
	}

	return nil
}

func (w *Worker) runDACheck(ctx context.Context) error {
	donations, err := w.da.GetRecentDonations()
	if err != nil {
		return fmt.Errorf("get DA donations: %w", err)
	}

	for _, d := range donations {
		if _, seen := w.daProcessed[d.ID]; seen {
			continue
		}

		var exists int
		w.db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM da_processed_donations WHERE id = ?`, d.ID).Scan(&exists)
		if exists > 0 {
			w.daProcessed[d.ID] = struct{}{}
			continue
		}

		tgID := extractTelegramID(d.Message)
		if tgID == 0 {
			w.daProcessed[d.ID] = struct{}{}
			w.saveDAID(ctx, d.ID)
			continue
		}

		user, err := w.db.GetUserByTelegramID(tgID)
		if err != nil || user == nil {
			log.Printf("[billing] DA donation %d: user %d not found", d.ID, tgID)
			w.daProcessed[d.ID] = struct{}{}
			w.saveDAID(ctx, d.ID)
			continue
		}

		credited := math.Floor(d.Amount*(1-w.cfg.Payments.DonationAlerts.CommissionRate)*100) / 100
		if credited <= 0 {
			w.daProcessed[d.ID] = struct{}{}
			w.saveDAID(ctx, d.ID)
			continue
		}

		if err := w.db.UpdateUserBalance(user.TelegramID, credited); err != nil {
			log.Printf("[billing] DA: update balance for %d: %v", user.TelegramID, err)
			continue
		}

		if err := w.db.AddPayment(user.ID, credited, "donation_alerts"); err != nil {
			log.Printf("[billing] DA: add payment for %d: %v", user.ID, err)
		}

		if err := w.applyReferralBonus(user, credited); err != nil {
			log.Printf("[billing] DA: referral bonus for %d: %v", user.TelegramID, err)
		}

		if !user.IsBanned && user.Balance <= 0 && user.DailyUsedBytes > user.FreeLimitBytes {
			_ = w.xui.SetClientEnabled(user.VlessUUID, user.XUIEmail, true)
		}

		w.notify(user.TelegramID, fmt.Sprintf("Баланс пополнен на %.2f руб. (DonationAlerts)", credited))

		w.daProcessed[d.ID] = struct{}{}
		w.saveDAID(ctx, d.ID)
	}

	return nil
}

func extractTelegramID(message string) int64 {
	matches := digitRe.FindAllString(message, -1)
	for _, m := range matches {
		n, err := strconv.ParseInt(m, 10, 64)
		if err == nil && n > 10000 {
			return n
		}
	}
	return 0
}

func (w *Worker) saveTXHash(ctx context.Context, hash string) {
	_, _ = w.db.Conn().ExecContext(ctx, `INSERT OR IGNORE INTO ton_processed_txs (hash) VALUES (?)`, hash)
}

func (w *Worker) saveDAID(ctx context.Context, id int64) {
	_, _ = w.db.Conn().ExecContext(ctx, `INSERT OR IGNORE INTO da_processed_donations (id) VALUES (?)`, id)
}

func (w *Worker) applyReferralBonus(user *db.User, amount float64) error {
	if user.ReferrerID == 0 {
		return nil
	}
	referrer, err := w.db.GetReferrer(user.ReferrerID)
	if err != nil || referrer == nil {
		return nil
	}
	bonus := amount * (w.cfg.Referral.RewardPercent / 100.0)
	if bonus <= 0 {
		return nil
	}
	if err := w.db.UpdateUserBalance(referrer.TelegramID, bonus); err != nil {
		return fmt.Errorf("update referrer balance: %w", err)
	}
	w.notify(referrer.TelegramID, fmt.Sprintf("Реферальный бонус: +%.2f руб.", bonus))
	return nil
}

func (w *Worker) ApplyDonationAlertsPayment(ctx context.Context, userTgID int64, amount float64) error {
	user, err := w.db.GetUserByTelegramID(userTgID)
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	if err := w.db.UpdateUserBalance(userTgID, amount); err != nil {
		return fmt.Errorf("update balance: %w", err)
	}

	if err := w.db.AddPayment(user.ID, amount, "donation_alerts_manual"); err != nil {
		log.Printf("[billing] add payment log: %v", err)
	}

	if err := w.applyReferralBonus(user, amount); err != nil {
		log.Printf("[billing] referral bonus: %v", err)
	}

	if !user.IsBanned && user.Balance <= 0 && user.DailyUsedBytes > user.FreeLimitBytes {
		if err := w.xui.SetClientEnabled(user.VlessUUID, user.XUIEmail, true); err != nil {
			log.Printf("[billing] re-enable vpn: %v", err)
		}
	}

	return nil
}
