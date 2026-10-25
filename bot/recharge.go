package bot

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"

	"wanxiao-elec-bot/model"
	"wanxiao-elec-bot/wanxiao"

	"gopkg.in/telebot.v3"
)

var presetAmounts = []int{10, 20, 30, 50, 100}

var unsupportedSchoolMsg = fmt.Sprintf("⚠️ 当前电费充值功能仅支持学校代码为 %s 的院校（珠海科技学院）。", wanxiao.SupportedSchoolCode)

// parseAmount validates a recharge amount in yuan (at most two decimals).
func parseAmount(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "元"))
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || v < MinRechargeAmt || v > MaxRechargeAmt {
		return 0, false
	}
	if math.Abs(v*100-math.Round(v*100)) > 1e-6 {
		return 0, false
	}
	return v, true
}

// ⚡ Recharge Electricity
func (bot *Bot) handleRecharge(c telebot.Context) error {
	userID := c.Sender().ID
	log.Printf("[Recharge] Menu requested by user=%d", userID)
	bot.clearState(userID)

	bindings, err := bot.userBindings(userID)
	if err != nil {
		return c.Send("❌ 读取账号信息失败，请稍后再试。")
	}
	if len(bindings) == 0 {
		return noBindingsReply(c)
	}

	var supported []model.Binding
	for _, b := range bindings {
		if b.CustomerCode == wanxiao.SupportedSchoolCode {
			supported = append(supported, b)
		}
	}

	switch len(supported) {
	case 0:
		return c.Send(unsupportedSchoolMsg + "\n你当前绑定的账号暂不支持在线充值。")
	case 1:
		return bot.showAmountSelection(c, &supported[0], false)
	}

	var rows []telebot.Row
	for i := range supported {
		b := &supported[i]
		rows = append(rows, telebot.Row{{
			Text:   fmt.Sprintf("🏠 %s", roomDisplay(b)),
			Unique: btnPaySelEndpoint.Unique,
			Data:   strconv.Itoa(int(b.ID)),
		}})
	}
	rows = append(rows, telebot.Row{btnCancel})
	return c.Send("⚡ <b>请选择要充值的房间</b>", inlineMarkup(rows...), telebot.ModeHTML)
}

// pay_acc: from query results / alerts, keep that message and send a new amount menu.
func (bot *Bot) handlePayAccCallback(c telebot.Context) error {
	return bot.payBindingCallback(c, false)
}

// pay_sel: from the room selection list, replace it with the amount menu.
func (bot *Bot) handlePaySelCallback(c telebot.Context) error {
	return bot.payBindingCallback(c, true)
}

func (bot *Bot) payBindingCallback(c telebot.Context, edit bool) error {
	respond(c)
	b, err := bot.findBinding(c.Sender().ID, c.Callback().Data)
	if err != nil {
		return c.Send("❌ 未找到该账号，可能已被解绑。")
	}
	if b.CustomerCode != wanxiao.SupportedSchoolCode {
		return c.Send(unsupportedSchoolMsg)
	}
	bot.clearState(c.Sender().ID)
	return bot.showAmountSelection(c, b, edit)
}

func (bot *Bot) showAmountSelection(c telebot.Context, b *model.Binding, edit bool) error {
	balance := "未知"
	if !b.LastCheck.IsZero() {
		balance = fmt.Sprintf("<code>%.2f</code> 度（%s）", b.LastBalance, formatAgo(b.LastCheck))
	}
	msg := fmt.Sprintf("⚡ <b>充值电费</b>\n\n"+
		"🏠 房间：<code>%s</code>\n"+
		"👤 账号：<code>%s</code>\n"+
		"🔋 当前余额：%s\n\n"+
		"请选择充值金额：",
		esc(roomDisplay(b)), esc(b.Account), balance)

	var amountBtns []telebot.Btn
	for _, amt := range presetAmounts {
		amountBtns = append(amountBtns, telebot.Btn{
			Text:   fmt.Sprintf("%d 元", amt),
			Unique: btnAmtEndpoint.Unique,
			Data:   fmt.Sprintf("%d:%d", b.ID, amt),
		})
	}
	markup := inlineMarkup(
		telebot.Row(amountBtns[:3]),
		telebot.Row(amountBtns[3:]),
		telebot.Row{
			{Text: "✏️ 自定义金额", Unique: btnAmtCustomEndpoint.Unique, Data: strconv.Itoa(int(b.ID))},
			btnCancel,
		},
	)

	if edit {
		return editOrSend(c, msg, markup, telebot.ModeHTML)
	}
	return c.Send(msg, markup, telebot.ModeHTML)
}

func (bot *Bot) handleAmtCallback(c telebot.Context) error {
	userID := c.Sender().ID
	parts := strings.SplitN(strings.TrimSpace(c.Callback().Data), ":", 2)
	if len(parts) != 2 {
		respond(c, "无效的金额选项")
		return nil
	}
	amount, ok := parseAmount(parts[1])
	if !ok {
		respond(c, "无效的金额选项")
		return nil
	}
	b, err := bot.findBinding(userID, parts[0])
	if err != nil {
		respond(c, "未找到该账号，可能已被解绑")
		return nil
	}

	if !bot.tryLock(userID) {
		respond(c, "正在处理中，请稍候…")
		return nil
	}
	defer bot.unlock(userID)
	respond(c, "正在生成充值链接…")
	bot.clearState(userID)

	// Remove the buttons right away so the menu cannot be clicked twice.
	_ = bot.edit(c.Callback(), fmt.Sprintf("⏳ 正在生成 %s 元充值链接，请稍候…", formatNum(amount)))
	return bot.generateRecharge(c, b, amount, c.Callback())
}

