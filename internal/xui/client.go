package xui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	username   string
	password   string
	inboundID  int
	httpClient *http.Client
	sessionCookie string
}

type ClientInfo struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Enable     bool   `json:"enable"`
	TotalGB    int64  `json:"totalGB"`
	ExpiryTime int64  `json:"expiryTime"`
	Flow       string `json:"flow"`
}

type InboundClientStat struct {
	ID      int64  `json:"id"`
	InboundID int  `json:"inboundId"`
	Enable  bool   `json:"enable"`
	Email   string `json:"email"`
	Up      int64  `json:"up"`
	Down    int64  `json:"down"`
	Total   int64  `json:"total"`
}

type InboundResponse struct {
	Success bool   `json:"success"`
	Msg     string `json:"msg"`
	Obj     struct {
		ClientStats []InboundClientStat `json:"clientStats"`
		Settings    string              `json:"settings"`
		StreamSettings string           `json:"streamSettings"`
		Protocol    string              `json:"protocol"`
		Remark      string              `json:"remark"`
	} `json:"obj"`
}

type StreamSettings struct {
	Network         string `json:"network"`
	Security        string `json:"security"`
	RealitySettings struct {
		Dest        string   `json:"dest"`
		ServerNames []string `json:"serverNames"`
		PrivateKey  string   `json:"privateKey"`
		ShortIds    []string `json:"shortIds"`
	} `json:"realitySettings"`
}

func New(baseURL, username, password string, inboundID int) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("cookiejar: %w", err)
	}
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		username:  username,
		password:  password,
		inboundID: inboundID,
		httpClient: &http.Client{
			Jar:     jar,
			Timeout: 15 * time.Second,
		},
	}, nil
}

func (c *Client) login() error {
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, c.username, c.password)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/login", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode login response: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("login failed: %s", result.Msg)
	}
	return nil
}

func (c *Client) doRequest(method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.login(); err != nil {
			return nil, fmt.Errorf("re-login: %w", err)
		}
		return c.doRequest(method, path, body)
	}

	return io.ReadAll(resp.Body)
}

func (c *Client) AddClient(uuid, email string) error {
	clientJSON := fmt.Sprintf(`{"clients":[{"id":%q,"email":%q,"enable":true,"flow":"xtls-rprx-vision","totalGB":0,"expiryTime":0,"limitIp":0}]}`, uuid, email)

	payload := map[string]interface{}{
		"id":       c.inboundID,
		"settings": clientJSON,
	}

	data, err := c.doRequest(http.MethodPost, "/panel/api/inbounds/addClient", payload)
	if err != nil {
		return err
	}

	var result struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("decode addClient: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("addClient failed: %s", result.Msg)
	}
	return nil
}

func (c *Client) DeleteClient(uuid string) error {
	path := fmt.Sprintf("/panel/api/inbounds/%d/delClient/%s", c.inboundID, uuid)
	data, err := c.doRequest(http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	var result struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("decode delClient: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("delClient failed: %s", result.Msg)
	}
	return nil
}

func (c *Client) SetClientEnabled(uuid, email string, enabled bool) error {
	clientJSON := fmt.Sprintf(`{"clients":[{"id":%q,"email":%q,"enable":%v,"flow":"xtls-rprx-vision","totalGB":0,"expiryTime":0,"limitIp":0}]}`, uuid, email, enabled)

	payload := map[string]interface{}{
		"id":       c.inboundID,
		"settings": clientJSON,
	}

	path := fmt.Sprintf("/panel/api/inbounds/updateClient/%s", uuid)
	data, err := c.doRequest(http.MethodPost, path, payload)
	if err != nil {
		return err
	}
	var result struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("decode updateClient: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("updateClient failed: %s", result.Msg)
	}
	return nil
}

func (c *Client) GetInbound() (*InboundResponse, error) {
	path := fmt.Sprintf("/panel/api/inbounds/get/%d", c.inboundID)
	data, err := c.doRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var result InboundResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode inbound: %w", err)
	}
	return &result, nil
}

func (c *Client) GetClientTraffic(email string) (up, down int64, err error) {
	inbound, err := c.GetInbound()
	if err != nil {
		return 0, 0, err
	}
	for _, cs := range inbound.Obj.ClientStats {
		if cs.Email == email {
			return cs.Up, cs.Down, nil
		}
	}
	return 0, 0, fmt.Errorf("client %q not found in inbound", email)
}

func (c *Client) GetAllClientTraffic() (map[string]int64, error) {
	inbound, err := c.GetInbound()
	if err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(inbound.Obj.ClientStats))
	for _, cs := range inbound.Obj.ClientStats {
		result[cs.Email] = cs.Up + cs.Down
	}
	return result, nil
}

func (c *Client) GetInboundStreamSettings() (*StreamSettings, error) {
	inbound, err := c.GetInbound()
	if err != nil {
		return nil, err
	}
	var ss StreamSettings
	if err := json.Unmarshal([]byte(inbound.Obj.StreamSettings), &ss); err != nil {
		return nil, fmt.Errorf("decode stream settings: %w", err)
	}
	return &ss, nil
}

func (c *Client) BuildVlessLink(uuid, remark string) (string, error) {
	ss, err := c.GetInboundStreamSettings()
	if err != nil {
		return "", err
	}

	host := strings.TrimPrefix(strings.TrimPrefix(c.baseURL, "http://"), "https://")
	colonIdx := strings.LastIndex(host, ":")
	if colonIdx > 0 {
		host = host[:colonIdx]
	}

	serverName := ""
	if len(ss.RealitySettings.ServerNames) > 0 {
		serverName = ss.RealitySettings.ServerNames[0]
	}
	shortID := ""
	if len(ss.RealitySettings.ShortIds) > 0 {
		shortID = ss.RealitySettings.ShortIds[0]
	}

	link := fmt.Sprintf(
		"vless://%s@%s:443?type=%s&security=%s&pbk=%s&fp=chrome&sni=%s&sid=%s&spx=%%2F&flow=xtls-rprx-vision#%s",
		uuid, host, ss.Network, ss.Security,
		ss.RealitySettings.PrivateKey,
		serverName, shortID, remark,
	)
	return link, nil
}

func (c *Client) EnsureLoggedIn() error {
	return c.login()
}
