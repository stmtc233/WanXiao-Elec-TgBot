package bot

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"

	"wanxiao-elec-bot/model"

	"gopkg.in/telebot.v3"
)

var (
	presetThresholds = []string{"5", "10", "20", "30"}
	presetIntervals  = []string{"30", "60", "120", "360"}
)

// ⚙️ Settings
func (bot *Bot) handleSettings(c telebot.Context) error {
	respond(c)
	bot.clearState(c.Sender().ID)
	return bot.renderSettings(c)
}

func (bot *Bot) renderSettings(c telebot.Context) error {
	user, err := bot.getOrCreateUser(c.Sender().ID)
	if err != nil {
		log.Printf("[Settings] load user %d failed: %v", c.Sender().ID, err)
		return c.Send("❌ 读取设置失败，请稍后再试。")
	}

	status, toggleText := "❌ 已关闭", "🔔 开启预警"
	if user.NotifyEnabled {
		status, toggleText = "✅ 已开启", "🔕 关闭预警"
	}

	msg := fmt.Sprintf("⚙️ <b>预警设置</b>\n\n"+
		"🔔 预警状态：%s\n"+
		"📉 报警阈值：<code>%s</code> 度\n"+
		"⏱️ 检查间隔：<code>%d</code> 分钟\n\n"+
		"<i>余额低于阈值时推送一次提醒；若持续偏低，每 24 小时再提醒一次。</i>",
		status, formatNum(user.NotifyThreshold), effectiveInterval(user.CheckInterval))

	markup := inlineMarkup(
		telebot.Row{{Text: toggleText, Unique: btnToggleAlert.Unique}},
		telebot.Row{btnSetThreshold, btnSetInterval},
	)
	return editOrSend(c, msg, markup, telebot.ModeHTML)
}

// effectiveInterval clamps legacy/invalid stored intervals into the allowed range.
func effectiveInterval(minutes int) int {
	if minutes < MinInterval {
		return MinInterval
	}
	if minutes > MaxInterval {
		return MaxInterval
	}
	return minutes
}

func (bot *Bot) handleToggleAlert(c telebot.Context) error {
	userID := c.Sender().ID
	user, err := bot.getOrCreateUser(userID)
	if err != nil {
		respond(c, "设置失败，请稍后再试")
		return nil
	}

	enabled := !user.NotifyEnabled
	if err := bot.DB.Model(&model.User{}).Where("id = ?", userID).Update("notify_enabled", enabled).Error; err != nil {
		log.Printf("[Settings] toggle alert for user %d failed: %v", userID, err)
		respond(c, "设置失败，请稍后再试")
		return nil
	}
	if enabled {
		bot.resetAlerts(userID)
		respond(c, "预警已开启")
	} else {
		respond(c, "预警已关闭")
	}
	return bot.renderSettings(c)
}

// resetAlerts makes the monitor re-evaluate alerts for all of a user's bindings.
func (bot *Bot) resetAlerts(userID int64) {
	if err := bot.DB.Model(&model.Binding{}).Where("user_id = ?", userID).Update("alerted_at", nil).Error; err != nil {
		log.Printf("[Settings] reset alerts for user %d failed: %v", userID, err)
	}
}

func presetRow(unique string, values []string, unit string) telebot.Row {
	row := make(telebot.Row, 0, len(values))
	for _, v := range values {
		row = append(row, telebot.Btn{Text: v + unit, Unique: unique, Data: v})
	}
	return row
}

func (bot *Bot) handleSetThresholdBtn(c telebot.Context) error {
	respond(c)
	bot.setState(c.Sender().ID, StateSettings_WaitThreshold)
	msg := fmt.Sprintf("📉 请选择或输入新的 <b>报警阈值</b>（度，%s–%s）：\n发送 /cancel 取消。",
		formatNum(MinThreshold), formatNum(MaxThreshold))
	markup := inlineMarkup(presetRow(btnThresholdValue.Unique, presetThresholds, " 度"), telebot.Row{btnBackSettings})
	return editOrSend(c, msg, markup, telebot.ModeHTML)
}

