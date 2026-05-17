package bot

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/qzeass/vpn-bot/internal/db"
)

type userSession struct {
	state string
	data  map[string]string
}

var (
	sessions   = make(map[int64]*userSession)
	sessionsMu sync.Mutex
)

func getSession(tgID int64) *userSession {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	if sessions[tgID] == nil {
		sessions[tgID] = &userSession{data: make(map[string]string)}
	}
	return sessions[tgID]
}

func setState(tgID int64, state string) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	if sessions[tgID] == nil {
		sessions[tgID] = &userSession{data: make(map[string]string)}
	}
	sessions[tgID].state = state
}

func setStateData(tgID int64, state string, data map[string]string) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	sessions[tgID] = &userSession{state: state, data: data}
}

func clearState(tgID int64) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	if sessions[tgID] != nil {
		sessions[tgID].state = ""
		sessions[tgID].data = make(map[string]string)
	}
}

func randomCode() string {
	b := make([]byte, 4)
	rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	tgID := msg.From.ID
	text := strings.TrimSpace(msg.Text)

	if strings.HasPrefix(text, "/start") {
		b.handleStart(ctx, msg, text)
		return
	}

	if b.cfg.IsAdmin(tgID) && b.handleAdminMessage(ctx, msg) {
		return
	}

	sess := getSession(tgID)
	switch sess.state {
	case "await_promo_activate":
		b.deleteMsg(msg.Chat.ID, msg.MessageID)
		b.handlePromoActivation(ctx, tgID, text)
		clearState(tgID)

	case "await_da_amount":
		b.deleteMsg(msg.Chat.ID, msg.MessageID)
		b.handleDAAmountInput(ctx, tgID, text)

	case "await_promo_create_amount":
		b.deleteMsg(msg.Chat.ID, msg.MessageID)
		b.handleUserPromoCreateAmount(ctx, tgID, text)

	case "await_promo_create_subdays":
		b.deleteMsg(msg.Chat.ID, msg.MessageID)
		b.handleUserPromoCreateSubDays(ctx, tgID, text)

	case "await_promo_create_maxact":
		b.deleteMsg(msg.Chat.ID, msg.MessageID)
		b.handleUserPromoCreateMaxAct(ctx, tgID, text)

	default:
		b.deleteMsg(msg.Chat.ID, msg.MessageID)
		b.showMainMenu(ctx, tgID, msg.From.UserName)
	}
}

func (b *Bot) handleStart(ctx context.Context, msg *tgbotapi.Message, text string) {
	tgID := msg.From.ID
	username := msg.From.UserName

	b.deleteMsg(msg.Chat.ID, msg.MessageID)

	var referrerID int64
	parts := strings.Fields(text)
	if len(parts) > 1 {
		refTgID, err := strconv.ParseInt(parts[1], 10, 64)
		if err == nil && refTgID != tgID {
			ref, err := b.db.GetUserByTelegramID(refTgID)
			if err == nil && ref != nil {
				referrerID = ref.ID
			}
		}
	}

	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil && err != sql.ErrNoRows {
		return
	}

	if user == nil || err == sql.ErrNoRows {
		freeLimitBytes := int64(b.cfg.Billing.FreeLimitGB * 1e9)
		user, err = b.db.CreateUser(tgID, username, referrerID, freeLimitBytes)
		if err != nil {
			return
		}
		xuiEmail := fmt.Sprintf("tg_%d", tgID)
		clientUUID := uuid.New().String()
		if err := b.xui.AddClient(clientUUID, xuiEmail); err != nil {
			b.pushNotice(tgID, "Ошибка создания VPN аккаунта. Обратитесь к администратору.")
		} else {
			_ = b.db.UpdateUserVPN(tgID, clientUUID, xuiEmail)
			user.VlessUUID = clientUUID
			user.XUIEmail = xuiEmail
		}
		b.pushNotice(tgID, fmt.Sprintf(
			"Добро пожаловать!\n\nВам доступен бесплатный лимит <b>%.0f ГБ/сутки</b>.",
			b.cfg.Billing.FreeLimitGB,
		))
	}

	b.showMainMenu(ctx, tgID, username)
}

