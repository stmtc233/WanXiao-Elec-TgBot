package wanxiao

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const APIURL = "https://xqh5.17wanxiao.com/smartWaterAndElectricityService/SWAEServlet"

type Client struct {
	HTTPClient *http.Client
}

func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

type Param struct {
	Cmd       string `json:"cmd"`
	Account   string `json:"account"`
	Timestamp string `json:"timestamp"`
}

type Response struct {
	Code    int    `json:"code_"`
	Sign    string `json:"sign"`
	Result  string `json:"result_"`
	Body    string `json:"body"`
	Message string `json:"message_"`
}

type Body struct {
	Result     string   `json:"result"`
	AccountNum string   `json:"account_num"`
	RoomFull   string   `json:"roomfullname"`
	DetailList []Detail `json:"detaillist"`
	Message    string   `json:"message"`
	RoomVerify string   `json:"roomverify"`
}

type Detail struct {
	Use          string `json:"use"`
	Odd          string `json:"odd"` // Balance
	BusinessType string `json:"businesstype"`
	Status       string `json:"status"`
}

// RoomInfo simplifies the result for the bot
type RoomInfo struct {
	RoomName string
	Balance  float64
}

func (c *Client) GetBalance(account, customerCode string) ([]RoomInfo, error) {
	// Create timestamp
	timestamp := time.Now().Format("20060102150405000") // Format used in example: 20251129230945222

	// Construct param JSON
	paramData := Param{
		Cmd:       "getbindroom",
		Account:   account,
		Timestamp: timestamp,
	}

	paramJSON, err := json.Marshal(paramData)
	if err != nil {
		return nil, fmt.Errorf("marshal param error: %v", err)
	}

	// Prepare form data
	data := url.Values{}
	data.Set("param", string(paramJSON))
	data.Set("customercode", customerCode)

	req, err := http.NewRequest("POST", APIURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request error: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request error: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body error: %v", err)
	}

	var wxResp Response
	if err := json.Unmarshal(bodyBytes, &wxResp); err != nil {
		return nil, fmt.Errorf("unmarshal response error: %v", err)
	}

	if wxResp.Code != 0 {
		return nil, fmt.Errorf("api error: %s", wxResp.Message)
	}

	var bodyData Body
	if err := json.Unmarshal([]byte(wxResp.Body), &bodyData); err != nil {
		return nil, fmt.Errorf("unmarshal inner body error: %v", err)
	}

	// Parse results
	// The example JSON shows "detaillist" containing the "odd" (balance).
	// However, the task mentions "returned likely multiple rooms", but the example shows one structure "body"
	// with "detaillist". Usually "getbindroom" might return info for the bound account.
	// If the structure allows multiple rooms, we should check if Body or DetailList handles that.
	// Based on "detaillist", it seems to be a list of details for the account.
	// If "odd" is per detail, we iterate.

	var rooms []RoomInfo

	// Assuming detail list contains the electricity info
	for _, detail := range bodyData.DetailList {
		balance, _ := strconv.ParseFloat(detail.Odd, 64)
		rooms = append(rooms, RoomInfo{
			RoomName: bodyData.RoomFull, // The example has roomfullname at the top level of body
			Balance:  balance,
		})
	}

	// If detaillist is empty but body has fields, we might need to look there,
	// but the example shows detaillist having the 'odd' field.

	return rooms, nil
}
