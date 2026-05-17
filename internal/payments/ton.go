package payments

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type TONChecker struct {
	walletAddress   string
	apiKey          string
	tonToRUB        float64
	usdtToRUB       float64
	usdtMasterAddr  string
	httpClient      *http.Client
}

type TONTransaction struct {
	Hash        string `json:"hash"`
	InMsg       *TONMessage `json:"in_msg"`
	OutMsgs     []TONMessage `json:"out_msgs"`
}

type TONMessage struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Value       string `json:"value"`
	Comment     string `json:"comment"`
	MsgData     struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"msg_data"`
}

type TONTransactionsResponse struct {
	OK     bool             `json:"ok"`
	Result []TONTransaction `json:"result"`
}

type JettonTransfer struct {
	TransactionHash string `json:"transaction_hash"`
	QueryID         string `json:"query_id"`
	Amount          string `json:"amount"`
	Source          string `json:"source"`
	Destination     string `json:"destination"`
	JettonMaster    string `json:"jetton_master"`
	Comment         string `json:"comment"`
	Timestamp       int64  `json:"utime"`
}

type JettonTransfersResponse struct {
	Transfers []JettonTransfer `json:"transfers"`
}

type ParsedPayment struct {
	Hash      string
	UserID    int64
	AmountRUB float64
	IsUSDT    bool
}

func NewTONChecker(walletAddress, apiKey string, tonToRUB, usdtToRUB float64, usdtMasterAddr string) *TONChecker {
	return &TONChecker{
		walletAddress:  walletAddress,
		apiKey:         apiKey,
		tonToRUB:       tonToRUB,
		usdtToRUB:      usdtToRUB,
		usdtMasterAddr: usdtMasterAddr,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *TONChecker) GetNewTONTransactions(afterLT int64) ([]ParsedPayment, int64, error) {
	url := fmt.Sprintf(
		"https://toncenter.com/api/v2/getTransactions?address=%s&limit=20&to_lt=%d&archival=false",
		c.walletAddress, afterLT,
	)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, afterLT, err
	}
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, afterLT, fmt.Errorf("TON request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, afterLT, err
	}

	var result TONTransactionsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, afterLT, fmt.Errorf("decode TON txs: %w", err)
	}
	if !result.OK {
		return nil, afterLT, fmt.Errorf("TON API returned not OK")
	}

	var payments []ParsedPayment
	var maxLT int64 = afterLT

	for _, tx := range result.Result {
		if tx.InMsg == nil {
			continue
		}
		msg := tx.InMsg

		comment := extractComment(msg)
		userID, err := strconv.ParseInt(strings.TrimSpace(comment), 10, 64)
		if err != nil || userID <= 0 {
			continue
		}

		nanoTON, err := strconv.ParseInt(msg.Value, 10, 64)
		if err != nil || nanoTON <= 0 {
			continue
		}

		tonAmount := float64(nanoTON) / 1e9
		rubAmount := tonAmount * c.tonToRUB

		payments = append(payments, ParsedPayment{
			Hash:      tx.Hash,
			UserID:    userID,
			AmountRUB: rubAmount,
			IsUSDT:    false,
		})
	}

	return payments, maxLT, nil
}

func (c *TONChecker) GetNewJettonTransfers(processedHashes map[string]struct{}) ([]ParsedPayment, error) {
	url := fmt.Sprintf(
		"https://toncenter.com/api/v3/jetton/transfers?direction=in&account=%s&limit=20",
		c.walletAddress,
	)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jetton request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result JettonTransfersResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode jetton txs: %w", err)
	}

	var payments []ParsedPayment
	for _, tr := range result.Transfers {
		if _, seen := processedHashes[tr.TransactionHash]; seen {
			continue
		}

		if !strings.EqualFold(tr.JettonMaster, c.usdtMasterAddr) {
			continue
		}

		userID, err := strconv.ParseInt(strings.TrimSpace(tr.Comment), 10, 64)
		if err != nil || userID <= 0 {
			continue
		}

		microUSDT, err := strconv.ParseInt(tr.Amount, 10, 64)
		if err != nil || microUSDT <= 0 {
			continue
		}

		usdtAmount := float64(microUSDT) / 1e6
		rubAmount := usdtAmount * c.usdtToRUB

		payments = append(payments, ParsedPayment{
			Hash:      tr.TransactionHash,
			UserID:    userID,
			AmountRUB: rubAmount,
			IsUSDT:    true,
		})
	}

	return payments, nil
}

func extractComment(msg *TONMessage) string {
	if msg.Comment != "" {
		return msg.Comment
	}
	if msg.MsgData.Text != "" {
		return msg.MsgData.Text
	}
	return ""
}

func (c *TONChecker) WalletAddress() string {
	return c.walletAddress
}
