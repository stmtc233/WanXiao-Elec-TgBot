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
)

type balanceResult struct {
	rooms []wanxiao.RoomInfo
	err   error
}

// queryBindings fetches balances for all bindings concurrently (bounded).
func (bot *Bot) queryBindings(bindings []model.Binding) []balanceResult {
	results := make([]balanceResult, len(bindings))
	sem := make(chan struct{}, queryConcurrency)
	var wg sync.WaitGroup
	for i := range bindings {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b := &bindings[i]
			rooms, err := bot.Client.GetBalance(b.Account, b.CustomerCode)
			if err == nil && len(rooms) == 0 {
				err = &wanxiao.APIError{Msg: "未查询到房间信息"}
			}
			results[i] = balanceResult{rooms: rooms, err: err}
		}(i)
	}
	wg.Wait()
	return results
}

// 🔌 Query Electricity
func (bot *Bot) handleElec(c telebot.Context) error {
	userID := c.Sender().ID
	log.Printf("[Elec] Query requested by user=%d", userID)
	bot.clearState(userID)

	bindings, err := bot.userBindings(userID)
	if err != nil {
		return c.Send("❌ 读取账号信息失败，请稍后再试。")
	}
	if len(bindings) == 0 {
		return noBindingsReply(c)
	}

	if !bot.tryLock(userID) {
		return c.Send("⏳ 正在查询中，请稍候…")
	}
	defer bot.unlock(userID)

	waitMsg, err := bot.B.Send(c.Recipient(), "🔍 正在查询电量，请稍候…")
	if err != nil {
		return err
	}
	text, markup := bot.renderElec(userID, bindings)
	return bot.edit(waitMsg, text, markup, telebot.ModeHTML)
}

// 🔄 Refresh button on the query result
func (bot *Bot) handleRefreshElec(c telebot.Context) error {
	userID := c.Sender().ID
	if !bot.tryLock(userID) {
		respond(c, "正在查询中，请稍候…")
		return nil
	}
	defer bot.unlock(userID)
	respond(c, "正在刷新…")

	bindings, err := bot.userBindings(userID)
	if err != nil {
		return c.Send("❌ 读取账号信息失败，请稍后再试。")
	}
	if len(bindings) == 0 {
		return noBindingsReply(c)
	}
	text, markup := bot.renderElec(userID, bindings)
	return bot.edit(c.Callback(), text, markup, telebot.ModeHTML)
}

func (bot *Bot) renderElec(userID int64, bindings []model.Binding) (string, *telebot.ReplyMarkup) {
	threshold := -1.0
	if user, err := bot.getOrCreateUser(userID); err == nil && user.NotifyEnabled {
		threshold = user.NotifyThreshold
	}

	results := bot.queryBindings(bindings)

	var sb strings.Builder
	sb.WriteString("📊 <b>电量状态</b>\n")
	var rows []telebot.Row

	for i := range bindings {
		b := &bindings[i]
		res := results[i]
		sb.WriteString("\n")

		if res.err != nil {
			log.Printf("[Elec] Query error for user=%d account=%s: %v", userID, b.Account, res.err)
			fmt.Fprintf(&sb, "🏠 <b>%s</b>\n❌ 查询失败：%s\n", esc(roomDisplay(b)), esc(friendlyErr(res.err)))
			if !b.LastCheck.IsZero() {
				fmt.Fprintf(&sb, "🕘 上次记录：<code>%.2f</code> 度（%s）\n", b.LastBalance, formatAgo(b.LastCheck))
			}
			continue
		}

		for _, room := range res.rooms {
			fmt.Fprintf(&sb, "🏠 <b>%s</b>\n⚡ 余额：<code>%.2f</code> 度", esc(room.RoomName), room.Balance)
			if threshold >= 0 && room.Balance < threshold {
				fmt.Fprintf(&sb, "  ⚠️ 低于阈值 %s 度", formatNum(threshold))
			}
			sb.WriteString("\n")
		}
		bot.saveBalance(b, res.rooms[0], nil)

		if b.CustomerCode == wanxiao.SupportedSchoolCode {
			rows = append(rows, telebot.Row{{
				Text:   fmt.Sprintf("⚡ 充值 %s", roomDisplay(b)),
				Unique: btnPayAccEndpoint.Unique,
				Data:   strconv.Itoa(int(b.ID)),
			}})
		}
	}

	fmt.Fprintf(&sb, "\n🕐 更新于 %s", time.Now().In(displayLoc).Format("01-02 15:04:05"))
	rows = append(rows, telebot.Row{btnRefreshElec})
	return sb.String(), inlineMarkup(rows...)
}
