package bot

import (
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"wanxiao-elec-bot/model"
	"wanxiao-elec-bot/wanxiao"

	"gopkg.in/telebot.v3"
	"gorm.io/gorm"
)

// User States
const (
	StateNone = iota
	StateAddAccount_WaitAccount
	StateAddAccount_WaitCode
	StateSettings_WaitThreshold
	StateSettings_WaitInterval
	StateRecharge_WaitAmount
)

type Bot struct {
	B      *telebot.Bot
	DB     *gorm.DB
	Client *wanxiao.Client

	// State management
	states    map[int64]int
	tempData  map[int64]map[string]string
	stateLock sync.RWMutex
}

func escapeMarkdownV2(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!', '\\':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func escapeMarkdownV2Code(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '`', '\\':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Keyboards
var (
	// Main Menu (Reply Keyboard)
	menuBtnElec     = telebot.Btn{Text: "🔌 查询电量"}
	menuBtnRecharge = telebot.Btn{Text: "⚡ 充值电费"}
	menuBtnAccounts = telebot.Btn{Text: "👤 账号管理"}
	menuBtnSettings = telebot.Btn{Text: "⚙️ 预警设置"}
	menuKeyboard    = &telebot.ReplyMarkup{ResizeKeyboard: true}

	// Inline Buttons (Registered Endpoints)
	btnAddAccount   = telebot.Btn{Text: "➕ 添加账号", Unique: "add_acc"}
	btnToggleAlert  = telebot.Btn{Text: "🔔 开关预警", Unique: "toggle_alert"}
	btnSetThreshold = telebot.Btn{Text: "📉 修改阈值", Unique: "set_thres"}
	btnSetInterval  = telebot.Btn{Text: "⏱️ 修改间隔", Unique: "set_inter"}

	// Callback endpoints for recharge & unbind
	btnPayAccEndpoint    = telebot.Btn{Unique: "pay_acc"}
	btnAmtEndpoint       = telebot.Btn{Unique: "amt"}
	btnAmtCustomEndpoint = telebot.Btn{Unique: "amt_custom"}
	btnUnbindEndpoint    = telebot.Btn{Unique: "unbind"}
)

func NewBot(token string, db *gorm.DB) (*Bot, error) {
	pref := telebot.Settings{
		Token:  token,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
		OnError: func(err error, c telebot.Context) {
			log.Printf("[Bot Error] %v", err)
		},
	}

	b, err := telebot.NewBot(pref)
	if err != nil {
		return nil, err
	}

	bot := &Bot{
		B:        b,
		DB:       db,
		Client:   wanxiao.NewClient(),
		states:   make(map[int64]int),
		tempData: make(map[int64]map[string]string),
	}

	// Init keyboards
	menuKeyboard.Reply(
		menuKeyboard.Row(menuBtnElec, menuBtnRecharge),
		menuKeyboard.Row(menuBtnAccounts, menuBtnSettings),
	)

	bot.registerHandlers()
	return bot, nil
}

func (bot *Bot) Start() {
	bot.B.Start()
}

func (bot *Bot) registerHandlers() {
	// Commands
	bot.B.Handle("/start", bot.handleStart)
	bot.B.Handle("/elec", bot.handleElec)
	bot.B.Handle("/recharge", bot.handleRecharge)
	bot.B.Handle("/pay", bot.handleRecharge)

	// Menu Buttons
	bot.B.Handle(&menuBtnElec, bot.handleElec)
	bot.B.Handle(&menuBtnRecharge, bot.handleRecharge)
	bot.B.Handle(&menuBtnAccounts, bot.handleAccounts)
	bot.B.Handle(&menuBtnSettings, bot.handleSettings)

	// Static Inline Buttons
	bot.B.Handle(&btnAddAccount, bot.handleAddAccountBtn)
	bot.B.Handle(&btnToggleAlert, bot.handleToggleAlert)
	bot.B.Handle(&btnSetThreshold, bot.handleSetThresholdBtn)
	bot.B.Handle(&btnSetInterval, bot.handleSetIntervalBtn)

	// Dynamic Inline Callback Endpoints (Direct registration in telebot)
	bot.B.Handle(&btnPayAccEndpoint, bot.handlePayAccCallback)
	bot.B.Handle(&btnAmtEndpoint, bot.handleAmtCallback)
	bot.B.Handle(&btnAmtCustomEndpoint, bot.handleAmtCustomCallback)
	bot.B.Handle(&btnUnbindEndpoint, bot.handleUnbindCallback)

	// Generic Text Handler (for inputs)
	bot.B.Handle(telebot.OnText, bot.handleText)

	// Fallback Callback Handler
	bot.B.Handle(telebot.OnCallback, bot.handleCallbackFallback)
}

// Helper to manage state
func (bot *Bot) setState(userID int64, state int) {
	bot.stateLock.Lock()
	defer bot.stateLock.Unlock()
	bot.states[userID] = state
	if state == StateNone {
		delete(bot.tempData, userID)
	}
}

func (bot *Bot) getState(userID int64) int {
	bot.stateLock.RLock()
	defer bot.stateLock.RUnlock()
	return bot.states[userID]
}

func (bot *Bot) setTempData(userID int64, key, value string) {
	bot.stateLock.Lock()
	defer bot.stateLock.Unlock()
	if bot.tempData[userID] == nil {
		bot.tempData[userID] = make(map[string]string)
	}
	bot.tempData[userID][key] = value
}

func (bot *Bot) getTempData(userID int64, key string) string {
	bot.stateLock.RLock()
	defer bot.stateLock.RUnlock()
	if bot.tempData[userID] == nil {
		return ""
	}
	return bot.tempData[userID][key]
}

// --- Handlers ---

func (bot *Bot) handleStart(c telebot.Context) error {
	log.Printf("[Start] user=%d", c.Sender().ID)
	user := model.User{ID: c.Sender().ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	bot.DB.FirstOrCreate(&user, model.User{ID: c.Sender().ID})
	bot.setState(c.Sender().ID, StateNone)
	return c.Send("欢迎使用完美校园电费监控与充值机器人！\n请通过下方菜单进行操作。", menuKeyboard)
}

// 🔌 Query Electricity
func (bot *Bot) handleElec(c telebot.Context) error {
	log.Printf("[Elec] Query requested by user=%d", c.Sender().ID)
	bot.setState(c.Sender().ID, StateNone)
	var bindings []model.Binding
	bot.DB.Where("user_id = ?", c.Sender().ID).Find(&bindings)

	if len(bindings) == 0 {
		return c.Send("未绑定账号。请先到“👤 账号管理”中添加账号。")
	}

	msg, _ := bot.B.Send(c.Recipient(), "正在查询中，请稍候...")

	statusMsg := "📊 *电量状态*:\n\n"
	menu := &telebot.ReplyMarkup{}
	var rows []telebot.Row

	for _, b := range bindings {
		rooms, err := bot.Client.GetBalance(b.Account, b.CustomerCode)
		if err != nil {
			log.Printf("[Elec] Query error for user=%d account=%s: %v", c.Sender().ID, b.Account, err)
			statusMsg += fmt.Sprintf("❌ 账号 `%s`: 查询失败 \\(%s\\)\n", escapeMarkdownV2Code(b.Account), escapeMarkdownV2(err.Error()))
			continue
		}

		for _, room := range rooms {
			statusMsg += fmt.Sprintf("🏠 *%s*\n⚡ 余额: `%.2f` 度\n\n", escapeMarkdownV2(room.RoomName), room.Balance)

			// Update cache
			b.LastBalance = room.Balance
			b.RoomName = room.RoomName
			b.RoomVerify = room.RoomVerify
			b.LastCheck = time.Now()
			bot.DB.Save(&b)

			if b.CustomerCode == wanxiao.SupportedSchoolCode {
				btnPay := telebot.Btn{
					Text:   fmt.Sprintf("⚡ 充值 %s", room.RoomName),
					Unique: "pay_acc",
					Data:   strconv.Itoa(int(b.ID)),
				}
				rows = append(rows, menu.Row(btnPay))
			}
		}
	}

	// Delete "checking" message and send result
	if msg != nil {
		bot.B.Delete(msg)
	}

	if len(rows) > 0 {
		menu.Inline(rows...)
		return c.Send(statusMsg, menu, telebot.ModeMarkdownV2)
	}
	return c.Send(statusMsg, telebot.ModeMarkdownV2)
}

// ⚡ Recharge Electricity
func (bot *Bot) handleRecharge(c telebot.Context) error {
	log.Printf("[Recharge] Menu requested by user=%d", c.Sender().ID)
	bot.setState(c.Sender().ID, StateNone)
	var bindings []model.Binding
	bot.DB.Where("user_id = ?", c.Sender().ID).Find(&bindings)

	if len(bindings) == 0 {
		return c.Send("未绑定账号。请先到“👤 账号管理”中添加账号。")
	}

	var supportedBindings []model.Binding
	for _, b := range bindings {
		if b.CustomerCode == wanxiao.SupportedSchoolCode {
			supportedBindings = append(supportedBindings, b)
		}
	}

	if len(supportedBindings) == 0 {
		return c.Send(fmt.Sprintf("⚠️ 当前电费充值功能仅支持学校代码为 %s 的院校（珠海科技学院）。\n您当前绑定的账号暂不支持在线充值。", wanxiao.SupportedSchoolCode))
	}

	if len(supportedBindings) == 1 {
		return bot.showAmountSelection(c, supportedBindings[0])
	}

	msg := "⚡ <b>请选择要充值的房间</b>:\n"
	menu := &telebot.ReplyMarkup{}
	var rows []telebot.Row
	for _, b := range supportedBindings {
		displayName := b.RoomName
		if displayName == "" {
			displayName = b.Account
		}
		btn := telebot.Btn{
			Text:   fmt.Sprintf("🏠 %s", displayName),
			Unique: "pay_acc",
			Data:   strconv.Itoa(int(b.ID)),
		}
		rows = append(rows, menu.Row(btn))
	}
	menu.Inline(rows...)
	return c.Send(msg, menu, telebot.ModeHTML)
}

func (bot *Bot) showAmountSelection(c telebot.Context, b model.Binding) error {
	roomDisplay := b.RoomName
	if roomDisplay == "" {
		roomDisplay = b.Account
	}

	log.Printf("[Recharge] Showing amount options for user=%d bindingID=%d room=%s", c.Sender().ID, b.ID, roomDisplay)

	msg := fmt.Sprintf("⚡ <b>充值电费</b>\n\n"+
		"🏠 房间: <code>%s</code>\n"+
		"👤 账号: <code>%s</code>\n"+
		"⚡ 当前余额: <code>%.2f</code> 度\n\n"+
		"请选择充值金额：",
		html.EscapeString(roomDisplay), html.EscapeString(b.Account), b.LastBalance)

	menu := &telebot.ReplyMarkup{}
	btn10 := telebot.Btn{Text: "10 元", Unique: "amt", Data: fmt.Sprintf("%d:10", b.ID)}
	btn20 := telebot.Btn{Text: "20 元", Unique: "amt", Data: fmt.Sprintf("%d:20", b.ID)}
	btn30 := telebot.Btn{Text: "30 元", Unique: "amt", Data: fmt.Sprintf("%d:30", b.ID)}
	btn50 := telebot.Btn{Text: "50 元", Unique: "amt", Data: fmt.Sprintf("%d:50", b.ID)}
	btn100 := telebot.Btn{Text: "100 元", Unique: "amt", Data: fmt.Sprintf("%d:100", b.ID)}
	btnCustom := telebot.Btn{Text: "✏️ 自定义金额", Unique: "amt_custom", Data: strconv.Itoa(int(b.ID))}

	menu.Inline(
		menu.Row(btn10, btn20, btn30),
		menu.Row(btn50, btn100),
		menu.Row(btnCustom),
	)
	return c.Send(msg, menu, telebot.ModeHTML)
}

func (bot *Bot) generateAndSendRechargeURL(c telebot.Context, b model.Binding, amount float64) error {
	if b.CustomerCode != wanxiao.SupportedSchoolCode {
		return c.Send(fmt.Sprintf("⚠️ 该账号所在学校暂不支持充值（仅支持学校代码 %s）。", wanxiao.SupportedSchoolCode))
	}

	log.Printf("[Recharge] Generating URL for user=%d account=%s roomVerify=%s amount=%.2f", c.Sender().ID, b.Account, b.RoomVerify, amount)
	waitMsg, _ := bot.B.Send(c.Recipient(), "正在生成充值链接，请稍候...")

	rechargeURL, err := bot.Client.GetRechargeURL(b.Account, b.CustomerCode, b.RoomVerify, b.RoomName, amount)
	if waitMsg != nil {
		bot.B.Delete(waitMsg)
	}

	if err != nil {
		log.Printf("[Recharge] Failed to generate recharge URL for user=%d: %v", c.Sender().ID, err)
		return c.Send(fmt.Sprintf("❌ 生成充值链接失败: %v", err))
	}

	log.Printf("[Recharge] Successfully generated URL for user=%d: %s", c.Sender().ID, rechargeURL)

	roomDisplay := b.RoomName
	if roomDisplay == "" {
		roomDisplay = b.Account
	}

	msg := fmt.Sprintf("⚡ <b>充值链接已生成</b>\n\n"+
		"🏠 房间: <code>%s</code>\n"+
		"👤 账号: <code>%s</code>\n"+
		"💰 充值金额: <code>%.2f</code> 元\n\n"+
		"🔗 <a href=\"%s\">👉 点击此处直接跳转充值</a>\n\n"+
		"如无法直接点击，可复制以下地址并在浏览器打开：\n<code>%s</code>",
		html.EscapeString(roomDisplay),
		html.EscapeString(b.Account),
		amount,
		html.EscapeString(rechargeURL),
		html.EscapeString(rechargeURL),
	)

	menu := &telebot.ReplyMarkup{}
	btnPay := menu.URL("👉 点击跳转至充值", rechargeURL)
	menu.Inline(menu.Row(btnPay))

	return c.Send(msg, menu, telebot.ModeHTML)
}

// Direct Callback Handlers
func (bot *Bot) handlePayAccCallback(c telebot.Context) error {
	_ = c.Respond()
	data := strings.TrimSpace(c.Callback().Data)
	log.Printf("[Callback] handlePayAccCallback user=%d data=%s", c.Sender().ID, data)

	id, err := strconv.Atoi(data)
	if err != nil {
		return c.Send("❌ 无效的账号 ID")
	}
	var b model.Binding
	if err := bot.DB.First(&b, id).Error; err != nil {
		return c.Send("❌ 未找到账号信息")
	}
	if b.CustomerCode != wanxiao.SupportedSchoolCode {
		return c.Send(fmt.Sprintf("⚠️ 该账号所在学校暂不支持充值（仅支持学校代码 %s）。", wanxiao.SupportedSchoolCode))
	}
	return bot.showAmountSelection(c, b)
}

func (bot *Bot) handleAmtCallback(c telebot.Context) error {
	_ = c.Respond()
	data := strings.TrimSpace(c.Callback().Data)
	log.Printf("[Callback] handleAmtCallback user=%d data=%s", c.Sender().ID, data)

	parts := strings.Split(data, ":")
	if len(parts) != 2 {
		return c.Send("❌ 无效的金额选项")
	}

	id, _ := strconv.Atoi(parts[0])
	amount, _ := strconv.ParseFloat(parts[1], 64)
	var b model.Binding
	if err := bot.DB.First(&b, id).Error; err != nil {
		return c.Send("❌ 未找到账号绑定信息")
	}
	return bot.generateAndSendRechargeURL(c, b, amount)
}

func (bot *Bot) handleAmtCustomCallback(c telebot.Context) error {
	_ = c.Respond()
	data := strings.TrimSpace(c.Callback().Data)
	log.Printf("[Callback] handleAmtCustomCallback user=%d data=%s", c.Sender().ID, data)

	bot.setTempData(c.Sender().ID, "recharge_binding_id", data)
	bot.setState(c.Sender().ID, StateRecharge_WaitAmount)
	return c.Send("请输入充值金额（元，例如 15 或 50）：")
}

func (bot *Bot) handleUnbindCallback(c telebot.Context) error {
	_ = c.Respond(&telebot.CallbackResponse{Text: "解绑成功"})
	account := strings.TrimSpace(c.Callback().Data)
	log.Printf("[Callback] handleUnbindCallback user=%d account=%s", c.Sender().ID, account)

	bot.DB.Where("user_id = ? AND account = ?", c.Sender().ID, account).Delete(&model.Binding{})
	return bot.handleAccounts(c)
}

// Fallback Callback Handler (in case any callback bypasses direct endpoint handlers)
func (bot *Bot) handleCallbackFallback(c telebot.Context) error {
	cb := c.Callback()
	if cb == nil {
		return nil
	}
	_ = c.Respond()

	unique := strings.TrimSpace(cb.Unique)
	data := strings.TrimSpace(cb.Data)

	// If telebot didn't parse \funique|payload:
	if unique == "" && strings.HasPrefix(data, "\f") {
		raw := strings.TrimPrefix(data, "\f")
		parts := strings.SplitN(raw, "|", 2)
		unique = parts[0]
		if len(parts) > 1 {
			data = parts[1]
		}
	}

	log.Printf("[Callback Fallback] user=%d unique=%s data=%s", c.Sender().ID, unique, data)

	switch unique {
	case "pay_acc":
		id, err := strconv.Atoi(data)
		if err != nil {
			return nil
		}
		var b model.Binding
		if err := bot.DB.First(&b, id).Error; err == nil {
			return bot.showAmountSelection(c, b)
		}
	case "amt":
		parts := strings.Split(data, ":")
		if len(parts) == 2 {
			id, _ := strconv.Atoi(parts[0])
			amount, _ := strconv.ParseFloat(parts[1], 64)
			var b model.Binding
			if err := bot.DB.First(&b, id).Error; err == nil {
				return bot.generateAndSendRechargeURL(c, b, amount)
			}
		}
	case "amt_custom":
		bot.setTempData(c.Sender().ID, "recharge_binding_id", data)
		bot.setState(c.Sender().ID, StateRecharge_WaitAmount)
		return c.Send("请输入充值金额（元，例如 15 或 50）：")
	case "unbind":
		bot.DB.Where("user_id = ? AND account = ?", c.Sender().ID, data).Delete(&model.Binding{})
		return bot.handleAccounts(c)
	}

	return nil
}

// 👤 Account Management
func (bot *Bot) handleAccounts(c telebot.Context) error {
	bot.setState(c.Sender().ID, StateNone)
	var bindings []model.Binding
	bot.DB.Where("user_id = ?", c.Sender().ID).Find(&bindings)

	menu := &telebot.ReplyMarkup{}

	msg := "📋 *账号列表*:\n"
	if len(bindings) == 0 {
		msg += "暂无绑定账号。\n"
	}

	var rows []telebot.Row
	rows = append(rows, menu.Row(btnAddAccount))

	for _, b := range bindings {
		msg += fmt.Sprintf("\\- `%s` \\(%s\\)\n", escapeMarkdownV2Code(b.Account), escapeMarkdownV2(b.RoomName))
		btnDelete := telebot.Btn{
			Text:   fmt.Sprintf("❌ 解绑 %s", b.Account),
			Unique: "unbind",
			Data:   b.Account,
		}
		rows = append(rows, menu.Row(btnDelete))
	}

	menu.Inline(rows...)
	return c.Send(msg, menu, telebot.ModeMarkdownV2)
}

func (bot *Bot) handleAddAccountBtn(c telebot.Context) error {
	_ = c.Respond()
	bot.setState(c.Sender().ID, StateAddAccount_WaitAccount)
	return c.Send("请输入 *账号* \\(学号或卡号\\):", telebot.ModeMarkdownV2)
}

// ⚙️ Settings
func (bot *Bot) handleSettings(c telebot.Context) error {
	bot.setState(c.Sender().ID, StateNone)
	var user model.User
	if err := bot.DB.First(&user, c.Sender().ID).Error; err != nil {
		bot.DB.Create(&model.User{ID: c.Sender().ID})
		return c.Send("初始化用户数据...")
	}

	msg := fmt.Sprintf("⚙️ *预警设置*:\n\n"+
		"📉 报警阈值: `%.2f` 度\n"+
		"🔔 预警开关: `%v`\n"+
		"⏱️ 检查间隔: `%d` 分钟",
		user.NotifyThreshold, user.NotifyEnabled, user.CheckInterval)

	menu := &telebot.ReplyMarkup{}
	menu.Inline(
		menu.Row(btnSetThreshold, btnSetInterval),
		menu.Row(btnToggleAlert),
	)

	return c.Send(msg, menu, telebot.ModeMarkdownV2)
}

func (bot *Bot) handleToggleAlert(c telebot.Context) error {
	_ = c.Respond(&telebot.CallbackResponse{Text: "设置已更新"})
	var user model.User
	if err := bot.DB.First(&user, c.Sender().ID).Error; err != nil {
		return nil
	}

	user.NotifyEnabled = !user.NotifyEnabled
	bot.DB.Save(&user)

	return bot.handleSettings(c)
}

func (bot *Bot) handleSetThresholdBtn(c telebot.Context) error {
	_ = c.Respond()
	bot.setState(c.Sender().ID, StateSettings_WaitThreshold)
	return c.Send("请输入新的 *报警阈值* \\(例如 10\\):", telebot.ModeMarkdownV2)
}

func (bot *Bot) handleSetIntervalBtn(c telebot.Context) error {
	_ = c.Respond()
	bot.setState(c.Sender().ID, StateSettings_WaitInterval)
	return c.Send("请输入新的 *检查间隔* \\(分钟，例如 60\\):", telebot.ModeMarkdownV2)
}

// Global Text Handler (State Machine)
func (bot *Bot) handleText(c telebot.Context) error {
	userID := c.Sender().ID
	state := bot.getState(userID)

	switch state {
	case StateAddAccount_WaitAccount:
		account := c.Text()
		bot.setTempData(userID, "account", account)
		bot.setState(userID, StateAddAccount_WaitCode)
		return c.Send(fmt.Sprintf("收到账号 `%s`。\n请继续输入 *学校代码 \\(Customer Code\\)*:", escapeMarkdownV2Code(account)), telebot.ModeMarkdownV2)

	case StateAddAccount_WaitCode:
		code := c.Text()
		account := bot.getTempData(userID, "account")

		c.Send("正在验证并绑定，请稍候...")

		// Verify
		rooms, err := bot.Client.GetBalance(account, code)
		if err != nil {
			bot.setState(userID, StateNone)
			return c.Send(fmt.Sprintf("❌ 验证失败: %v\n绑定流程已取消。", err))
		}
		if len(rooms) == 0 {
			bot.setState(userID, StateNone)
			return c.Send("❌ 未找到该账号的房间信息。绑定流程已取消。")
		}

		// Bind
		var binding model.Binding
		result := bot.DB.Where("user_id = ? AND account = ? AND customer_code = ?", userID, account, code).First(&binding)
		if result.RowsAffected > 0 {
			bot.setState(userID, StateNone)
			return c.Send("⚠️ 该账号已绑定。")
		}

		binding = model.Binding{
			UserID:       userID,
			Account:      account,
			CustomerCode: code,
			RoomName:     rooms[0].RoomName,
			RoomVerify:   rooms[0].RoomVerify,
			LastBalance:  rooms[0].Balance,
			LastCheck:    time.Now(),
		}
		bot.DB.Create(&binding)
		bot.setState(userID, StateNone)

		return c.Send(fmt.Sprintf("✅ *绑定成功\\!*\n🏠 房间: %s\n⚡ 当前余额: `%.2f`", escapeMarkdownV2(rooms[0].RoomName), rooms[0].Balance), telebot.ModeMarkdownV2)

	case StateSettings_WaitThreshold:
		val, err := strconv.ParseFloat(c.Text(), 64)
		if err != nil {
			return c.Send("❌ 输入无效，请输入数字。")
		}

		var user model.User
		bot.DB.First(&user, userID)
		user.NotifyThreshold = val
		bot.DB.Save(&user)
		bot.setState(userID, StateNone)

		c.Send("✅ 阈值已更新。")
		return bot.handleSettings(c)

	case StateSettings_WaitInterval:
		val, err := strconv.Atoi(c.Text())
		if err != nil || val < 1 {
			return c.Send("❌ 输入无效，请输入大于0的整数。")
		}

		var user model.User
		bot.DB.First(&user, userID)
		user.CheckInterval = val
		bot.DB.Save(&user)
		bot.setState(userID, StateNone)

		c.Send("✅ 检查间隔已更新。")
		return bot.handleSettings(c)

	case StateRecharge_WaitAmount:
		amt, err := strconv.ParseFloat(strings.TrimSpace(c.Text()), 64)
		if err != nil || amt <= 0 {
			return c.Send("❌ 输入无效，请输入大于 0 的金额数字（如 15 或 50）。")
		}

		bindingIDStr := bot.getTempData(userID, "recharge_binding_id")
		bindingID, _ := strconv.Atoi(bindingIDStr)
		var b model.Binding
		if err := bot.DB.First(&b, bindingID).Error; err != nil {
			bot.setState(userID, StateNone)
			return c.Send("❌ 未找到绑定账号。")
		}

		bot.setState(userID, StateNone)
		return bot.generateAndSendRechargeURL(c, b, amt)
	}

	return nil
}

// CheckLowBalance is called by cron scheduler
func (bot *Bot) CheckLowBalance() {
	var users []model.User
	bot.DB.Preload("Bindings").Find(&users)

	for _, user := range users {
		if !user.NotifyEnabled {
			continue
		}

		for _, b := range user.Bindings {
			if time.Since(b.LastCheck) < time.Duration(user.CheckInterval)*time.Minute {
				continue
			}

			rooms, err := bot.Client.GetBalance(b.Account, b.CustomerCode)
			if err != nil {
				log.Printf("Error checking balance for user %d: %v", user.ID, err)
				continue
			}

			for _, room := range rooms {
				if room.Balance < user.NotifyThreshold {
					msg := fmt.Sprintf("⚠️ <b>低电量预警</b>\n\n🏠 房间: <code>%s</code>\n⚡ 余额: <code>%.2f</code> 度\n📉 阈值: <code>%.2f</code> 度",
						html.EscapeString(room.RoomName), room.Balance, user.NotifyThreshold)

					if b.CustomerCode == wanxiao.SupportedSchoolCode {
						menu := &telebot.ReplyMarkup{}
						btnRecharge := telebot.Btn{
							Text:   "⚡ 立即充值",
							Unique: "pay_acc",
							Data:   strconv.Itoa(int(b.ID)),
						}
						menu.Inline(menu.Row(btnRecharge))
						bot.B.Send(&telebot.User{ID: user.ID}, msg, menu, telebot.ModeHTML)
					} else {
						bot.B.Send(&telebot.User{ID: user.ID}, msg, telebot.ModeHTML)
					}
				}

				// Update cache
				b.LastBalance = room.Balance
				b.RoomName = room.RoomName
				b.RoomVerify = room.RoomVerify
				b.LastCheck = time.Now()
				bot.DB.Save(&b)
			}
		}
	}
}
