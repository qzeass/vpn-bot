package payments

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type DonationAlertsClient struct {
	accessToken    string
	pageName       string
	commissionRate float64
	httpClient     *http.Client
}

type DADonation struct {
	ID        int64   `json:"id"`
	Username  string  `json:"username"`
	Message   string  `json:"message"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	CreatedAt string  `json:"created_at"`
}

type DAListResponse struct {
	Data []DADonation `json:"data"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

func NewDonationAlerts(accessToken, pageName string, commissionRate float64) *DonationAlertsClient {
	return &DonationAlertsClient{
		accessToken:    accessToken,
		pageName:       pageName,
		commissionRate: commissionRate,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *DonationAlertsClient) Enabled() bool {
	return c.accessToken != "" && c.accessToken != "YOUR_DONATIONALERTS_ACCESS_TOKEN"
}

func (c *DonationAlertsClient) CalcRequiredPayment(desiredAmount float64) float64 {
	return desiredAmount / (1.0 - c.commissionRate)
}

func (c *DonationAlertsClient) GetRecentDonations() ([]DADonation, error) {
	req, err := http.NewRequest(http.MethodGet, "https://www.donationalerts.com/api/v1/alerts/donations", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request donations: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result DAListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode donations: %w", err)
	}

	return result.Data, nil
}

func (c *DonationAlertsClient) CreatePaymentLink(amount float64, comment string) string {
	return fmt.Sprintf(
		"https://www.donationalerts.com/r/%s?amount=%.2f&comment=%s",
		c.pageName, c.CalcRequiredPayment(amount), comment,
	)
}
