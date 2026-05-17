package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/qzeass/vpn-bot/internal/billing"
	"github.com/qzeass/vpn-bot/internal/bot"
	"github.com/qzeass/vpn-bot/internal/config"
	"github.com/qzeass/vpn-bot/internal/db"
	"github.com/qzeass/vpn-bot/internal/payments"
	"github.com/qzeass/vpn-bot/internal/xui"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	database, err := db.New(cfg.Database.Path)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer database.Close()

	xuiClient, err := xui.New(cfg.XUI.BaseURL, cfg.XUI.Username, cfg.XUI.Password, cfg.XUI.InboundID)
	if err != nil {
		log.Fatalf("create xui client: %v", err)
	}

	if err := xuiClient.EnsureLoggedIn(); err != nil {
		log.Printf("WARNING: initial XUI login failed: %v", err)
	}

	daClient := payments.NewDonationAlerts(
		cfg.Payments.DonationAlerts.AccessToken,
		cfg.Payments.DonationAlerts.PageName,
		cfg.Payments.DonationAlerts.CommissionRate,
	)

	tonChecker := payments.NewTONChecker(
		cfg.Payments.TON.WalletAddress,
		cfg.Payments.TON.ToncenterAPIKey,
		cfg.Payments.TON.TONToRUB,
		cfg.Payments.TON.USDTToRUB,
		cfg.Payments.TON.USDTMasterAddr,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var notifyFn billing.NotifyFunc

	billingWorker := billing.New(database, xuiClient, cfg, tonChecker, daClient, func(tgID int64, msg string) {
		if notifyFn != nil {
			notifyFn(tgID, msg)
		}
	})

	botInstance, err := bot.New(cfg, database, xuiClient, billingWorker, daClient)
	if err != nil {
		log.Fatalf("create bot: %v", err)
	}

	notifyFn = botInstance.NotifyUser

	go billingWorker.Start(ctx)

	log.Println("Bot started.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Shutting down...")
		cancel()
	}()

	botInstance.Run(ctx)
	log.Println("Bot stopped.")
}
