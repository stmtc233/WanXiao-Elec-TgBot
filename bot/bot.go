package bot

import (
	"fmt"
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
	// Main Menu
	menuBtnElec     = telebot.Btn{Text: "🔌 查询电量"}
	menuBtnAccounts = telebot.Btn{Text: "👤 账号管理"}
	menuBtnSettings = telebot.Btn{Text: "⚙️ 预警设置"}
	menuKeyboard    = &telebot.ReplyMarkup{ResizeKeyboard: true}

	// Inline Buttons
	btnAddAccount   = telebot.Btn{Text: "➕ 添加账号", Unique: "add_acc"}
	btnToggleAlert  = telebot.Btn{Text: "🔔 开关预警", Unique: "toggle_alert"}
	btnSetThreshold = telebot.Btn{Text: "📉 修改阈值", Unique: "set_thres"}
	btnSetInterval  = telebot.Btn{Text: "⏱️ 修改间隔", Unique: "set_inter"}
)

func NewBot(token string, db *gorm.DB) (*Bot, error) {
	pref := telebot.Settings{
		Token:  token,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
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
		menuKeyboard.Row(menuBtnElec),
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

	// Menu Buttons
	bot.B.Handle(&menuBtnElec, bot.handleElec)
	bot.B.Handle(&menuBtnAccounts, bot.handleAccounts)
	bot.B.Handle(&menuBtnSettings, bot.handleSettings)

	// Inline Buttons
	bot.B.Handle(&btnAddAccount, bot.handleAddAccountBtn)
	bot.B.Handle(&btnToggleAlert, bot.handleToggleAlert)
	bot.B.Handle(&btnSetThreshold, bot.handleSetThresholdBtn)
	bot.B.Handle(&btnSetInterval, bot.handleSetIntervalBtn)

	// Generic Text Handler (for inputs)
	bot.B.Handle(telebot.OnText, bot.handleText)

	// Callback for Unbind (dynamic unique)
	bot.B.Handle(telebot.OnCallback, bot.handleCallback)
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
	user := model.User{ID: c.Sender().ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	bot.DB.FirstOrCreate(&user, model.User{ID: c.Sender().ID})
	bot.setState(c.Sender().ID, StateNone)
	return c.Send("欢迎使用完美校园电费监控机器人！\n请通过下方菜单进行操作。", menuKeyboard)
}

// 🔌 Query Electricity
func (bot *Bot) handleElec(c telebot.Context) error {
	bot.setState(c.Sender().ID, StateNone)
	var bindings []model.Binding
	bot.DB.Where("user_id = ?", c.Sender().ID).Find(&bindings)

	if len(bindings) == 0 {
		return c.Send("未绑定账号。请先到“👤 账号管理”中添加账号。")
	}

	msg, _ := bot.B.Send(c.Recipient(), "正在查询中，请稍候...")

	statusMsg := "📊 *电量状态*:\n\n"
	for _, b := range bindings {
		rooms, err := bot.Client.GetBalance(b.Account, b.CustomerCode)
		if err != nil {
			statusMsg += fmt.Sprintf("❌ 账号 `%s`: 查询失败 \\(%s\\)\n", escapeMarkdownV2Code(b.Account), escapeMarkdownV2(err.Error()))
			continue
		}

		for _, room := range rooms {
			statusMsg += fmt.Sprintf("🏠 *%s*\n⚡ 余额: `%.2f` 度\n\n", escapeMarkdownV2(room.RoomName), room.Balance)

			// Update cache
			b.LastBalance = room.Balance
			b.RoomName = room.RoomName
			b.LastCheck = time.Now()
			bot.DB.Save(&b)
		}
	}

	// Delete "checking" message and send result
	if msg != nil {
		bot.B.Delete(msg)
	}
	return c.Send(statusMsg, telebot.ModeMarkdownV2)
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
		// Add delete button for each account
		// Unique payload: unbind_<account>
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
	var user model.User
	if err := bot.DB.First(&user, c.Sender().ID).Error; err != nil {
		return c.Respond()
	}

	user.NotifyEnabled = !user.NotifyEnabled
	bot.DB.Save(&user)

	// Refresh info
	bot.handleSettings(c)
	return c.Respond(&telebot.CallbackResponse{Text: "设置已更新"})
}

func (bot *Bot) handleSetThresholdBtn(c telebot.Context) error {
	bot.setState(c.Sender().ID, StateSettings_WaitThreshold)
	return c.Send("请输入新的 *报警阈值* \\(例如 10\\):", telebot.ModeMarkdownV2)
}

func (bot *Bot) handleSetIntervalBtn(c telebot.Context) error {
	bot.setState(c.Sender().ID, StateSettings_WaitInterval)
	return c.Send("请输入新的 *检查间隔* \\(分钟，例如 60\\):", telebot.ModeMarkdownV2)
}

// Global Text Handler (State Machine)
func (bot *Bot) handleText(c telebot.Context) error {
	userID := c.Sender().ID
	state := bot.getState(userID)

	// Ignore if clicking menu buttons (they are handled by specific handlers)
	// But telebot might route them here if using OnText.
	// We checked specific button handlers first in registerHandlers.

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
	}

	return nil
}

// Callback handler for dynamic buttons like unbind
func (bot *Bot) handleCallback(c telebot.Context) error {
	data := strings.TrimSpace(c.Callback().Data)
	unique := strings.TrimSpace(c.Callback().Unique) // Telebot splits unique|data

	if unique == "unbind" {
		account := data
		result := bot.DB.Where("user_id = ? AND account = ?", c.Sender().ID, account).Delete(&model.Binding{})
		if result.RowsAffected == 0 {
			return c.Respond(&telebot.CallbackResponse{Text: "未找到绑定"})
		}
		c.Respond(&telebot.CallbackResponse{Text: "解绑成功"})
		// Refresh list
		return bot.handleAccounts(c)
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
			// Check if we should check based on interval
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
					// Alert!
					msg := fmt.Sprintf("⚠️ *低电量预警\\!*\n\n🏠 房间: %s\n⚡ 余额: `%.2f` 度\n📉 阈值: `%.2f` 度",
						escapeMarkdownV2(room.RoomName), room.Balance, user.NotifyThreshold)
					bot.B.Send(&telebot.User{ID: user.ID}, msg, telebot.ModeMarkdownV2)
				}

				// Update cache
				b.LastBalance = room.Balance
				b.RoomName = room.RoomName
				b.LastCheck = time.Now()
				bot.DB.Save(&b)
			}
		}
	}
}