func (bot *Bot) handleAmtCustomCallback(c telebot.Context) error {
	respond(c)
	userID := c.Sender().ID
	b, err := bot.findBinding(userID, c.Callback().Data)
	if err != nil {
		return c.Send("❌ 未找到该账号，可能已被解绑。")
	}

	bot.setState(userID, StateRecharge_WaitAmount)
	bot.setTempData(userID, "recharge_binding_id", strconv.Itoa(int(b.ID)))
	msg := fmt.Sprintf("✏️ 请输入为 <b>%s</b> 充值的金额（%s–%s 元，例如 <code>15</code>）：\n发送 /cancel 取消。",
		esc(roomDisplay(b)), formatNum(MinRechargeAmt), formatNum(MaxRechargeAmt))
	return editOrSend(c, msg, inlineMarkup(telebot.Row{btnCancel}), telebot.ModeHTML)
}

func (bot *Bot) onRechargeAmountInput(c telebot.Context, text string) error {
	userID := c.Sender().ID
	amount, ok := parseAmount(text)
	if !ok {
		return c.Send(fmt.Sprintf("❌ 输入无效，请输入 %s–%s 之间的金额（最多两位小数），或发送 /cancel 取消。",
			formatNum(MinRechargeAmt), formatNum(MaxRechargeAmt)))
	}

	b, err := bot.findBinding(userID, bot.getTempData(userID, "recharge_binding_id"))
	bot.clearState(userID)
	if err != nil {
		return c.Send("❌ 未找到该账号，可能已被解绑。")
	}

	if !bot.tryLock(userID) {
		return c.Send("⏳ 正在处理中，请稍候…")
	}
	defer bot.unlock(userID)

	waitMsg, err := bot.B.Send(c.Recipient(), fmt.Sprintf("⏳ 正在生成 %s 元充值链接，请稍候…", formatNum(amount)))
	if err != nil {
		return err
	}
	return bot.generateRecharge(c, b, amount, waitMsg)
}

// generateRecharge creates the payment link and renders the result into target.
func (bot *Bot) generateRecharge(c telebot.Context, b *model.Binding, amount float64, target telebot.Editable) error {
	userID := c.Sender().ID
	if b.CustomerCode != wanxiao.SupportedSchoolCode {
		return bot.edit(target, unsupportedSchoolMsg)
	}

	log.Printf("[Recharge] Generating URL for user=%d bindingID=%d amount=%.2f", userID, b.ID, amount)

	rechargeURL, err := bot.rechargeURL(b, amount)
	if err != nil {
		log.Printf("[Recharge] Failed to generate recharge URL for user=%d bindingID=%d: %v", userID, b.ID, err)
		retry := inlineMarkup(telebot.Row{{
			Text:   "🔁 重试",
			Unique: btnAmtEndpoint.Unique,
			Data:   fmt.Sprintf("%d:%s", b.ID, formatNum(amount)),
		}})
		return bot.edit(target, fmt.Sprintf("❌ 生成充值链接失败：%s", esc(friendlyErr(err))), retry, telebot.ModeHTML)
	}
	log.Printf("[Recharge] Generated URL for user=%d bindingID=%d (len=%d)", userID, b.ID, len(rechargeURL))

	msg := fmt.Sprintf("✅ <b>充值链接已生成</b>\n\n"+
		"🏠 房间：<code>%s</code>\n"+
		"👤 账号：<code>%s</code>\n"+
		"💰 金额：<code>%.2f</code> 元\n\n"+
		"请点击下方按钮完成支付。如按钮无法打开，可展开复制链接：\n"+
		"<blockquote expandable>%s</blockquote>",
		esc(roomDisplay(b)), esc(b.Account), amount, esc(rechargeURL))

	markup := &telebot.ReplyMarkup{}
	markup.Inline(markup.Row(markup.URL("💳 前往支付", rechargeURL)))
	return bot.edit(target, msg, markup, telebot.ModeHTML, telebot.NoPreview)
}

// rechargeURL fills cached student info (fetching and saving it once) and requests the payment link.
func (bot *Bot) rechargeURL(b *model.Binding, amount float64) (string, error) {
	if b.RealName == "" {
		realName, schoolName, err := bot.Client.GetStudentInfo(b.Account, b.CustomerCode)
		if err != nil {
			return "", err
		}
		b.RealName, b.SchoolName = realName, schoolName
		if err := bot.DB.Model(&model.Binding{}).Where("id = ?", b.ID).
			Updates(map[string]interface{}{"real_name": realName, "school_name": schoolName}).Error; err != nil {
			log.Printf("[DB] cache student info for binding %d failed: %v", b.ID, err)
		}
	}

	return bot.Client.GetRechargeURL(wanxiao.RechargeRequest{
		Account:      b.Account,
		CustomerCode: b.CustomerCode,
		RoomVerify:   b.RoomVerify,
		RoomName:     b.RoomName,
		RealName:     b.RealName,
		SchoolName:   b.SchoolName,
		AmountYuan:   amount,
	})
}
