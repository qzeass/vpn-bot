package bot

import (
	"context"
	"fmt"
	"log"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/qzeass/vpn-bot/internal/billing"
	"github.com/qzeass/vpn-bot/internal/config"
	"github.com/qzeass/vpn-bot/internal/db"
	"github.com/qzeass/vpn-bot/internal/payments"
	"github.com/qzeass/vpn-bot/internal/xui"
)

type Bot struct {
	api      *tgbotapi.BotAPI
	db       *db.DB
	xui      *xui.Client
	cfg      *config.Config
	billing  *billing.Worker
	daClient *payments.DonationAlertsClient
	mu       sync.Mutex
	msgIDs   map[int64]int
}

func New(cfg *config.Config, database *db.DB, xuiClient *xui.Client, billingWorker *billing.Worker, daClient *payments.DonationAlertsClient) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.Telegram.Token)
	if err != nil {
		return nil, fmt.Errorf("new bot api: %w", err)
	}
	api.Debug = false

	return &Bot{
		api:      api,
		db:       database,
		xui:      xuiClient,
		cfg:      cfg,
		billing:  billingWorker,
		daClient: daClient,
		msgIDs:   make(map[int64]int),
	}, nil
}

func (b *Bot) NotifyUser(telegramID int64, msg string) {
	m := tgbotapi.NewMessage(telegramID, msg)
	m.ParseMode = "HTML"
	if _, err := b.api.Send(m); err != nil {
		log.Printf("[bot] notify user %d: %v", telegramID, err)
	}
}

func (b *Bot) Run(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := b.api.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			go b.handleUpdate(ctx, update)
		}
	}
}

func (b *Bot) handleUpdate(ctx context.Context, update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[bot] panic: %v", r)
		}
	}()

	if update.Message != nil {
		b.handleMessage(ctx, update.Message)
	} else if update.CallbackQuery != nil {
		b.handleCallback(ctx, update.CallbackQuery)
	}
}

func (b *Bot) storeMsgID(chatID int64, msgID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.msgIDs[chatID] = msgID
}

func (b *Bot) getMsgID(chatID int64) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.msgIDs[chatID]
}

func (b *Bot) showMenu(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	msgID := b.getMsgID(chatID)
	if msgID != 0 {
		edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, text, keyboard)
		edit.ParseMode = "HTML"
		if _, err := b.api.Send(edit); err == nil {
			return
		}
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = keyboard
	sent, err := b.api.Send(msg)
	if err != nil {
		log.Printf("[bot] showMenu send: %v", err)
		return
	}
	b.storeMsgID(chatID, sent.MessageID)
}

func (b *Bot) pushNotice(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("[bot] pushNotice: %v", err)
	}
}

func (b *Bot) answerCallback(query *tgbotapi.CallbackQuery, text string) {
	cb := tgbotapi.NewCallback(query.ID, text)
	if _, err := b.api.Request(cb); err != nil {
		log.Printf("[bot] answerCallback: %v", err)
	}
}

func (b *Bot) isChatMember(userID, channelID int64) bool {
	cfg := tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: channelID,
			UserID: userID,
		},
	}
	member, err := b.api.GetChatMember(cfg)
	if err != nil {
		return false
	}
	s := member.Status
	return s == "member" || s == "administrator" || s == "creator"
}

func (b *Bot) deleteMsg(chatID int64, msgID int) {
	d := tgbotapi.NewDeleteMessage(chatID, msgID)
	b.api.Request(d)
}

func mainMenu() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Мой VPN ключ", "vpn_key"),
			tgbotapi.NewInlineKeyboardButtonData("Баланс", "balance"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Статистика", "stats"),
			tgbotapi.NewInlineKeyboardButtonData("Безлимит", "unlimited"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Пополнить баланс", "topup"),
			tgbotapi.NewInlineKeyboardButtonData("Промокоды", "promos"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Реферальная программа", "referral"),
		),
	)
}

func backRow() []tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Главное меню", "menu"),
	)
}