func (b *Bot) showMainMenu(ctx context.Context, tgID int64, username string) {
	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}

	status := "Активен"
	if user.IsBanned {
		status = "Заблокирован"
	} else if !user.HasUnlimited() && user.DailyUsedBytes >= user.FreeLimitBytes && user.Balance <= 0 {
		status = "Лимит исчерпан"
	}

	subLine := "нет"
	if user.HasUnlimited() {
		subLine = "до " + time.Unix(user.UnlimitedExpiresAt, 0).Format("02.01.2006")
	}

	usedGB := float64(user.DailyUsedBytes) / 1e9
	freeGB := float64(user.FreeLimitBytes) / 1e9

	name := username
	if name == "" {
		name = "пользователь"
	}

	text := fmt.Sprintf(
		"@%s\n"+
			"━━━━━━━━━━━━━━\n"+
			"Баланс: <b>%.2f руб.</b>\n"+
			"Статус: %s\n"+
			"Трафик сегодня: <b>%.2f / %.1f ГБ</b>\n"+
			"Подписка: %s\n"+
			"━━━━━━━━━━━━━━",
		name, user.Balance, status, usedGB, freeGB, subLine,
	)

	b.showMenu(tgID, text, mainMenu())
}

func (b *Bot) handleCallback(ctx context.Context, query *tgbotapi.CallbackQuery) {
	tgID := query.From.ID
	data := query.Data
	b.answerCallback(query, "")

	if b.cfg.IsAdmin(tgID) && strings.HasPrefix(data, "admin_") {
		b.handleAdminCallback(ctx, query)
		return
	}

	switch data {
	case "menu":
		clearState(tgID)
		b.showMainMenu(ctx, tgID, query.From.UserName)
	case "vpn_key":
		b.showVPNKey(ctx, tgID)
	case "balance":
		b.showBalance(ctx, tgID)
	case "stats":
		b.showStats(ctx, tgID)
	case "unlimited":
		b.showUnlimited(ctx, tgID)
	case "unlimited_buy":
		b.doBuyUnlimited(ctx, tgID)
	case "topup":
		b.showTopup(tgID)
	case "topup_ton":
		b.showTONInfo(tgID)
	case "topup_da":
		b.showDAInput(tgID)
	case "promos":
		b.showPromosMenu(ctx, tgID)
	case "promo_activate":
		b.showPromoActivateInput(tgID)
	case "promo_create":
		b.showPromoCreateTypeSelect(tgID)
	case "promo_create_balance":
		b.startPromoCreateBalance(tgID)
	case "promo_create_sub":
		b.startPromoCreateSubscription(tgID)
	case "promo_mylist":
		b.showMyPromos(ctx, tgID)
	case "referral":
		b.showReferral(ctx, tgID)
	}
}

func (b *Bot) showVPNKey(ctx context.Context, tgID int64) {
	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}
	if user.IsBanned {
		b.showMenu(tgID, "Ваш аккаунт заблокирован.", tgbotapi.NewInlineKeyboardMarkup(backRow()))
		return
	}
	if user.VlessUUID == "" {
		b.showMenu(tgID, "VPN ключ не создан. Напишите /start", tgbotapi.NewInlineKeyboardMarkup(backRow()))
		return
	}

	link, err := b.xui.BuildVlessLink(user.VlessUUID, fmt.Sprintf("tg_%d", tgID))
	if err != nil {
		b.showMenu(tgID, "Ошибка получения ключа. Попробуйте позже.", tgbotapi.NewInlineKeyboardMarkup(backRow()))
		return
	}

	text := fmt.Sprintf(
		"<b>Ваш VLESS ключ</b>\n\n"+
			"<code>%s</code>\n\n"+
			"Вставьте ключ в приложение:\n"+
			"v2rayNG / Streisand / Hiddify / NekoBox",
		link,
	)
	b.showMenu(tgID, text, tgbotapi.NewInlineKeyboardMarkup(backRow()))
}

