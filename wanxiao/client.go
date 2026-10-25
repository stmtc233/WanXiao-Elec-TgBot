package wanxiao

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	APIURL   = "https://xqh5.17wanxiao.com/smartWaterAndElectricityService/SWAEServlet"
	GoPayURL = "https://xqh5.17wanxiao.com/smartWaterAndElectricityService/goPay"
	TradeURL = "https://cloudpaygateway.59wanmei.com:8087/paygateway/smallpaygateway/trade"

	userAgent       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	maxResponseSize = 1 << 20
)

// SupportedSchoolCode defines the whitelisted school codes for recharge
const SupportedSchoolCode = "1399"

// APIError is a business error whose message is safe to show to users.
type APIError struct {
	Msg string
}

func (e *APIError) Error() string { return e.Msg }

func apiErrorf(format string, args ...interface{}) error {
	return &APIError{Msg: fmt.Sprintf(format, args...)}
}

type Client struct {
	HTTPClient *http.Client
}

func NewClient() *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 10
	return &Client{
		HTTPClient: &http.Client{Timeout: 15 * time.Second, Transport: transport},
	}
}

// post sends a request and returns the (size limited) response body.
func (c *Client) post(endpoint, contentType string, body string) ([]byte, error) {
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request error: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read body error: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected http status %d", resp.StatusCode)
	}
	return bodyBytes, nil
}

func (c *Client) postForm(endpoint string, data url.Values) ([]byte, error) {
	return c.post(endpoint, "application/x-www-form-urlencoded", data.Encode())
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
	RoomName   string
	RoomVerify string
	Balance    float64
}

