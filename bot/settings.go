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
		"⏱️ 自动检查：全局每 <code>%d</code> 分钟（不可修改）\n\n"+
		"<i>关闭预警后仍会每 30 分钟采集历史。余额低于阈值时提醒；持续偏低每 24 小时再提醒一次。</i>",
		status, formatNum(user.NotifyThreshold), int(model.CheckInterval.Minutes()))

	markup := inlineMarkup(
		telebot.Row{{Text: toggleText, Unique: btnToggleAlert.Unique}},
		telebot.Row{btnSetThreshold},
	)
	return editOrSend(c, msg, markup, telebot.ModeHTML)
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

func parseThreshold(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "度")), 64)
	if err != nil || math.IsNaN(v) || v <= MinThreshold || v > MaxThreshold {
		return 0, false
	}
	return math.Round(v*100) / 100, true
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