func (b *Bot) showBalance(ctx context.Context, tgID int64) {
	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}

	unlimitedLine := "не активна"
	if user.HasUnlimited() {
		unlimitedLine = "до " + time.Unix(user.UnlimitedExpiresAt, 0).Format("02.01.2006")
	}

	text := fmt.Sprintf(
		"<b>Баланс и тарифы</b>\n\n"+
			"Баланс: <b>%.2f руб.</b>\n"+
			"Безлимитная подписка: %s\n\n"+
			"Тарифы:\n"+
			"  Бесплатно: %.0f ГБ/сутки\n"+
			"  Овердрафт: %.0f руб./ГБ\n"+
			"  Безлимит: %.0f руб./месяц",
		user.Balance, unlimitedLine,
		b.cfg.Billing.FreeLimitGB,
		b.cfg.Billing.OverdraftCostPerGB,
		b.cfg.Billing.UnlimitedPriceMonth,
	)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Пополнить", "topup"),
			tgbotapi.NewInlineKeyboardButtonData("Купить безлимит", "unlimited_buy"),
		),
		backRow(),
	)
	b.showMenu(tgID, text, keyboard)
}

func (b *Bot) showStats(ctx context.Context, tgID int64) {
	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}

	freeGB := float64(user.FreeLimitBytes) / 1e9
	usedGB := float64(user.DailyUsedBytes) / 1e9
	remaining := math.Max(0, freeGB-usedGB)

	status := "Активен"
	if user.IsBanned {
		status = "Заблокирован"
	} else if !user.HasUnlimited() && user.DailyUsedBytes >= user.FreeLimitBytes && user.Balance <= 0 {
		status = "Отключён (нет баланса)"
	}

	text := fmt.Sprintf(
		"<b>Статистика</b>\n\n"+
			"Статус: %s\n"+
			"Использовано: <b>%.2f ГБ</b> из %.0f ГБ\n"+
			"Осталось бесплатно: <b>%.2f ГБ</b>\n"+
			"Баланс: <b>%.2f руб.</b>\n\n"+
			"Сброс суточного лимита: в 00:00",
		status, usedGB, freeGB, remaining, user.Balance,
	)
	b.showMenu(tgID, text, tgbotapi.NewInlineKeyboardMarkup(backRow()))
}

func (b *Bot) showUnlimited(ctx context.Context, tgID int64) {
	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}

	var text string
	var keyboard tgbotapi.InlineKeyboardMarkup

	if user.HasUnlimited() {
		exp := time.Unix(user.UnlimitedExpiresAt, 0).Format("02.01.2006")
		text = fmt.Sprintf(
			"<b>Безлимитная подписка активна</b>\n\n"+
				"Действует до: <b>%s</b>\n\n"+
				"Трафик не тарифицируется, овердрафт не списывается.",
			exp,
		)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					fmt.Sprintf("Продлить (%.0f руб.)", b.cfg.Billing.UnlimitedPriceMonth),
					"unlimited_buy",
				),
			),
			backRow(),
		)
	} else {
		text = fmt.Sprintf(
			"<b>Безлимитная подписка</b>\n\n"+
				"Цена: <b>%.0f руб./месяц</b>\n\n"+
				"  Неограниченный трафик\n"+
				"  Без списания за овердрафт\n"+
				"  Срок: 30 дней\n\n"+
				"Ваш баланс: <b>%.2f руб.</b>",
			b.cfg.Billing.UnlimitedPriceMonth, user.Balance,
		)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					fmt.Sprintf("Купить за %.0f руб.", b.cfg.Billing.UnlimitedPriceMonth),
					"unlimited_buy",
				),
			),
			backRow(),
		)
	}
	b.showMenu(tgID, text, keyboard)
}