func (bot *Bot) handleSetIntervalBtn(c telebot.Context) error {
	respond(c)
	bot.setState(c.Sender().ID, StateSettings_WaitInterval)
	msg := fmt.Sprintf("⏱️ 请选择或输入新的 <b>检查间隔</b>（分钟，%d–%d）：\n发送 /cancel 取消。", MinInterval, MaxInterval)
	markup := inlineMarkup(presetRow(btnIntervalValue.Unique, presetIntervals, " 分钟"), telebot.Row{btnBackSettings})
	return editOrSend(c, msg, markup, telebot.ModeHTML)
}

func parseThreshold(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "度")), 64)
	if err != nil || math.IsNaN(v) || v <= MinThreshold || v > MaxThreshold {
		return 0, false
	}
	return math.Round(v*100) / 100, true
}

func parseInterval(s string) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "分钟")))
	if err != nil || v < MinInterval || v > MaxInterval {
		return 0, false
	}
	return v, true
}

func (bot *Bot) updateThreshold(userID int64, v float64) error {
	if _, err := bot.getOrCreateUser(userID); err != nil {
		return err
	}
	if err := bot.DB.Model(&model.User{}).Where("id = ?", userID).Update("notify_threshold", v).Error; err != nil {
		return err
	}
	bot.resetAlerts(userID)
	return nil
}

func (bot *Bot) updateInterval(userID int64, v int) error {
	if _, err := bot.getOrCreateUser(userID); err != nil {
		return err
	}
	return bot.DB.Model(&model.User{}).Where("id = ?", userID).Update("check_interval", v).Error
}

func (bot *Bot) handleThresholdValue(c telebot.Context) error {
	v, ok := parseThreshold(c.Callback().Data)
	if !ok {
		respond(c, "无效的阈值")
		return nil
	}
	if err := bot.updateThreshold(c.Sender().ID, v); err != nil {
		log.Printf("[Settings] update threshold failed: %v", err)
		respond(c, "设置失败，请稍后再试")
		return nil
	}
	bot.clearState(c.Sender().ID)
	respond(c, "阈值已更新")
	return bot.renderSettings(c)
}

func (bot *Bot) handleIntervalValue(c telebot.Context) error {
	v, ok := parseInterval(c.Callback().Data)
	if !ok {
		respond(c, "无效的间隔")
		return nil
	}
	if err := bot.updateInterval(c.Sender().ID, v); err != nil {
		log.Printf("[Settings] update interval failed: %v", err)
		respond(c, "设置失败，请稍后再试")
		return nil
	}
	bot.clearState(c.Sender().ID)
	respond(c, "检查间隔已更新")
	return bot.renderSettings(c)
}

func (bot *Bot) onThresholdInput(c telebot.Context, text string) error {
	v, ok := parseThreshold(text)
	if !ok {
		return c.Send(fmt.Sprintf("❌ 输入无效，请输入 %s–%s 之间的数字（不含 0），或发送 /cancel 取消。",
			formatNum(MinThreshold), formatNum(MaxThreshold)))
	}
	if err := bot.updateThreshold(c.Sender().ID, v); err != nil {
		log.Printf("[Settings] update threshold failed: %v", err)
		return c.Send("❌ 设置失败，请稍后再试。")
	}
	bot.clearState(c.Sender().ID)
	_ = c.Send(fmt.Sprintf("✅ 报警阈值已更新为 %s 度。", formatNum(v)))
	return bot.renderSettings(c)
}

func (bot *Bot) onIntervalInput(c telebot.Context, text string) error {
	v, ok := parseInterval(text)
	if !ok {
		return c.Send(fmt.Sprintf("❌ 输入无效，请输入 %d–%d 之间的整数，或发送 /cancel 取消。", MinInterval, MaxInterval))
	}
	if err := bot.updateInterval(c.Sender().ID, v); err != nil {
		log.Printf("[Settings] update interval failed: %v", err)
		return c.Send("❌ 设置失败，请稍后再试。")
	}
	bot.clearState(c.Sender().ID)
	_ = c.Send(fmt.Sprintf("✅ 检查间隔已更新为 %d 分钟。", v))
	return bot.renderSettings(c)
}
