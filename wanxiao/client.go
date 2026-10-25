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
)

type Client struct {
	HTTPClient *http.Client
}

func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
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
	RoomName   string
	RoomVerify string
	Balance    float64
}

// GetRoomDetails queries getbindroom from SWAEServlet
func (c *Client) GetRoomDetails(account, customerCode string) (*Body, error) {
	timestamp := time.Now().Format("20060102150405000")

	paramData := Param{
		Cmd:       "getbindroom",
		Account:   account,
		Timestamp: timestamp,
	}

	paramJSON, err := json.Marshal(paramData)
	if err != nil {
		return nil, fmt.Errorf("marshal param error: %v", err)
	}

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

	return &bodyData, nil
}

func (c *Client) GetBalance(account, customerCode string) ([]RoomInfo, error) {
	bodyData, err := c.GetRoomDetails(account, customerCode)
	if err != nil {
		return nil, err
	}

	var rooms []RoomInfo
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
	bizContent := map[string]string{
		"token":      "",
		"idserial":   account,
		"schoolcode": customerCode,
	}
	bizJSON, _ := json.Marshal(bizContent)

	payload := map[string]interface{}{
		"timestamp":  time.Now().Format("2006-01-02 15:04:05"),
		"method":     "smallProgramUserAuth",
		"bizcontent": string(bizJSON),
		"sourceId":   2,
	}
	reqBody, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", TradeURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	var authResp AuthResponse
	if err := json.Unmarshal(bodyBytes, &authResp); err != nil {
		return "", "", err
	}

	if authResp.ReturnCode != "SUCCESS" {
		return "", "", fmt.Errorf("user auth failed: %s", authResp.ReturnMsg)
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

// SupportedSchoolCode defines the whitelisted school codes for recharge
const SupportedSchoolCode = "1399"

// GetRechargeURL calls goPay to generate the /paygateway/YunPayServie/redirectToPayFeeH5 payment URL
func (c *Client) GetRechargeURL(account, customerCode, roomVerify, roomFull string, amountYuan float64) (string, error) {
	if customerCode != SupportedSchoolCode {
		return "", fmt.Errorf("当前充值功能仅支持学校代码为 %s 的院校", SupportedSchoolCode)
	}

	// 1. Ensure room info
	if roomVerify == "" || roomFull == "" {
		details, err := c.GetRoomDetails(account, customerCode)
		if err != nil {
			return "", fmt.Errorf("获取房间信息失败: %v", err)
		}
		roomVerify = details.RoomVerify
		roomFull = details.RoomFull
	}

	if roomVerify == "" {
		return "", fmt.Errorf("未获取到有效的房间验证号(roomverify)")
	}

	payamtCents := int(math.Round(amountYuan * 100))
	if payamtCents <= 0 {
		return "", fmt.Errorf("充值金额必须大于0")
	}

	// 2. Query student real name & school name (critical for gateway validation!)
	realName, schoolName, err := c.GetStudentInfo(account, customerCode)
	if err != nil || realName == "" {
		if err == nil {
			err = fmt.Errorf("未查询到学生姓名")
		}
		return "", fmt.Errorf("学生信息校验失败: %v (请确认绑定的学号是否正确)", err)
	}

	// 3. For login-free H5 page, token MUST remain empty ("")!
	// If token is set to a non-existent UUID, the H5 page's smallProgramUserAuth will fail with "用户不存在".
	token := ""
	journo := fmt.Sprintf("T002%s%s", time.Now().Format("20060102150405"), uuid.New().String()[:6])

	customJSONMap := map[string]string{
		"idserial": account,
		"username": url.QueryEscape(realName),
		"wxToken":  "",
	}
	customJSONBytes, _ := json.Marshal(customJSONMap)

	payload := map[string]interface{}{
		"cmd":           "getPayInfo",
		"thirdsource":   "T002",
		"token":         token,
		"wxToken":       token,
		"idserial":      account,
		"username":      url.QueryEscape(realName),
		"txcode":        "51101", // 电费
		"acccode":       "51101",
		"payamt":        strconv.Itoa(payamtCents),
		"schoolcode":    customerCode,
		"customerName":  url.QueryEscape(schoolName),
		"returnurl":     "https://xqh5.17wanxiao.com/userwaterelecmini/index.html#/pages/index/index",
		"payaccnum":     roomVerify,
		"payaccname":    url.QueryEscape(roomFull),
		"summary":       "",
		"extendinfo":    "",
		"paychannel":    "13",
		"journo":        journo,
		"roomLimitAmt":  nil,
		"checkIdentity": "1",
		"flag":          "cassso_zhkjxysdZ",
		"customizedPay": customerCode,
		"customJson":    string(customJSONBytes),
	}

	paramJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("序列化支付参数失败: %v", err)
	}

	formData := url.Values{}
	formData.Set("param", string(paramJSON))
	formData.Set("customercode", customerCode)
	formData.Set("method", "getPayInfo")
	formData.Set("command", "JBSWaterElecService")

	req, err := http.NewRequest("POST", GoPayURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求 goPay 接口失败: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取 goPay 响应失败: %v", err)
	}

	var goResp GoPayResponse
	if err := json.Unmarshal(bodyBytes, &goResp); err != nil {
		return "", fmt.Errorf("解析 goPay 响应失败: %v", err)
	}

	if goResp.Code != 0 || goResp.Body == "" {
		return "", fmt.Errorf("goPay 返回错误: %s", goResp.Message)
	}

	var goBody GoPayBody
	if err := json.Unmarshal([]byte(goResp.Body), &goBody); err != nil {
		return "", fmt.Errorf("解析 goPay 内部数据失败: %v", err)
	}

	if goBody.Result != 0 {
		return "", fmt.Errorf("goPay 业务失败: %s", goBody.Message)
	}

	data := goBody.Data
	if data.URL == "" || data.JsonData == "" || data.Sign == "" {
		return "", fmt.Errorf("goPay 返回的支付数据不完整")
	}

	rechargeURL := fmt.Sprintf("%s?jsonData=%s&sign=%s", data.URL, url.QueryEscape(data.JsonData), url.QueryEscape(data.Sign))
	return rechargeURL, nil
}