func (b *Bot) doBuyUnlimited(ctx context.Context, tgID int64) {
	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}

	price := b.cfg.Billing.UnlimitedPriceMonth
	if user.Balance < price {
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Пополнить баланс", "topup"),
			),
			backRow(),
		)
		b.showMenu(tgID, fmt.Sprintf(
			"<b>Недостаточно средств</b>\n\nНужно: %.0f руб.\nВаш баланс: %.2f руб.",
			price, user.Balance,
		), keyboard)
		return
	}

	if err := b.db.UpdateUserBalance(tgID, -price); err != nil {
		return
	}
	var newExpiry int64
	if user.HasUnlimited() {
		newExpiry = user.UnlimitedExpiresAt + 30*24*3600
	} else {
		newExpiry = time.Now().Unix() + 30*24*3600
	}
	if err := b.db.SetUserUnlimited(tgID, newExpiry); err != nil {
		_ = b.db.UpdateUserBalance(tgID, price)
		return
	}
	if !user.IsBanned {
		_ = b.xui.SetClientEnabled(user.VlessUUID, user.XUIEmail, true)
	}
	exp := time.Unix(newExpiry, 0).Format("02.01.2006")
	b.showMenu(tgID, fmt.Sprintf(
		"<b>Безлимитная подписка активирована!</b>\n\nДействует до: <b>%s</b>\nСписано: %.0f руб.",
		exp, price,
	), tgbotapi.NewInlineKeyboardMarkup(backRow()))
}

func (b *Bot) showTopup(tgID int64) {
	text := "<b>Пополнение баланса</b>\n\nВыберите способ оплаты:"
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("TON / USDT", "topup_ton"),
			tgbotapi.NewInlineKeyboardButtonData("DonationAlerts", "topup_da"),
		),
		backRow(),
	)
	b.showMenu(tgID, text, keyboard)
}

func (b *Bot) showTONInfo(tgID int64) {
	text := fmt.Sprintf(
		"<b>Пополнение через TON / USDT</b>\n\n"+
			"Кошелёк:\n<code>%s</code>\n\n"+
			"В поле <b>Комментарий</b> обязательно укажите ваш ID:\n"+
			"<code>%d</code>\n\n"+
			"Курс зачисления:\n"+
			"  1 TON = %.0f руб.\n"+
			"  1 USDT = %.0f руб.\n\n"+
			"Зачисление в течение 1–2 минут.",
		b.cfg.Payments.TON.WalletAddress, tgID,
		b.cfg.Payments.TON.TONToRUB,
		b.cfg.Payments.TON.USDTToRUB,
	)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Назад", "topup"),
		),
	)
	b.showMenu(tgID, text, keyboard)
}

func (b *Bot) showDAInput(tgID int64) {
	setState(tgID, "await_da_amount")
	text := "<b>DonationAlerts</b>\n\n" +
		"Введите сумму, которую хотите зачислить на баланс (в рублях).\n\n" +
		"Бот автоматически рассчитает сумму с учётом комиссии и зачислит баланс после получения доната."
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Отмена", "topup"),
		),
	)
	b.showMenu(tgID, text, keyboard)
}

func (b *Bot) handleDAAmountInput(ctx context.Context, tgID int64, text string) {
	amount, err := strconv.ParseFloat(strings.Replace(text, ",", ".", 1), 64)
	if err != nil || amount <= 0 {
		b.showMenu(tgID,
			"Некорректная сумма. Введите число, например: <b>100</b>",
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("Назад", "topup_da"),
				),
			),
		)
		return
	}
	clearState(tgID)
	required := b.daClient.CalcRequiredPayment(amount)
	text2 := fmt.Sprintf(
		"<b>DonationAlerts — инструкция</b>\n\n"+
			"Сумма к зачислению: <b>%.2f руб.</b>\n"+
			"Сумма к оплате (с комиссией %.0f%%): <b>%.2f руб.</b>\n\n"+
			"Ссылка для оплаты:\n%s\n\n"+
			"В поле «Сообщение» укажите ваш Telegram ID:\n<code>%d</code>\n\n"+
			"Баланс будет зачислен автоматически в течение 1–2 минут после оплаты.",
		amount,
		b.cfg.Payments.DonationAlerts.CommissionRate*100,
		required,
		b.daClient.CreatePaymentLink(amount, fmt.Sprintf("%d", tgID)),
		tgID,
	)
	b.showMenu(tgID, text2, tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Назад", "topup"),
		),
	))
}

