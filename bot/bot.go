package bot

import (
	"log"
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

const (
	sessionTTL = 10 * time.Minute

	// Limits for user input
	MinThreshold   = 0.0
	MaxThreshold   = 1000.0
	MinInterval    = 10   // minutes
	MaxInterval    = 1440 // minutes
	MinRechargeAmt = 1.0
	MaxRechargeAmt = 500.0

	// Max concurrent requests to wanxiao per query/run
	queryConcurrency = 5
)

// session holds the input state of a user in a multi-step flow.
type session struct {
	state   int
	data    map[string]string
	expires time.Time
}

type Bot struct {
	B      *telebot.Bot
	DB     *gorm.DB
	Client *wanxiao.Client

	sessions  map[int64]*session
	sessionMu sync.Mutex

	// Users with a slow operation in flight (query / recharge), to ignore double clicks
	busy sync.Map
}

// Keyboards
var (
	// Main Menu (Reply Keyboard)
	menuBtnElec     = telebot.Btn{Text: "🔌 查询电量"}
	menuBtnRecharge = telebot.Btn{Text: "⚡ 充值电费"}
	menuBtnAccounts = telebot.Btn{Text: "👤 账号管理"}
	menuBtnSettings = telebot.Btn{Text: "⚙️ 预警设置"}
	menuKeyboard    = &telebot.ReplyMarkup{ResizeKeyboard: true}

	// Static inline buttons
	btnAddAccount   = telebot.Btn{Text: "➕ 添加账号", Unique: "add_acc"}
	btnSetThreshold = telebot.Btn{Text: "📉 修改阈值", Unique: "set_thres"}
	btnSetInterval  = telebot.Btn{Text: "⏱️ 修改间隔", Unique: "set_inter"}
	btnRefreshElec  = telebot.Btn{Text: "🔄 刷新", Unique: "refresh_elec"}
	btnBackSettings = telebot.Btn{Text: "↩️ 返回", Unique: "settings_back"}
	btnBackAccounts = telebot.Btn{Text: "↩️ 返回", Unique: "acc_back"}
	btnCancel       = telebot.Btn{Text: "✖️ 取消", Unique: "cancel"}

	// Callback endpoints carrying data
	btnToggleAlert       = telebot.Btn{Unique: "toggle_alert"}
	btnThresholdValue    = telebot.Btn{Unique: "thres_val"}
	btnIntervalValue     = telebot.Btn{Unique: "inter_val"}
	btnSchoolEndpoint    = telebot.Btn{Unique: "school"}
	btnPayAccEndpoint    = telebot.Btn{Unique: "pay_acc"} // sends a new amount menu (from query result / alerts)
	btnPaySelEndpoint    = telebot.Btn{Unique: "pay_sel"} // edits the room selection into the amount menu
	btnAmtEndpoint       = telebot.Btn{Unique: "amt"}
	btnAmtCustomEndpoint = telebot.Btn{Unique: "amt_custom"}
	btnUnbindEndpoint    = telebot.Btn{Unique: "unbind"}
	btnUnbindOkEndpoint  = telebot.Btn{Unique: "unbind_ok"}
)

var botCommands = []telebot.Command{
	{Text: "elec", Description: "查询电量"},
	{Text: "recharge", Description: "充值电费"},
	{Text: "accounts", Description: "账号管理"},
	{Text: "settings", Description: "预警设置"},
	{Text: "cancel", Description: "取消当前操作"},
	{Text: "help", Description: "使用帮助"},
}

func NewBot(token string, db *gorm.DB) (*Bot, error) {
	pref := telebot.Settings{
		Token:  token,
		Poller: &telebot.LongPoller{Timeout: 30 * time.Second},
		OnError: func(err error, c telebot.Context) {
			if c != nil && c.Sender() != nil {
				log.Printf("[Bot Error] user=%d: %v", c.Sender().ID, err)
				return
			}
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
		sessions: make(map[int64]*session),
	}

	menuKeyboard.Reply(
		menuKeyboard.Row(menuBtnElec, menuBtnRecharge),
		menuKeyboard.Row(menuBtnAccounts, menuBtnSettings),
	)

	if err := b.SetCommands(botCommands); err != nil {
		log.Printf("[Bot] set commands failed: %v", err)
	}

	bot.registerHandlers()
	return bot, nil
}

func (bot *Bot) Start() {
	bot.B.Start()
}

func (bot *Bot) Stop() {
	bot.B.Stop()
}

func (bot *Bot) registerHandlers() {
	// Commands
	bot.B.Handle("/start", bot.handleStart)
	bot.B.Handle("/help", bot.handleHelp)
	bot.B.Handle("/cancel", bot.handleCancel)
	bot.B.Handle("/elec", bot.handleElec)
	bot.B.Handle("/recharge", bot.handleRecharge)
	bot.B.Handle("/pay", bot.handleRecharge)
	bot.B.Handle("/accounts", bot.handleAccounts)
	bot.B.Handle("/settings", bot.handleSettings)

	// Menu Buttons
	bot.B.Handle(&menuBtnElec, bot.handleElec)
	bot.B.Handle(&menuBtnRecharge, bot.handleRecharge)
	bot.B.Handle(&menuBtnAccounts, bot.handleAccounts)
	bot.B.Handle(&menuBtnSettings, bot.handleSettings)

	// Inline buttons
	for btn, h := range bot.callbackHandlers() {
		bot.B.Handle(&telebot.Btn{Unique: btn}, h)
	}

	// Generic Text Handler (for inputs)
	bot.B.Handle(telebot.OnText, bot.handleText)

	// Fallback Callback Handler
	bot.B.Handle(telebot.OnCallback, bot.handleCallbackFallback)
}

func (bot *Bot) callbackHandlers() map[string]telebot.HandlerFunc {
	return map[string]telebot.HandlerFunc{
		btnAddAccount.Unique:        bot.handleAddAccountBtn,
		btnBackAccounts.Unique:      bot.handleAccounts,
		btnSchoolEndpoint.Unique:    bot.handleSchoolCallback,
		btnUnbindEndpoint.Unique:    bot.handleUnbindCallback,
		btnUnbindOkEndpoint.Unique:  bot.handleUnbindConfirmCallback,
		btnToggleAlert.Unique:       bot.handleToggleAlert,
		btnSetThreshold.Unique:      bot.handleSetThresholdBtn,
		btnSetInterval.Unique:       bot.handleSetIntervalBtn,
		btnThresholdValue.Unique:    bot.handleThresholdValue,
		btnIntervalValue.Unique:     bot.handleIntervalValue,
		btnBackSettings.Unique:      bot.handleSettings,
		btnRefreshElec.Unique:       bot.handleRefreshElec,
		btnPayAccEndpoint.Unique:    bot.handlePayAccCallback,
		btnPaySelEndpoint.Unique:    bot.handlePaySelCallback,
		btnAmtEndpoint.Unique:       bot.handleAmtCallback,
		btnAmtCustomEndpoint.Unique: bot.handleAmtCustomCallback,
		btnCancel.Unique:            bot.handleCancelCallback,
	}
}

// Fallback Callback Handler (in case any callback bypasses direct endpoint handlers)
func (bot *Bot) handleCallbackFallback(c telebot.Context) error {
	cb := c.Callback()
	if cb == nil {
		return nil
	}

	unique := strings.TrimSpace(cb.Unique)
	data := cb.Data

	// If telebot didn't parse \funique|payload (note: TrimSpace would strip the \f marker):
	if unique == "" && strings.HasPrefix(data, "\f") {
		parts := strings.SplitN(strings.TrimPrefix(data, "\f"), "|", 2)
		unique = strings.TrimSpace(parts[0])
		data = ""
		if len(parts) > 1 {
			data = parts[1]
		}
	}
	data = strings.TrimSpace(data)

	log.Printf("[Callback Fallback] user=%d unique=%s data=%s", c.Sender().ID, unique, data)

	if h, ok := bot.callbackHandlers()[unique]; ok {
		cb.Unique, cb.Data = unique, data
		return h(c)
	}
	return c.Respond(&telebot.CallbackResponse{Text: "该按钮已失效"})
}

// --- Session management ---

func (bot *Bot) setState(userID int64, state int) {
	bot.sessionMu.Lock()
	defer bot.sessionMu.Unlock()
	if state == StateNone {
		delete(bot.sessions, userID)
		return
	}
	s := bot.sessions[userID]
	if s == nil || time.Now().After(s.expires) {
		s = &session{data: make(map[string]string)}
		bot.sessions[userID] = s
	}
	s.state = state
	s.expires = time.Now().Add(sessionTTL)
}

func (bot *Bot) clearState(userID int64) {
	bot.setState(userID, StateNone)
}

// liveSession returns the user's session if not expired. Caller must hold sessionMu.
func (bot *Bot) liveSession(userID int64) *session {
	s := bot.sessions[userID]
	if s != nil && time.Now().After(s.expires) {
		delete(bot.sessions, userID)
		return nil
	}
	return s
}

func (bot *Bot) getState(userID int64) int {
	bot.sessionMu.Lock()
	defer bot.sessionMu.Unlock()
	if s := bot.liveSession(userID); s != nil {
		return s.state
	}
	return StateNone
}

func (bot *Bot) setTempData(userID int64, key, value string) {
	bot.sessionMu.Lock()
	defer bot.sessionMu.Unlock()
	if s := bot.liveSession(userID); s != nil {
		s.data[key] = value
	}
}

func (bot *Bot) getTempData(userID int64, key string) string {
	bot.sessionMu.Lock()
	defer bot.sessionMu.Unlock()
	if s := bot.liveSession(userID); s != nil {
		return s.data[key]
	}
	return ""
}

// cleanupSessions drops expired sessions; called periodically by the scheduler.
func (bot *Bot) cleanupSessions() {
	bot.sessionMu.Lock()
	defer bot.sessionMu.Unlock()
	now := time.Now()
	for id, s := range bot.sessions {
		if now.After(s.expires) {
			delete(bot.sessions, id)
		}
	}
}

// tryLock marks a user as busy. It returns false if an operation is already running.
func (bot *Bot) tryLock(userID int64) bool {
	_, loaded := bot.busy.LoadOrStore(userID, struct{}{})
	return !loaded
}

func (bot *Bot) unlock(userID int64) {
	bot.busy.Delete(userID)
}

// --- DB helpers ---

func (bot *Bot) getOrCreateUser(userID int64) (*model.User, error) {
	user := model.User{ID: userID}
	if err := bot.DB.FirstOrCreate(&user, model.User{ID: userID}).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (bot *Bot) userBindings(userID int64) ([]model.Binding, error) {
	var bindings []model.Binding
	err := bot.DB.Where("user_id = ?", userID).Order("id").Find(&bindings).Error
	return bindings, err
}

// findBinding loads a binding by ID, ensuring it belongs to the user.
func (bot *Bot) findBinding(userID int64, idStr string) (*model.Binding, error) {
	var b model.Binding
	err := bot.DB.Where("id = ? AND user_id = ?", strings.TrimSpace(idStr), userID).First(&b).Error
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// saveBalance updates the cached room info of a binding after a successful query.
func (bot *Bot) saveBalance(b *model.Binding, room wanxiao.RoomInfo, extra map[string]interface{}) {
	now := time.Now()
	updates := map[string]interface{}{
		"room_name":    room.RoomName,
		"room_verify":  room.RoomVerify,
		"last_balance": room.Balance,
		"last_check":   now,
	}
	for k, v := range extra {
		updates[k] = v
	}
	if err := bot.DB.Model(&model.Binding{}).Where("id = ?", b.ID).Updates(updates).Error; err != nil {
		log.Printf("[DB] update binding %d failed: %v", b.ID, err)
		return
	}
	b.RoomName, b.RoomVerify, b.LastBalance, b.LastCheck = room.RoomName, room.RoomVerify, room.Balance, now
}
