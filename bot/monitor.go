package bot

import (
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"wanxiao-elec-bot/model"
	"wanxiao-elec-bot/wanxiao"

	"gopkg.in/telebot.v3"
)

const (
	// remindAfter is how long to wait before reminding again while the balance stays low.
	remindAfter = 24 * time.Hour
	// maxBackoffShift caps the failure backoff at interval * 2^maxBackoffShift.
	maxBackoffShift = 3
	// maxBackoffWait caps the failure backoff in absolute time.
	maxBackoffWait = 6 * time.Hour
)

// monitorTarget is a binding due for a check together with its owner's settings.
type monitorTarget struct {
	user    *model.User
	binding model.Binding
}

// isDue reports whether a binding should be checked now, backing off on repeated failures.
func isDue(b *model.Binding, intervalMinutes int, now time.Time) bool {
	wait := time.Duration(effectiveInterval(intervalMinutes)) * time.Minute
	if b.FailCount > 0 {
		shift := b.FailCount
		if shift > maxBackoffShift {
			shift = maxBackoffShift
		}
		backoff := wait << shift
		if backoff > maxBackoffWait {
			backoff = maxBackoffWait
		}
		if backoff > wait {
			wait = backoff
		}
	}
	return now.Sub(b.LastAttempt) >= wait
}

// CheckLowBalance is called by cron scheduler
func (bot *Bot) CheckLowBalance() {
	bot.cleanupSessions()

	var users []model.User
	if err := bot.DB.Where("notify_enabled = ?", true).Preload("Bindings").Find(&users).Error; err != nil {
		log.Printf("[Monitor] load users failed: %v", err)
		return
	}

	// Group due bindings by (account, code) so each room is queried once per run.
	now := time.Now()
	groups := make(map[string][]monitorTarget)
	var keys []string
	for i := range users {
		user := &users[i]
		for _, b := range user.Bindings {
			if !isDue(&b, user.CheckInterval, now) {
				continue
			}
			key := b.Account + "\x00" + b.CustomerCode
			if _, ok := groups[key]; !ok {
				keys = append(keys, key)
			}
			groups[key] = append(groups[key], monitorTarget{user: user, binding: b})
		}
	}
	if len(keys) == 0 {
		return
	}
	log.Printf("[Monitor] checking %d rooms", len(keys))

	sem := make(chan struct{}, queryConcurrency)
	var wg sync.WaitGroup
	for _, key := range keys {
		targets := groups[key]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			first := targets[0].binding
			rooms, err := bot.Client.GetBalance(first.Account, first.CustomerCode)
			if err == nil && len(rooms) == 0 {
				err = &wanxiao.APIError{Msg: "未查询到房间信息"}
			}
			for _, t := range targets {
				bot.handleCheckResult(t, rooms, err)
			}
		}()
	}
	wg.Wait()
}

func (bot *Bot) handleCheckResult(t monitorTarget, rooms []wanxiao.RoomInfo, err error) {
	b := &t.binding
	now := time.Now()

	if err != nil {
		log.Printf("[Monitor] check failed for user=%d binding=%d (fails=%d): %v", t.user.ID, b.ID, b.FailCount+1, err)
		if err := bot.DB.Model(&model.Binding{}).Where("id = ?", b.ID).Updates(map[string]interface{}{
			"last_attempt": now,
			"fail_count":   b.FailCount + 1,
		}).Error; err != nil {
			log.Printf("[DB] update binding %d failed: %v", b.ID, err)
		}
		return
	}

	room := rooms[0]
	extra := map[string]interface{}{
		"last_attempt": now,
		"fail_count":   0,
	}

	low := room.Balance < t.user.NotifyThreshold
	switch {
	case low && (b.AlertedAt == nil || now.Sub(*b.AlertedAt) >= remindAfter):
		if bot.sendAlert(t.user, b, room) {
			extra["alerted_at"] = now
		}
	case !low && b.AlertedAt != nil:
		extra["alerted_at"] = nil
	}

	bot.saveBalance(b, room, extra)
}

// sendAlert pushes a low balance notice. It returns true if the message was delivered.
func (bot *Bot) sendAlert(user *model.User, b *model.Binding, room wanxiao.RoomInfo) bool {
	msg := fmt.Sprintf("⚠️ <b>低电量预警</b>\n\n🏠 房间：<code>%s</code>\n⚡ 余额：<code>%.2f</code> 度\n📉 阈值：<code>%s</code> 度",
		esc(room.RoomName), room.Balance, formatNum(user.NotifyThreshold))

	opts := []interface{}{telebot.ModeHTML}
	if b.CustomerCode == wanxiao.SupportedSchoolCode {
		opts = append(opts, inlineMarkup(telebot.Row{{
			Text:   "⚡ 立即充值",
			Unique: btnPayAccEndpoint.Unique,
			Data:   strconv.Itoa(int(b.ID)),
		}}))
	}

	_, err := bot.B.Send(&telebot.User{ID: user.ID}, msg, opts...)
	if err == nil {
		log.Printf("[Monitor] alert sent to user=%d binding=%d balance=%.2f", user.ID, b.ID, room.Balance)
		return true
	}

	log.Printf("[Monitor] send alert to user=%d failed: %v", user.ID, err)
	if isUnreachable(err) {
		// The user blocked the bot or is gone: stop notifying until they come back and re-enable it.
		if err := bot.DB.Model(&model.User{}).Where("id = ?", user.ID).Update("notify_enabled", false).Error; err != nil {
			log.Printf("[DB] disable notify for user %d failed: %v", user.ID, err)
		} else {
			log.Printf("[Monitor] disabled alerts for unreachable user=%d", user.ID)
		}
	}
	return false
}