func (b *Bot) showPromosMenu(ctx context.Context, tgID int64) {
	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}

	myPromos, _ := b.db.ListUserPromoCodes(user.ID)
	activeCount := 0
	for _, p := range myPromos {
		if p.IsActive {
			activeCount++
		}
	}

	text := fmt.Sprintf(
		"<b>Промокоды</b>\n\n"+
			"Активируйте чужой промокод или создайте свой.\n"+
			"Ваших промокодов (активных): <b>%d</b>",
		activeCount,
	)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Активировать промокод", "promo_activate"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Создать промокод", "promo_create"),
			tgbotapi.NewInlineKeyboardButtonData("Мои промокоды", "promo_mylist"),
		),
		backRow(),
	)
	b.showMenu(tgID, text, keyboard)
}

func (b *Bot) showPromoActivateInput(tgID int64) {
	setState(tgID, "await_promo_activate")
	text := "<b>Активация промокода</b>\n\nОтправьте код промокода:"
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Назад", "promos"),
		),
	)
	b.showMenu(tgID, text, keyboard)
}

func (b *Bot) handlePromoActivation(ctx context.Context, tgID int64, code string) {
	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		b.showMenu(tgID, "Невозможно активировать промокод.", tgbotapi.NewInlineKeyboardMarkup(backRow()))
		return
	}

	fail := func() {
		b.showMenu(tgID, "Невозможно активировать промокод.",
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Назад", "promos")),
			))
	}

	promo, err := b.db.GetPromoCode(strings.TrimSpace(code))
	if err != nil || promo == nil || !promo.IsActive {
		fail()
		return
	}
	if promo.MaxActivations > 0 && promo.Activations >= promo.MaxActivations {
		fail()
		return
	}
	if promo.CreatedByUserID == user.ID {
		fail()
		return
	}
	if promo.ChannelID != 0 && !b.isChatMember(tgID, promo.ChannelID) {
		fail()
		return
	}
	used, err := b.db.HasUserActivatedPromo(user.ID, promo.ID)
	if err != nil || used {
		fail()
		return
	}
	if err := b.db.ActivatePromo(user.ID, promo.ID); err != nil {
		fail()
		return
	}

	var successText string

	switch promo.PromoType {
	case db.PromoTypeSubscription:
		days := promo.SubscriptionDays
		if days <= 0 {
			days = 30
		}
		var newExpiry int64
		if user.HasUnlimited() {
			newExpiry = user.UnlimitedExpiresAt + days*24*3600
		} else {
			newExpiry = time.Now().Unix() + days*24*3600
		}
		if err := b.db.SetUserUnlimited(tgID, newExpiry); err == nil {
			if !user.IsBanned {
				_ = b.xui.SetClientEnabled(user.VlessUUID, user.XUIEmail, true)
			}
			exp := time.Unix(newExpiry, 0).Format("02.01.2006")
			successText = fmt.Sprintf(
				"<b>Промокод активирован!</b>\n\nБезлимитная подписка на %d дн.\nДействует до: <b>%s</b>",
				days, exp,
			)
		}
	default:
		if err := b.db.UpdateUserBalance(tgID, promo.BalanceAmount); err == nil {
			successText = fmt.Sprintf(
				"<b>Промокод активирован!</b>\n\nНачислено: <b>+%.2f руб.</b>",
				promo.BalanceAmount,
			)
		}
	}

	if successText == "" {
		successText = "Невозможно активировать промокод."
	}

	b.showMenu(tgID, successText, tgbotapi.NewInlineKeyboardMarkup(backRow()))
}

func (b *Bot) showPromoCreateTypeSelect(tgID int64) {
	text := "<b>Создание промокода</b>\n\n" +
		"Выберите тип:\n\n" +
		"<b>На баланс</b> — получатель получает рубли (списываются с вашего баланса сразу).\n\n" +
		"<b>На подписку</b> — получатель получает безлимитную подписку (списывается стоимость подписки)."
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("На баланс", "promo_create_balance"),
			tgbotapi.NewInlineKeyboardButtonData("На подписку", "promo_create_sub"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Назад", "promos"),
		),
	)
	b.showMenu(tgID, text, keyboard)
}