// GetRoomDetails queries getbindroom from SWAEServlet
func (c *Client) GetRoomDetails(account, customerCode string) (*Body, error) {
	paramJSON, err := json.Marshal(Param{
		Cmd:       "getbindroom",
		Account:   account,
		Timestamp: time.Now().Format("20060102150405000"),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal param error: %w", err)
	}

	data := url.Values{}
	data.Set("param", string(paramJSON))
	data.Set("customercode", customerCode)

	bodyBytes, err := c.postForm(APIURL, data)
	if err != nil {
		return nil, err
	}

	var wxResp Response
	if err := json.Unmarshal(bodyBytes, &wxResp); err != nil {
		return nil, fmt.Errorf("unmarshal response error: %w", err)
	}
	if wxResp.Code != 0 {
		return nil, apiErrorf("%s", wxResp.Message)
	}

	var bodyData Body
	if err := json.Unmarshal([]byte(wxResp.Body), &bodyData); err != nil {
		return nil, fmt.Errorf("unmarshal inner body error: %w", err)
	}
	return &bodyData, nil
}

func (c *Client) GetBalance(account, customerCode string) ([]RoomInfo, error) {
	bodyData, err := c.GetRoomDetails(account, customerCode)
	if err != nil {
		return nil, err
	}

	rooms := make([]RoomInfo, 0, len(bodyData.DetailList))
	for _, detail := range bodyData.DetailList {
		balance, _ := strconv.ParseFloat(detail.Odd, 64)
		rooms = append(rooms, RoomInfo{
			RoomName:   bodyData.RoomFull,
			RoomVerify: bodyData.RoomVerify,
			Balance:    balance,
		})
	}
	return rooms, nil
}

type AuthResponse struct {
	ReturnCode   string       `json:"returncode"`
	ReturnMsg    string       `json:"returnmsg"`
	BusinessData AuthUserData `json:"businessData"`
}

type AuthUserData struct {
	Username string `json:"username"`
	OrgName  string `json:"orgname"`
	OrgCode  string `json:"orgcode"`
	IdSerial string `json:"idserial"`
}

// GetStudentInfo queries the student's real name and school name without login
func (c *Client) GetStudentInfo(account, customerCode string) (string, string, error) {
	bizJSON, _ := json.Marshal(map[string]string{
		"token":      "",
		"idserial":   account,
		"schoolcode": customerCode,
	})
	reqBody, _ := json.Marshal(map[string]interface{}{
		"timestamp":  time.Now().Format("2006-01-02 15:04:05"),
		"method":     "smallProgramUserAuth",
		"bizcontent": string(bizJSON),
		"sourceId":   2,
	})

	bodyBytes, err := c.post(TradeURL, "application/json;charset=UTF-8", string(reqBody))
	if err != nil {
		return "", "", err
	}

	var authResp AuthResponse
	if err := json.Unmarshal(bodyBytes, &authResp); err != nil {
		return "", "", fmt.Errorf("unmarshal auth response error: %w", err)
	}
	if authResp.ReturnCode != "SUCCESS" {
		return "", "", apiErrorf("学生信息校验失败: %s (请确认绑定的学号是否正确)", authResp.ReturnMsg)
	}
	if authResp.BusinessData.Username == "" {
		return "", "", apiErrorf("未查询到学生姓名 (请确认绑定的学号是否正确)")
	}
	return authResp.BusinessData.Username, authResp.BusinessData.OrgName, nil
}

type GoPayResponse struct {
	Code    int    `json:"code_"`
	Result  string `json:"result_"`
	Body    string `json:"body"`
	Message string `json:"message_"`
}

type GoPayBody struct {
	Result  int       `json:"result"`
	Message string    `json:"message"`
	Data    GoPayData `json:"data"`
}

type GoPayData struct {
	URL      string `json:"url"`
	JsonData string `json:"jsonData"`
	Sign     string `json:"sign"`
}

// RechargeRequest describes a recharge. RoomVerify/RoomName and RealName/SchoolName
// are optional caches; missing values are fetched from the API.
type RechargeRequest struct {
	Account      string
	CustomerCode string
	RoomVerify   string
	RoomName     string
	RealName     string
	SchoolName   string
	AmountYuan   float64
}

// GetRechargeURL calls goPay to generate the /paygateway/YunPayServie/redirectToPayFeeH5 payment URL
func (c *Client) GetRechargeURL(r RechargeRequest) (string, error) {
	if r.CustomerCode != SupportedSchoolCode {
		return "", apiErrorf("当前充值功能仅支持学校代码为 %s 的院校", SupportedSchoolCode)
	}

	payamtCents := int(math.Round(r.AmountYuan * 100))
	if payamtCents <= 0 {
		return "", apiErrorf("充值金额必须大于0")
	}

	// 1. Ensure room info
	if r.RoomVerify == "" || r.RoomName == "" {
		details, err := c.GetRoomDetails(r.Account, r.CustomerCode)
		if err != nil {
			return "", err
		}
		r.RoomVerify = details.RoomVerify
		r.RoomName = details.RoomFull
	}
	if r.RoomVerify == "" {
		return "", apiErrorf("未获取到有效的房间验证号(roomverify)")
	}

	// 2. Student real name & school name (critical for gateway validation!)
	if r.RealName == "" {
		realName, schoolName, err := c.GetStudentInfo(r.Account, r.CustomerCode)
		if err != nil {
			return "", err
		}
		r.RealName, r.SchoolName = realName, schoolName
	}

	// 3. For login-free H5 page, token MUST remain empty ("")!
	// If token is set to a non-existent UUID, the H5 page's smallProgramUserAuth will fail with "用户不存在".
	token := ""
	journo := fmt.Sprintf("T002%s%s", time.Now().Format("20060102150405"), uuid.New().String()[:6])

	customJSONBytes, _ := json.Marshal(map[string]string{
		"idserial": r.Account,
		"username": url.QueryEscape(r.RealName),
		"wxToken":  "",
	})

	payload := map[string]interface{}{
		"cmd":           "getPayInfo",
		"thirdsource":   "T002",
		"token":         token,
		"wxToken":       token,
		"idserial":      r.Account,
		"username":      url.QueryEscape(r.RealName),
		"txcode":        "51101", // 电费
		"acccode":       "51101",
		"payamt":        strconv.Itoa(payamtCents),
		"schoolcode":    r.CustomerCode,
		"customerName":  url.QueryEscape(r.SchoolName),
		"returnurl":     "https://xqh5.17wanxiao.com/userwaterelecmini/index.html#/pages/index/index",
		"payaccnum":     r.RoomVerify,
		"payaccname":    url.QueryEscape(r.RoomName),
		"summary":       "",
		"extendinfo":    "",
		"paychannel":    "13",
		"journo":        journo,
		"roomLimitAmt":  nil,
		"checkIdentity": "1",
		"flag":          "cassso_zhkjxysdZ",
		"customizedPay": r.CustomerCode,
		"customJson":    string(customJSONBytes),
	}

	paramJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal pay param error: %w", err)
	}

	formData := url.Values{}
	formData.Set("param", string(paramJSON))
	formData.Set("customercode", r.CustomerCode)
	formData.Set("method", "getPayInfo")
	formData.Set("command", "JBSWaterElecService")

	bodyBytes, err := c.postForm(GoPayURL, formData)
	if err != nil {
		return "", fmt.Errorf("goPay %w", err)
	}

	var goResp GoPayResponse
	if err := json.Unmarshal(bodyBytes, &goResp); err != nil {
		return "", fmt.Errorf("unmarshal goPay response error: %w", err)
	}
	if goResp.Code != 0 || goResp.Body == "" {
		return "", apiErrorf("支付网关返回错误: %s", goResp.Message)
	}

	var goBody GoPayBody
	if err := json.Unmarshal([]byte(goResp.Body), &goBody); err != nil {
		return "", fmt.Errorf("unmarshal goPay body error: %w", err)
	}
	if goBody.Result != 0 {
		return "", apiErrorf("支付网关业务失败: %s", goBody.Message)
	}

	data := goBody.Data
	if data.URL == "" || data.JsonData == "" || data.Sign == "" {
		return "", apiErrorf("支付网关返回的支付数据不完整")
	}

	return fmt.Sprintf("%s?jsonData=%s&sign=%s", data.URL, url.QueryEscape(data.JsonData), url.QueryEscape(data.Sign)), nil
}
