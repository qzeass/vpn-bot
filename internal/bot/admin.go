package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/qzeass/vpn-bot/internal/db"
)

type adminStateEntry struct {
	step string
	data map[string]string
}

var (
	adminStateMap   = make(map[int64]*adminStateEntry)
	adminStateMutex sync.Mutex
)

func (b *Bot) getAdminState(tgID int64) *adminStateEntry {
	adminStateMutex.Lock()
	defer adminStateMutex.Unlock()
	return adminStateMap[tgID]
}

func (b *Bot) setAdminState(tgID int64, s *adminStateEntry) {
	adminStateMutex.Lock()
	defer adminStateMutex.Unlock()
	adminStateMap[tgID] = s
}

func (b *Bot) clearAdminState(tgID int64) {
	adminStateMutex.Lock()
	defer adminStateMutex.Unlock()
	delete(adminStateMap, tgID)
}

func adminMainMenu() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Пользователи", "admin_users"),
			tgbotapi.NewInlineKeyboardButtonData("Рассылка", "admin_broadcast"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Промокоды", "admin_promos"),
			tgbotapi.NewInlineKeyboardButtonData("Тарифы", "admin_tariffs"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Статистика", "admin_stats"),
			tgbotapi.NewInlineKeyboardButtonData("Пополнить баланс", "admin_topup"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Пользовательское меню", "menu"),
		),
	)
}

func adminBackRow() []tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Назад", "admin_back"),
	)
}