func (b *Bot) startPromoCreateBalance(tgID int64) {
	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}
	setStateData(tgID, "await_promo_create_amount", map[string]string{"type": "balance"})
	text := fmt.Sprintf(
		"<b>Промокод на баланс</b>\n\n"+
			"Введите сумму (руб.), которую получит пользователь.\n"+
			"Эта сумма спишется с вашего баланса.\n\n"+
			"Ваш баланс: <b>%.2f руб.</b>",
		user.Balance,
	)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Назад", "promo_create"),
		),
	)
	b.showMenu(tgID, text, keyboard)
}

func (b *Bot) startPromoCreateSubscription(tgID int64) {
	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}
	price := b.cfg.Billing.UnlimitedPriceMonth
	setStateData(tgID, "await_promo_create_subdays", map[string]string{"type": "subscription"})
	text := fmt.Sprintf(
		"<b>Промокод на подписку</b>\n\n"+
			"Введите количество дней подписки (например: 30).\n\n"+
			"Стоимость: <b>%.0f руб./месяц</b>\n"+
			"Итоговая сумма = (дни / 30) × %.0f руб.\n\n"+
			"Ваш баланс: <b>%.2f руб.</b>",
		price, price, user.Balance,
	)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Назад", "promo_create"),
		),
	)
	b.showMenu(tgID, text, keyboard)
}

func (b *Bot) handleUserPromoCreateAmount(ctx context.Context, tgID int64, text string) {
	amount, err := strconv.ParseFloat(strings.Replace(text, ",", ".", 1), 64)
	if err != nil || amount <= 0 {
		b.showMenu(tgID, "Некорректная сумма. Введите положительное число:",
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Отмена", "promos")),
			))
		return
	}

	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}
	if user.Balance < amount {
		b.showMenu(tgID,
			fmt.Sprintf("Недостаточно средств.\n\nНужно: %.2f руб.\nВаш баланс: %.2f руб.", amount, user.Balance),
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Отмена", "promos")),
			))
		return
	}

	sess := getSession(tgID)
	sess.data["amount"] = text
	setStateData(tgID, "await_promo_create_maxact", sess.data)

	b.showMenu(tgID,
		fmt.Sprintf("Сумма: <b>%.2f руб.</b>\n\nВведите максимальное число активаций (1–50, 0 = безлимит):", amount),
		tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Отмена", "promos")),
		))
}

func (b *Bot) handleUserPromoCreateSubDays(ctx context.Context, tgID int64, text string) {
	days, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || days <= 0 || days > 365 {
		b.showMenu(tgID, "Некорректное количество дней (1–365):",
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Отмена", "promos")),
			))
		return
	}

	price := b.cfg.Billing.UnlimitedPriceMonth * float64(days) / 30.0

	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}
	if user.Balance < price {
		b.showMenu(tgID,
			fmt.Sprintf("Недостаточно средств.\n\nНужно: %.2f руб. (%d дн.)\nВаш баланс: %.2f руб.", price, days, user.Balance),
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Отмена", "promos")),
			))
		return
	}

	sess := getSession(tgID)
	sess.data["subdays"] = strconv.FormatInt(days, 10)
	sess.data["amount"] = fmt.Sprintf("%.2f", price)
	setStateData(tgID, "await_promo_create_maxact", sess.data)

	b.showMenu(tgID,
		fmt.Sprintf("Подписка: <b>%d дн.</b> (стоимость: %.2f руб.)\n\nВведите макс. число активаций (1–50, 0 = безлимит):", days, price),
		tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Отмена", "promos")),
		))
}

