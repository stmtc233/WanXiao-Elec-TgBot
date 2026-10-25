package bot

import (
	"log"
	"strings"

	"gopkg.in/telebot.v3"
)

const helpText = "<b>完美校园电费助手</b>\n\n" +
	"🔌 <b>查询电量</b>：查询所有绑定宿舍的剩余电量\n" +
	"⚡ <b>充值电费</b>：生成电费充值链接（目前仅支持珠海科技学院）\n" +
	"👤 <b>账号管理</b>：添加或解绑完美校园账号\n" +
	"⚙️ <b>预警设置</b>：设置低电量提醒的阈值和检查间隔\n\n" +
	"添加账号时可直接发送「学号 学校代码」或「学号 学校名称」，例如 <code>2023123456 1399</code>、<code>2023123456 郑州大学</code>；输入学校名称关键字即可搜索学校。\n" +
	"任何输入步骤中都可以发送 /cancel 取消。"

func (bot *Bot) handleStart(c telebot.Context) error {
	userID := c.Sender().ID
	log.Printf("[Start] user=%d", userID)
	bot.clearState(userID)
	if _, err := bot.getOrCreateUser(userID); err != nil {
		log.Printf("[Start] create user %d failed: %v", userID, err)
	}

	msg := "👋 欢迎使用完美校园电费监控与充值机器人！\n\n" + helpText
	if bindings, err := bot.userBindings(userID); err == nil && len(bindings) == 0 {
		msg += "\n\n👉 先到「👤 账号管理」中添加一个账号吧。"
	}
	return c.Send(msg, menuKeyboard, telebot.ModeHTML)
}

func (bot *Bot) handleHelp(c telebot.Context) error {
	return c.Send(helpText, menuKeyboard, telebot.ModeHTML)
}

func (bot *Bot) handleCancel(c telebot.Context) error {
	if bot.getState(c.Sender().ID) == StateNone {
		return c.Send("当前没有进行中的操作。", menuKeyboard)
	}
	bot.clearState(c.Sender().ID)
	return c.Send("已取消当前操作。", menuKeyboard)
}

func (bot *Bot) handleCancelCallback(c telebot.Context) error {
	respond(c, "已取消")
	bot.clearState(c.Sender().ID)
	return bot.edit(c.Callback(), "已取消。")
}

// Global Text Handler (State Machine)
func (bot *Bot) handleText(c telebot.Context) error {
	userID := c.Sender().ID
	text := strings.TrimSpace(c.Text())

	switch bot.getState(userID) {
	case StateAddAccount_WaitAccount:
		return bot.onAccountInput(c, text)
	case StateAddAccount_WaitCode:
		return bot.onSchoolCodeInput(c, text)
	case StateSettings_WaitThreshold:
		return bot.onThresholdInput(c, text)
	case StateSettings_WaitInterval:
		return bot.onIntervalInput(c, text)
	case StateRecharge_WaitAmount:
		return bot.onRechargeAmountInput(c, text)
	}

	if strings.HasPrefix(text, "/") {
		return c.Send("未知命令，发送 /help 查看可用功能。", menuKeyboard)
	}
	return c.Send("请使用下方菜单进行操作，或发送 /help 查看帮助。", menuKeyboard)
}