func (b *Bot) handleAdminMessage(ctx context.Context, msg *tgbotapi.Message) bool {
	tgID := msg.From.ID
	text := strings.TrimSpace(msg.Text)

	if text == "/admin" {
		b.deleteMsg(msg.Chat.ID, msg.MessageID)
		total, _ := b.db.CountUsers()
		b.showMenu(tgID,
			fmt.Sprintf("<b>Панель администратора</b>\n\nПользователей: <b>%d</b>", total),
			adminMainMenu(),
		)
		return true
	}

	st := b.getAdminState(tgID)
	if st == nil {
		return false
	}

	b.deleteMsg(msg.Chat.ID, msg.MessageID)

	switch st.step {
	case "broadcast_text":
		b.executeBroadcast(ctx, tgID, text)
		b.clearAdminState(tgID)
		return true

	case "promo_code":
		st.data["code"] = text
		st.step = "promo_type"
		b.setAdminState(tgID, st)
		b.showMenu(tgID, fmt.Sprintf("Код: <code>%s</code>\n\nВыберите тип промокода:", text),
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("На баланс", "admin_promo_type_balance"),
					tgbotapi.NewInlineKeyboardButtonData("На подписку", "admin_promo_type_sub"),
				),
				adminBackRow(),
			))
		return true

	case "promo_amount":
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil || amount <= 0 {
			b.showMenu(tgID, "Некорректная сумма. Введите число:", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			return true
		}
		st.data["amount"] = text
		st.step = "promo_maxact"
		b.setAdminState(tgID, st)
		b.showMenu(tgID, "Максимальное число активаций (0 = безлимит):", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return true

	case "promo_subdays":
		days, err := strconv.ParseInt(text, 10, 64)
		if err != nil || days <= 0 {
			b.showMenu(tgID, "Некорректное количество дней. Введите число:", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			return true
		}
		st.data["subdays"] = text
		st.step = "promo_maxact"
		b.setAdminState(tgID, st)
		b.showMenu(tgID, fmt.Sprintf("Подписка: <b>%d дн.</b>\n\nМаксимальное число активаций (0 = безлимит):", days),
			tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return true

	case "promo_maxact":
		maxAct, err := strconv.ParseInt(text, 10, 64)
		if err != nil || maxAct < 0 {
			b.showMenu(tgID, "Некорректное число. Введите 0 или больше:", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			return true
		}
		st.data["maxact"] = text
		st.step = "promo_channel"
		b.setAdminState(tgID, st)
		b.showMenu(tgID, "ID канала для проверки подписки (0 = без проверки):", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return true

	case "promo_channel":
		channelID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			b.showMenu(tgID, "Некорректный ID. Введите число или 0:", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			return true
		}
		code := st.data["code"]
		promoType := st.data["promo_type"]
		if promoType == "" {
			promoType = db.PromoTypeBalance
		}
		amount, _ := strconv.ParseFloat(st.data["amount"], 64)
		subDays, _ := strconv.ParseInt(st.data["subdays"], 10, 64)
		maxAct, _ := strconv.ParseInt(st.data["maxact"], 10, 64)

		balanceAmount := 0.0
		if promoType == db.PromoTypeBalance {
			balanceAmount = amount
		}

		if err := b.db.CreatePromoCode(code, promoType, balanceAmount, subDays, maxAct, channelID, 0); err != nil {
			b.showMenu(tgID, fmt.Sprintf("Ошибка создания: %v", err), tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		} else {
			var typeStr string
			if promoType == db.PromoTypeSubscription {
				typeStr = fmt.Sprintf("Подписка %d дн.", subDays)
			} else {
				typeStr = fmt.Sprintf("%.2f руб.", balanceAmount)
			}
			maxStr := "безлимит"
			if maxAct > 0 {
				maxStr = strconv.FormatInt(maxAct, 10)
			}
			b.showMenu(tgID, fmt.Sprintf(
				"<b>Промокод создан!</b>\n\nКод: <code>%s</code>\nТип: %s\nАктиваций: %s",
				code, typeStr, maxStr,
			), tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		}
		b.clearAdminState(tgID)
		return true

	case "user_lookup":
		b.adminHandleUserLookup(ctx, tgID, text)
		b.clearAdminState(tgID)
		return true

	case "user_set_balance":
		userTgID, _ := strconv.ParseInt(st.data["target_id"], 10, 64)
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil {
			b.showMenu(tgID, "Некорректная сумма.", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			b.clearAdminState(tgID)
			return true
		}
		if err := b.db.SetUserBalance(userTgID, amount); err != nil {
			b.showMenu(tgID, fmt.Sprintf("Ошибка: %v", err), tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		} else {
			b.showMenu(tgID,
				fmt.Sprintf("Баланс пользователя <code>%d</code> установлен: %.2f руб.", userTgID, amount),
				tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		}
		b.clearAdminState(tgID)
		return true

	case "admin_topup_id":
		st.data["target_id"] = text
		st.step = "admin_topup_amount"
		b.setAdminState(tgID, st)
		b.showMenu(tgID, fmt.Sprintf("Пополнение для <code>%s</code>\n\nВведите сумму (руб.):", text),
			tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return true

	case "admin_topup_amount":
		userTgID, err := strconv.ParseInt(st.data["target_id"], 10, 64)
		if err != nil {
			b.showMenu(tgID, "Некорректный ID.", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			b.clearAdminState(tgID)
			return true
		}
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil || amount <= 0 {
			b.showMenu(tgID, "Некорректная сумма.", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			b.clearAdminState(tgID)
			return true
		}
		if err := b.billing.ApplyDonationAlertsPayment(ctx, userTgID, amount); err != nil {
			b.showMenu(tgID, fmt.Sprintf("Ошибка: %v", err), tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		} else {
			b.showMenu(tgID,
				fmt.Sprintf("Баланс <code>%d</code> пополнен на %.2f руб.", userTgID, amount),
				tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		}
		b.clearAdminState(tgID)
		return true

	case "tariff_free_limit":
		gb, err := strconv.ParseFloat(text, 64)
		if err != nil || gb <= 0 {
			b.showMenu(tgID, "Некорректное значение ГБ.", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			b.clearAdminState(tgID)
			return true
		}
		bytes := int64(gb * 1e9)
		target := st.data["target"]
		if target == "global" {
			if err := b.db.SetGlobalFreeLimitBytes(bytes); err != nil {
				b.showMenu(tgID, fmt.Sprintf("Ошибка: %v", err), tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			} else {
				b.showMenu(tgID,
					fmt.Sprintf("Глобальный бесплатный лимит: <b>%.1f ГБ</b>", gb),
					tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			}
		} else {
			uid, _ := strconv.ParseInt(target, 10, 64)
			if err := b.db.SetUserFreeLimitBytes(uid, bytes); err != nil {
				b.showMenu(tgID, fmt.Sprintf("Ошибка: %v", err), tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			} else {
				b.showMenu(tgID,
					fmt.Sprintf("Лимит для <code>%d</code>: <b>%.1f ГБ</b>", uid, gb),
					tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			}
		}
		b.clearAdminState(tgID)
		return true

	case "tariff_user_limit_id":
		targetID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			b.showMenu(tgID, "Некорректный Telegram ID.", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			b.clearAdminState(tgID)
			return true
		}
		b.setAdminState(tgID, &adminStateEntry{
			step: "tariff_free_limit",
			data: map[string]string{"target": strconv.FormatInt(targetID, 10)},
		})
		b.showMenu(tgID,
			fmt.Sprintf("Введите новый лимит (ГБ) для пользователя <code>%d</code>:", targetID),
			tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return true
	}

	return false
}

func (b *Bot) handleAdminCallback(ctx context.Context, query *tgbotapi.CallbackQuery) {
	tgID := query.From.ID
	data := query.Data
	b.answerCallback(query, "")

	switch data {
	case "admin_back":
		b.clearAdminState(tgID)
		total, _ := b.db.CountUsers()
		b.showMenu(tgID,
			fmt.Sprintf("<b>Панель администратора</b>\n\nПользователей: <b>%d</b>", total),
			adminMainMenu())

	case "admin_users":
		b.clearAdminState(tgID)
		b.setAdminState(tgID, &adminStateEntry{step: "user_lookup", data: map[string]string{}})
		b.showMenu(tgID, "Введите Telegram ID пользователя:", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))

	case "admin_broadcast":
		b.clearAdminState(tgID)
		b.setAdminState(tgID, &adminStateEntry{step: "broadcast_text", data: map[string]string{}})
		b.showMenu(tgID, "Введите текст рассылки (HTML):", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))

	case "admin_promos":
		b.showAdminPromosMenu(ctx, tgID)

	case "admin_promos_create":
		b.clearAdminState(tgID)
		b.setAdminState(tgID, &adminStateEntry{step: "promo_code", data: map[string]string{}})
		b.showMenu(tgID, "Введите код нового промокода:", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))

	case "admin_promo_type_balance":
		st := b.getAdminState(tgID)
		if st == nil {
			return
		}
		st.data["promo_type"] = db.PromoTypeBalance
		st.step = "promo_amount"
		b.setAdminState(tgID, st)
		b.showMenu(tgID, "Введите сумму баланса (руб.):", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))

	case "admin_promo_type_sub":
		st := b.getAdminState(tgID)
		if st == nil {
			return
		}
		st.data["promo_type"] = db.PromoTypeSubscription
		st.step = "promo_subdays"
		b.setAdminState(tgID, st)
		b.showMenu(tgID, "Введите количество дней подписки:", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))

	case "admin_promos_list":
		b.showAdminPromoList(ctx, tgID)

	case "admin_tariffs":
		b.showAdminTariffsMenu(tgID)

	case "admin_tariff_global_limit":
		b.clearAdminState(tgID)
		b.setAdminState(tgID, &adminStateEntry{step: "tariff_free_limit", data: map[string]string{"target": "global"}})
		b.showMenu(tgID, "Введите новый глобальный бесплатный лимит (ГБ):", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))

	case "admin_tariff_user_limit":
		b.clearAdminState(tgID)
		b.setAdminState(tgID, &adminStateEntry{step: "tariff_user_limit_id", data: map[string]string{}})
		b.showMenu(tgID, "Введите Telegram ID пользователя для изменения лимита:", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))

	case "admin_stats":
		b.showAdminStats(ctx, tgID)

	case "admin_topup":
		b.clearAdminState(tgID)
		b.setAdminState(tgID, &adminStateEntry{step: "admin_topup_id", data: map[string]string{}})
		b.showMenu(tgID, "Введите Telegram ID пользователя для пополнения:", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))

	default:
		switch {
		case strings.HasPrefix(data, "admin_ban_"):
			uid, err := strconv.ParseInt(strings.TrimPrefix(data, "admin_ban_"), 10, 64)
			if err == nil {
				b.adminBanUser(ctx, tgID, uid, true)
			}
		case strings.HasPrefix(data, "admin_unban_"):
			uid, err := strconv.ParseInt(strings.TrimPrefix(data, "admin_unban_"), 10, 64)
			if err == nil {
				b.adminBanUser(ctx, tgID, uid, false)
			}
		case strings.HasPrefix(data, "admin_setbal_"):
			uid, err := strconv.ParseInt(strings.TrimPrefix(data, "admin_setbal_"), 10, 64)
			if err == nil {
				b.clearAdminState(tgID)
				b.setAdminState(tgID, &adminStateEntry{
					step: "user_set_balance",
					data: map[string]string{"target_id": strconv.FormatInt(uid, 10)},
				})
				b.showMenu(tgID,
					fmt.Sprintf("Введите новый баланс для <code>%d</code> (руб.):", uid),
					tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			}
		case strings.HasPrefix(data, "admin_promo_del_"):
			code := strings.TrimPrefix(data, "admin_promo_del_")
			if err := b.db.DeletePromoCode(code); err != nil {
				b.showMenu(tgID, fmt.Sprintf("Ошибка: %v", err), tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
			} else {
				b.showAdminPromoList(ctx, tgID)
			}
		}
	}
}

func (b *Bot) adminHandleUserLookup(ctx context.Context, adminTgID int64, input string) {
	targetID, err := strconv.ParseInt(strings.TrimSpace(input), 10, 64)
	if err != nil {
		b.showMenu(adminTgID, "Некорректный Telegram ID.", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return
	}

	user, err := b.db.GetUserByTelegramID(targetID)
	if err != nil || user == nil {
		b.showMenu(adminTgID, "Пользователь не найден.", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return
	}

	unlimited := "нет"
	if user.HasUnlimited() {
		unlimited = time.Unix(user.UnlimitedExpiresAt, 0).Format("02.01.2006")
	}
	bannedStr := "нет"
	if user.IsBanned {
		bannedStr = "ДА"
	}

	text := fmt.Sprintf(
		"@%s (<code>%d</code>)\n\n"+
			"Баланс: <b>%.2f руб.</b>\n"+
			"Лимит: <b>%.1f ГБ/день</b>\n"+
			"Использовано: <b>%.2f ГБ</b>\n"+
			"Безлимит до: <b>%s</b>\n"+
			"Заблокирован: <b>%s</b>\n"+
			"UUID: <code>%s</code>\n"+
			"С нами с: %s",
		user.Username, user.TelegramID,
		user.Balance,
		float64(user.FreeLimitBytes)/1e9,
		float64(user.DailyUsedBytes)/1e9,
		unlimited, bannedStr,
		user.VlessUUID,
		time.Unix(user.CreatedAt, 0).Format("02.01.2006"),
	)

	banLabel := "Заблокировать"
	banCallback := fmt.Sprintf("admin_ban_%d", targetID)
	if user.IsBanned {
		banLabel = "Разблокировать"
		banCallback = fmt.Sprintf("admin_unban_%d", targetID)
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(banLabel, banCallback),
			tgbotapi.NewInlineKeyboardButtonData("Изменить баланс", fmt.Sprintf("admin_setbal_%d", targetID)),
		),
		adminBackRow(),
	)
	b.showMenu(adminTgID, text, keyboard)
}

func (b *Bot) adminBanUser(ctx context.Context, adminTgID, targetTgID int64, ban bool) {
	user, err := b.db.GetUserByTelegramID(targetTgID)
	if err != nil || user == nil {
		b.showMenu(adminTgID, "Пользователь не найден.", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return
	}
	if err := b.db.SetUserBanned(targetTgID, ban); err != nil {
		b.showMenu(adminTgID, fmt.Sprintf("Ошибка: %v", err), tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return
	}
	if user.VlessUUID != "" {
		_ = b.xui.SetClientEnabled(user.VlessUUID, user.XUIEmail, !ban)
	}
	action, userMsg := "заблокирован", "Ваш аккаунт заблокирован администратором."
	if !ban {
		action, userMsg = "разблокирован", "Ваш аккаунт разблокирован."
	}
	b.showMenu(adminTgID,
		fmt.Sprintf("Пользователь <code>%d</code> %s.", targetTgID, action),
		tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
	b.pushNotice(targetTgID, userMsg)
}

func (b *Bot) executeBroadcast(ctx context.Context, adminTgID int64, text string) {
	users, err := b.db.GetAllUsers()
	if err != nil {
		b.showMenu(adminTgID, fmt.Sprintf("Ошибка: %v", err), tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return
	}
	sent, failed := 0, 0
	for _, u := range users {
		msg := tgbotapi.NewMessage(u.TelegramID, text)
		msg.ParseMode = "HTML"
		if _, err := b.api.Send(msg); err != nil {
			failed++
		} else {
			sent++
		}
	}
	b.showMenu(adminTgID,
		fmt.Sprintf("Рассылка завершена.\nОтправлено: %d\nОшибок: %d", sent, failed),
		tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
}

func (b *Bot) showAdminPromosMenu(ctx context.Context, tgID int64) {
	b.showMenu(tgID, "<b>Управление промокодами</b>",
		tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Создать", "admin_promos_create"),
				tgbotapi.NewInlineKeyboardButtonData("Список", "admin_promos_list"),
			),
			adminBackRow(),
		))
}

func (b *Bot) showAdminPromoList(ctx context.Context, tgID int64) {
	promos, err := b.db.ListPromoCodes()
	if err != nil {
		b.showMenu(tgID, fmt.Sprintf("Ошибка: %v", err), tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return
	}
	if len(promos) == 0 {
		b.showMenu(tgID, "Промокодов нет.", tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return
	}

	var sb strings.Builder
	sb.WriteString("<b>Промокоды</b>\n\n")
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, p := range promos {
		status := "+"
		if !p.IsActive {
			status = "-"
		}
		maxStr := "безл."
		if p.MaxActivations > 0 {
			maxStr = strconv.FormatInt(p.MaxActivations, 10)
		}
		var typeStr string
		if p.PromoType == db.PromoTypeSubscription {
			typeStr = fmt.Sprintf("подп.%dд", p.SubscriptionDays)
		} else {
			typeStr = fmt.Sprintf("%.0fр", p.BalanceAmount)
		}
		creator := ""
		if p.CreatedByUserID > 0 {
			creator = " [user]"
		}
		sb.WriteString(fmt.Sprintf("[%s] <code>%s</code> [%s] %d/%s%s\n",
			status, p.Code, typeStr, p.Activations, maxStr, creator))
		if p.IsActive {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Деактив. "+p.Code, "admin_promo_del_"+p.Code),
			))
		}
	}
	rows = append(rows, adminBackRow())
	b.showMenu(tgID, sb.String(), tgbotapi.NewInlineKeyboardMarkup(rows...))
}

func (b *Bot) showAdminTariffsMenu(tgID int64) {
	b.showMenu(tgID, fmt.Sprintf(
		"<b>Тарифы</b>\n\n"+
			"Бесплатный лимит: <b>%.1f ГБ/день</b>\n"+
			"Овердрафт: <b>%.0f руб./ГБ</b>\n"+
			"Безлимит: <b>%.0f руб./мес.</b>",
		b.cfg.Billing.FreeLimitGB,
		b.cfg.Billing.OverdraftCostPerGB,
		b.cfg.Billing.UnlimitedPriceMonth,
	), tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Лимит для всех", "admin_tariff_global_limit"),
			tgbotapi.NewInlineKeyboardButtonData("Лимит для юзера", "admin_tariff_user_limit"),
		),
		adminBackRow(),
	))
}

func (b *Bot) showAdminStats(ctx context.Context, tgID int64) {
	total, err := b.db.CountUsers()
	if err != nil {
		b.showMenu(tgID, fmt.Sprintf("Ошибка: %v", err), tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
		return
	}
	b.showMenu(tgID,
		fmt.Sprintf("<b>Статистика</b>\n\nВсего пользователей: <b>%d</b>", total),
		tgbotapi.NewInlineKeyboardMarkup(adminBackRow()))
}