func (b *Bot) handleUserPromoCreateMaxAct(ctx context.Context, tgID int64, text string) {
	maxAct, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || maxAct < 0 || maxAct > 50 {
		b.showMenu(tgID, "Число от 0 до 50:",
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Отмена", "promos")),
			))
		return
	}

	sess := getSession(tgID)
	promoType := sess.data["type"]
	amountStr := sess.data["amount"]
	subDaysStr := sess.data["subdays"]

	amount, _ := strconv.ParseFloat(amountStr, 64)
	subDays, _ := strconv.ParseInt(subDaysStr, 10, 64)

	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}
	if user.Balance < amount {
		b.showMenu(tgID, "Баланс изменился, операция отменена.",
			tgbotapi.NewInlineKeyboardMarkup(backRow()))
		clearState(tgID)
		return
	}

	if err := b.db.UpdateUserBalance(tgID, -amount); err != nil {
		b.showMenu(tgID, "Ошибка списания баланса.", tgbotapi.NewInlineKeyboardMarkup(backRow()))
		clearState(tgID)
		return
	}

	code := randomCode()
	balanceForPromo := 0.0
	if promoType == db.PromoTypeBalance {
		balanceForPromo = amount
	}

	if err := b.db.CreatePromoCode(code, promoType, balanceForPromo, subDays, maxAct, 0, user.ID); err != nil {
		_ = b.db.UpdateUserBalance(tgID, amount)
		b.showMenu(tgID, "Ошибка создания промокода.", tgbotapi.NewInlineKeyboardMarkup(backRow()))
		clearState(tgID)
		return
	}

	clearState(tgID)

	maxStr := "безлимит"
	if maxAct > 0 {
		maxStr = fmt.Sprintf("%d", maxAct)
	}

	var typeStr string
	if promoType == db.PromoTypeSubscription {
		typeStr = fmt.Sprintf("Подписка: %d дн.", subDays)
	} else {
		typeStr = fmt.Sprintf("Баланс: %.2f руб.", amount)
	}

	b.showMenu(tgID,
		fmt.Sprintf(
			"<b>Промокод создан!</b>\n\n"+
				"Код: <code>%s</code>\n"+
				"Тип: %s\n"+
				"Активаций: %s\n\n"+
				"Поделитесь кодом с друзьями!",
			code, typeStr, maxStr,
		),
		tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Мои промокоды", "promo_mylist"),
			),
			backRow(),
		),
	)
}

func (b *Bot) showMyPromos(ctx context.Context, tgID int64) {
	user, err := b.db.GetUserByTelegramID(tgID)
	if err != nil || user == nil {
		return
	}

	promos, err := b.db.ListUserPromoCodes(user.ID)
	if err != nil || len(promos) == 0 {
		b.showMenu(tgID, "У вас нет созданных промокодов.",
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Назад", "promos")),
			))
		return
	}

	var sb strings.Builder
	sb.WriteString("<b>Ваши промокоды</b>\n\n")
	for _, p := range promos {
		status := "активен"
		if !p.IsActive {
			status = "деактивирован"
		}
		maxStr := "безл."
		if p.MaxActivations > 0 {
			maxStr = strconv.FormatInt(p.MaxActivations, 10)
		}
		var typeStr string
		if p.PromoType == db.PromoTypeSubscription {
			typeStr = fmt.Sprintf("подписка %dд", p.SubscriptionDays)
		} else {
			typeStr = fmt.Sprintf("%.0f руб.", p.BalanceAmount)
		}
		sb.WriteString(fmt.Sprintf(
			"<code>%s</code> [%s] %d/%s — %s\n",
			p.Code, typeStr, p.Activations, maxStr, status,
		))
	}

	b.showMenu(tgID, sb.String(),
		tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Назад", "promos")),
		))
}

func (b *Bot) showReferral(ctx context.Context, tgID int64) {
	refLink := fmt.Sprintf("https://t.me/%s?start=%d", b.api.Self.UserName, tgID)
	text := fmt.Sprintf(
		"<b>Реферальная программа</b>\n\n"+
			"За каждое пополнение баланса вашего реферала вы получаете <b>%.0f%%</b> от суммы.\n\n"+
			"Ваша ссылка:\n%s",
		b.cfg.Referral.RewardPercent, refLink,
	)
	b.showMenu(tgID, text, tgbotapi.NewInlineKeyboardMarkup(backRow()))
}
