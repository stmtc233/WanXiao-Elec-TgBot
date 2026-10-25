package bot

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"wanxiao-elec-bot/model"
	"wanxiao-elec-bot/wanxiao"

	"gopkg.in/telebot.v3"
	"gorm.io/gorm"
)

var (
	accountPattern = regexp.MustCompile(`^[0-9A-Za-z_-]{1,32}$`)
	codePattern    = regexp.MustCompile(`^[0-9A-Za-z]{1,16}$`)
)

// 👤 Account Management
func (bot *Bot) handleAccounts(c telebot.Context) error {
	respond(c)
	bot.clearState(c.Sender().ID)
	return bot.renderAccounts(c)
}

func (bot *Bot) renderAccounts(c telebot.Context) error {
	bindings, err := bot.userBindings(c.Sender().ID)
	if err != nil {
		return c.Send("❌ 读取账号信息失败，请稍后再试。")
	}

	var sb strings.Builder
	sb.WriteString("📋 <b>账号管理</b>\n\n")
	if len(bindings) == 0 {
		sb.WriteString("暂无绑定账号，点击下方按钮添加。")
	}

	rows := []telebot.Row{{btnAddAccount}}
	for i := range bindings {
		b := &bindings[i]
		fmt.Fprintf(&sb, "%d. 🏠 %s\n    👤 <code>%s</code> · 🏫 <code>%s</code>\n",
			i+1, esc(roomDisplay(b)), esc(b.Account), esc(b.CustomerCode))
		rows = append(rows, telebot.Row{{
			Text:   fmt.Sprintf("🗑 解绑 %s", roomDisplay(b)),
			Unique: btnUnbindEndpoint.Unique,
			Data:   strconv.Itoa(int(b.ID)),
		}})
	}

	return editOrSend(c, sb.String(), inlineMarkup(rows...), telebot.ModeHTML)
}

func (bot *Bot) handleUnbindCallback(c telebot.Context) error {
	b, err := bot.findBinding(c.Sender().ID, c.Callback().Data)
	if err != nil {
		respond(c, "该账号不存在或已解绑")
		return bot.renderAccounts(c)
	}
	respond(c)

	msg := fmt.Sprintf("⚠️ 确定要解绑 <b>%s</b>（<code>%s</code>）吗？\n解绑后将不再查询和预警该房间。",
		esc(roomDisplay(b)), esc(b.Account))
	markup := inlineMarkup(telebot.Row{
		{Text: "✅ 确认解绑", Unique: btnUnbindOkEndpoint.Unique, Data: strconv.Itoa(int(b.ID))},
		btnBackAccounts,
	})
	return editOrSend(c, msg, markup, telebot.ModeHTML)
}

func (bot *Bot) handleUnbindConfirmCallback(c telebot.Context) error {
	userID := c.Sender().ID
	res := bot.DB.Where("id = ? AND user_id = ?", strings.TrimSpace(c.Callback().Data), userID).Delete(&model.Binding{})
	switch {
	case res.Error != nil:
		log.Printf("[Accounts] unbind failed for user=%d: %v", userID, res.Error)
		respond(c, "解绑失败，请稍后再试")
	case res.RowsAffected == 0:
		respond(c, "该账号不存在或已解绑")
	default:
		log.Printf("[Accounts] user=%d unbound binding=%s", userID, c.Callback().Data)
		respond(c, "已解绑")
	}
	return bot.renderAccounts(c)
}

func (bot *Bot) handleAddAccountBtn(c telebot.Context) error {
	respond(c)
	bot.setState(c.Sender().ID, StateAddAccount_WaitAccount)
	msg := "➕ <b>添加账号</b>\n\n" +
		"请输入 <b>学号或卡号</b>。\n" +
		"也可以一次发送「学号 学校代码」，例如 <code>2023123456 1399</code>。\n\n" +
		"发送 /cancel 取消。"
	return editOrSend(c, msg, inlineMarkup(telebot.Row{btnCancel}), telebot.ModeHTML)
}

func (bot *Bot) onAccountInput(c telebot.Context, text string) error {
	fields := strings.Fields(text)
	switch {
	case len(fields) == 2:
		return bot.bindAccount(c, fields[0], fields[1])
	case len(fields) != 1 || !accountPattern.MatchString(fields[0]):
		return c.Send("❌ 账号格式不正确，请输入学号/卡号（仅数字或字母），或发送 /cancel 取消。")
	}

	userID := c.Sender().ID
	account := fields[0]
	bot.setState(userID, StateAddAccount_WaitCode)
	bot.setTempData(userID, "account", account)

	var rows []telebot.Row
	for _, s := range knownSchools {
		rows = append(rows, telebot.Row{{
			Text:   fmt.Sprintf("🏫 %s（%s）", s.Name, s.Code),
			Unique: btnSchoolEndpoint.Unique,
			Data:   s.Code,
		}})
	}
	rows = append(rows, telebot.Row{btnCancel})

	msg := fmt.Sprintf("收到账号 <code>%s</code>。\n请选择学校，或直接输入 <b>学校代码</b>（Customer Code）：", esc(account))
	return c.Send(msg, inlineMarkup(rows...), telebot.ModeHTML)
}

