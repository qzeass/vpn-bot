package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Telegram TelegramConfig `yaml:"telegram"`
	Database DatabaseConfig `yaml:"database"`
	XUI      XUIConfig      `yaml:"xui"`
	Admin    AdminConfig    `yaml:"admin"`
	Billing  BillingConfig  `yaml:"billing"`
	Payments PaymentsConfig `yaml:"payments"`
	Referral ReferralConfig `yaml:"referral"`
}

type TelegramConfig struct {
	Token string `yaml:"token"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type XUIConfig struct {
	BaseURL   string `yaml:"base_url"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	InboundID int    `yaml:"inbound_id"`
}

type AdminConfig struct {
	IDs []int64 `yaml:"ids"`
}

type BillingConfig struct {
	FreeLimitGB           float64 `yaml:"free_limit_gb"`
	OverdraftCostPerGB    float64 `yaml:"overdraft_cost_per_gb"`
	UnlimitedPriceMonth   float64 `yaml:"unlimited_price_month"`
	WorkerIntervalMin     int     `yaml:"worker_interval_min"`
	TONCheckerIntervalSec int     `yaml:"ton_checker_interval_sec"`
	DACheckerIntervalSec  int     `yaml:"da_checker_interval_sec"`
}

type PaymentsConfig struct {
	DonationAlerts DonationAlertsConfig `yaml:"donation_alerts"`
	TON            TONConfig            `yaml:"ton"`
}

type DonationAlertsConfig struct {
	AccessToken    string  `yaml:"access_token"`
	PageName       string  `yaml:"page_name"`
	CommissionRate float64 `yaml:"commission_rate"`
}

type TONConfig struct {
	WalletAddress   string  `yaml:"wallet_address"`
	ToncenterAPIKey string  `yaml:"toncenter_api_key"`
	TONToRUB        float64 `yaml:"ton_to_rub"`
	USDTToRUB       float64 `yaml:"usdt_to_rub"`
	USDTMasterAddr  string  `yaml:"usdt_master_addr"`
}

type ReferralConfig struct {
	RewardPercent float64 `yaml:"reward_percent"`
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	if cfg.Billing.WorkerIntervalMin <= 0 {
		cfg.Billing.WorkerIntervalMin = 15
	}
	if cfg.Billing.TONCheckerIntervalSec <= 0 {
		cfg.Billing.TONCheckerIntervalSec = 45
	}
	if cfg.Billing.DACheckerIntervalSec <= 0 {
		cfg.Billing.DACheckerIntervalSec = 60
	}
	if cfg.Billing.FreeLimitGB <= 0 {
		cfg.Billing.FreeLimitGB = 3
	}
	if cfg.Billing.OverdraftCostPerGB <= 0 {
		cfg.Billing.OverdraftCostPerGB = 2
	}
	if cfg.Billing.UnlimitedPriceMonth <= 0 {
		cfg.Billing.UnlimitedPriceMonth = 100
	}
	if cfg.Payments.DonationAlerts.CommissionRate <= 0 {
		cfg.Payments.DonationAlerts.CommissionRate = 0.12
	}
	if cfg.Payments.DonationAlerts.PageName == "" {
		cfg.Payments.DonationAlerts.PageName = "your_page"
	}
	if cfg.Referral.RewardPercent <= 0 {
		cfg.Referral.RewardPercent = 10
	}
	if cfg.Payments.TON.USDTMasterAddr == "" {
		cfg.Payments.TON.USDTMasterAddr = "EQBynBO23ywHy_CgarY9NK9FTz0yDsG82PtcbSTQgGoXwiuA"
	}

	return &cfg, nil
}

func (c *Config) IsAdmin(userID int64) bool {
	for _, id := range c.Admin.IDs {
		if id == userID {
			return true
		}
	}
	return false
}