func (bot *Bot) onSchoolCodeInput(c telebot.Context, text string) error {
	account := bot.getTempData(c.Sender().ID, "account")
	if account == "" {
		bot.clearState(c.Sender().ID)
		return c.Send("操作已过期，请重新添加账号。")
	}
	return bot.bindAccount(c, account, text)
}

func (bot *Bot) handleSchoolCallback(c telebot.Context) error {
	userID := c.Sender().ID
	account := bot.getTempData(userID, "account")
	if bot.getState(userID) != StateAddAccount_WaitCode || account == "" {
		respond(c, "操作已过期，请重新添加账号")
		return nil
	}
	respond(c)
	code := strings.TrimSpace(c.Callback().Data)
	_ = bot.edit(c.Callback(), fmt.Sprintf("账号 <code>%s</code> · 学校代码 <code>%s</code>", esc(account), esc(code)), telebot.ModeHTML)
	return bot.bindAccount(c, account, code)
}

func (bot *Bot) bindAccount(c telebot.Context, account, code string) error {
	userID := c.Sender().ID
	if !accountPattern.MatchString(account) {
		return c.Send("❌ 账号格式不正确，请输入学号/卡号（仅数字或字母），或发送 /cancel 取消。")
	}
	if !codePattern.MatchString(code) {
		return c.Send("❌ 学校代码格式不正确，请重新输入，或发送 /cancel 取消。")
	}
	bot.clearState(userID)

	user, err := bot.getOrCreateUser(userID)
	if err != nil {
		log.Printf("[Accounts] create user %d failed: %v", userID, err)
		return c.Send("❌ 绑定失败，请稍后再试。")
	}

	var count int64
	bot.DB.Model(&model.Binding{}).Where("user_id = ? AND account = ? AND customer_code = ?", userID, account, code).Count(&count)
	if count > 0 {
		return c.Send("⚠️ 该账号已绑定，无需重复添加。")
	}

	if !bot.tryLock(userID) {
		return c.Send("⏳ 正在处理中，请稍候…")
	}
	defer bot.unlock(userID)

	waitMsg, err := bot.B.Send(c.Recipient(), "🔍 正在验证账号，请稍候…")
	if err != nil {
		return err
	}

	rooms, err := bot.Client.GetBalance(account, code)
	if err == nil && len(rooms) == 0 {
		err = &wanxiao.APIError{Msg: "未找到该账号的房间信息"}
	}
	if err != nil {
		log.Printf("[Accounts] verify failed for user=%d account=%s code=%s: %v", userID, account, code, err)
		msg := fmt.Sprintf("❌ 验证失败：%s\n请检查学号和学校代码后重试。", esc(friendlyErr(err)))
		return bot.edit(waitMsg, msg, inlineMarkup(telebot.Row{{Text: "🔁 重新添加", Unique: btnAddAccount.Unique}}), telebot.ModeHTML)
	}

	room := rooms[0]
	now := time.Now()
	binding := model.Binding{
		UserID:       userID,
		Account:      account,
		CustomerCode: code,
		RoomName:     room.RoomName,
		RoomVerify:   room.RoomVerify,
		LastBalance:  room.Balance,
		LastCheck:    now,
		LastAttempt:  now,
	}
	if err := bot.DB.Create(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return bot.edit(waitMsg, "⚠️ 该账号已绑定，无需重复添加。")
		}
		log.Printf("[Accounts] create binding failed for user=%d: %v", userID, err)
		return bot.edit(waitMsg, "❌ 绑定失败，请稍后再试。")
	}
	log.Printf("[Accounts] user=%d bound account=%s code=%s", userID, account, code)

	alert := "🔕 低电量预警未开启，可在「⚙️ 预警设置」中开启。"
	if user.NotifyEnabled {
		alert = fmt.Sprintf("🔔 余额低于 %s 度时会自动提醒你。", formatNum(user.NotifyThreshold))
	}
	msg := fmt.Sprintf("✅ <b>绑定成功！</b>\n\n🏠 房间：%s\n⚡ 当前余额：<code>%.2f</code> 度\n\n%s",
		esc(room.RoomName), room.Balance, alert)

	var markup *telebot.ReplyMarkup
	if code == wanxiao.SupportedSchoolCode {
		markup = inlineMarkup(telebot.Row{{
			Text:   "⚡ 立即充值",
			Unique: btnPayAccEndpoint.Unique,
			Data:   strconv.Itoa(int(binding.ID)),
		}})
	}
	if markup != nil {
		return bot.edit(waitMsg, msg, markup, telebot.ModeHTML)
	}
	return bot.edit(waitMsg, msg, telebot.ModeHTML)
}
