package bot

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"wanxiao-elec-bot/charts"
	"wanxiao-elec-bot/history"
	"wanxiao-elec-bot/model"

	"gopkg.in/telebot.v3"
)

func statsRange(period string, now time.Time) (time.Time, time.Time, error) {
	now = now.In(displayLoc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, displayLoc)
	switch period {
	case "today":
		return today, now, nil
	case "yesterday":
		return today.AddDate(0, 0, -1), today, nil
	case "24h":
		return now.Truncate(time.Hour).Add(-23 * time.Hour), now, nil
	case "48h":
		return now.Truncate(time.Hour).Add(-47 * time.Hour), now, nil
	case "7d":
		return today.AddDate(0, 0, -6), now, nil
	case "30d":
		return today.AddDate(0, 0, -29), now, nil
	case "month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayLoc), now, nil
	case "prevmonth":
		end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayLoc)
		return end.AddDate(0, -1, 0), end, nil
	}
	parts := strings.Split(period, "-")
	if len(parts) != 2 {
		return time.Time{}, time.Time{}, fmt.Errorf("无效的时间范围")
	}
	start, err := time.ParseInLocation("20060102", parts[0], displayLoc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	last, err := time.ParseInLocation("20060102", parts[1], displayLoc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end := last.AddDate(0, 0, 1)
	if last.Before(start) || last.After(today) || end.Sub(start) > 31*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("请选择截至今天、最多 31 天的日期范围")
	}
	if end.After(now) {
		end = now
	}
	return start, end, nil
}

func (bot *Bot) handleStats(c telebot.Context) error {
	respond(c)
	if !bot.tryLock(c.Sender().ID) {
		return c.Send("⏳ 正在处理中，请稍候…")
	}
	defer bot.unlock(c.Sender().ID)
	bot.clearState(c.Sender().ID)
	bindings, err := bot.userBindings(c.Sender().ID)
	if err != nil {
		return c.Send("❌ 读取账号信息失败，请稍后再试。")
	}
	if len(bindings) == 0 {
		return noBindingsReply(c)
	}
	if len(bindings) == 1 {
		return bot.showStats(c, &bindings[0], "today")
	}
	var rows []telebot.Row
	for _, b := range bindings {
		rows = append(rows, telebot.Row{{Text: "🏠 " + roomDisplay(&b), Unique: btnStatsEndpoint.Unique, Data: fmt.Sprintf("%d:today:hours", b.ID)}})
	}
	// A photo cannot be edited into text. Reuse its caption and keyboard for
	// account selection, then replace the photo after an account is chosen.
	if cb := c.Callback(); cb != nil && cb.Message != nil && cb.Message.Photo != nil {
		err := c.EditCaption("📊 <b>用电统计</b>\n\n请选择账号：", inlineMarkup(rows...), telebot.ModeHTML)
		if errors.Is(err, telebot.ErrMessageNotModified) {
			return nil
		}
		return err
	}
	return editOrSend(c, "📊 <b>用电统计</b>\n\n请选择要查看的账号：", inlineMarkup(rows...), telebot.ModeHTML)
}

func statsMarkup(id uint, period, view string, more, multiple bool, now time.Time) *telebot.ReplyMarkup {
	button := func(text, p, v string) telebot.Btn {
		return telebot.Btn{Text: text, Unique: btnStatsEndpoint.Unique, Data: fmt.Sprintf("%d:%s:%s", id, p, v)}
	}
	selected := func(text string, active bool) string {
		if active {
			return "✓ " + text
		}
		return text
	}
	hourPeriod := chartPeriod(period, "hours", now)
	dailyPeriod := chartPeriod(period, "daily", now)
	if view == "daily" {
		dailyPeriod = period
	}
	rows := []telebot.Row{
		{button(selected("每小时", view == "hours"), hourPeriod, "hours"), button(selected("每天", view == "daily"), dailyPeriod, "daily")},
	}
	if view == "hours" {
		start, _, _ := statsRange(period, now)
		today, _, _ := statsRange("today", now)
		row := telebot.Row{button("‹ 前一天", dayPeriod(start.AddDate(0, 0, -1)), "hours"), button(selected("今天", start.Equal(today)), "today", "hours")}
		if next := start.AddDate(0, 0, 1); !next.After(today) {
			row = append(row, button("后一天 ›", dayPeriod(next), "hours"))
		}
		rows = append(rows, row)
	} else {
		rows = append(rows, telebot.Row{button(selected("近7天", period == "7d"), "7d", "daily"), button(selected("近30天", period == "30d"), "30d", "daily"), button(selected("本月", period == "month"), "month", "daily")})
	}
	moreText, moreAction := "更多", "more-"+view
	if more {
		moreText, moreAction = "收起", "less-"+view
	}
	rows = append(rows, telebot.Row{{Text: "📅 选日期", Unique: btnStatsDate.Unique, Data: strconv.FormatUint(uint64(id), 10)}, button(moreText, period, moreAction)})
	if more {
		if view == "daily" {
			// Seven recent calendar days are directly reachable from the daily
			// view. Older dates remain available through the date input.
			start, end, _ := statsRange(period, now)
			last := end.Add(-time.Nanosecond)
			last = time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, displayLoc)
			var row telebot.Row
			for day := maxDate(start, last.AddDate(0, 0, -6)); !day.After(last); day = day.AddDate(0, 0, 1) {
				row = append(row, button(day.Format("01-02")+"逐时", dayPeriod(day), "hours"))
				if len(row) == 4 {
					rows = append(rows, row)
					row = nil
				}
			}
			if len(row) > 0 {
				rows = append(rows, row)
			}
		}
		rows = append(rows, telebot.Row{button("📄 导出CSV", period, "csv"), button("↻ 更新", period, view)})
		if view == "daily" {
			rows = append(rows, telebot.Row{button("上月", "prevmonth", "daily")})
		}
		if multiple {
			rows = append(rows, telebot.Row{{Text: "切换账号", Unique: btnStatsBack.Unique}})
		}
	}
	return inlineMarkup(rows...)
}

func maxDate(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func dayPeriod(day time.Time) string {
	return day.Format("20060102") + "-" + day.Format("20060102")
}

// Each hourly chart shows one calendar day. Switching back from a daily
// range opens its last day, so historical selections do not jump to today.
func chartPeriod(period, view string, now time.Time) string {
	start, end, err := statsRange(period, now)
	if err != nil {
		return "today"
	}
	last := end.Add(-time.Nanosecond)
	if !end.After(start) {
		last = start
	}
	oneDay := start.Format("20060102") == last.Format("20060102")
	if view == "daily" {
		if oneDay {
			today, _, _ := statsRange("today", now)
			if start.Before(today) {
				return start.AddDate(0, 0, -6).Format("20060102") + "-" + start.Format("20060102")
			}
			return "7d"
		}
		return period
	}
	if oneDay {
		return period
	}
	return dayPeriod(last)
}

func (bot *Bot) loadStats(b *model.Binding, period string) (history.Report, error) {
	start, end, err := statsRange(period, time.Now())
	if err != nil {
		return history.Report{}, err
	}
	samples, err := history.Load(bot.DB, b.ID, start, end)
	if err != nil {
		return history.Report{}, err
	}
	return history.Analyze(samples, start, end), nil
}

func statsView(period string, now time.Time) string {
	start, end, err := statsRange(period, now)
	if err != nil || !end.After(start) || start.Format("20060102") == end.Add(-time.Nanosecond).Format("20060102") {
		return "hours"
	}
	return "daily"
}

func (bot *Bot) showStats(c telebot.Context, b *model.Binding, period string) error {
	return bot.showStatsChart(c, b, period, statsView(period, time.Now()), false)
}

func (bot *Bot) handleStatsCallback(c telebot.Context) error {
	parts := strings.Split(c.Callback().Data, ":")
	if len(parts) != 3 {
		respond(c, "该按钮已失效")
		return nil
	}
	b, err := bot.findBinding(c.Sender().ID, parts[0])
	if err != nil {
		respond(c, "该账号不存在或已解绑")
		return nil
	}
	if _, _, err := statsRange(parts[1], time.Now()); err != nil {
		respond(c, "时间范围无效，请重新选择")
		return nil
	}
	view, more := parts[2], false
	switch view {
	case "hours", "daily", "csv":
	case "more-hours", "more-daily":
		view, more = strings.TrimPrefix(view, "more-"), true
	case "less-hours", "less-daily":
		view = strings.TrimPrefix(view, "less-")
	case "more", "details":
		view, more = statsView(parts[1], time.Now()), true
	case "summary", "less":
		view = statsView(parts[1], time.Now())
	default:
		respond(c, "请重新打开用电统计")
		return nil
	}
	if !bot.tryLock(c.Sender().ID) {
		respond(c, "正在生成统计，请稍候…")
		return nil
	}
	defer bot.unlock(c.Sender().ID)
	respond(c)
	bot.clearState(c.Sender().ID)
	if view == "csv" {
		r, err := bot.loadStats(b, parts[1])
		if err != nil {
			log.Printf("[Stats] load failed: %v", err)
			return c.Send("❌ 读取历史失败，请稍后再试。")
		}
		return bot.exportStats(c, b, r)
	}
	period := parts[1]
	if view == "hours" {
		period = chartPeriod(period, view, time.Now())
	}
	return bot.showStatsChart(c, b, period, view, more)
}

func (bot *Bot) showStatsChart(c telebot.Context, b *model.Binding, period, view string, more bool) error {
	r, err := bot.loadStats(b, period)
	if err != nil {
		log.Printf("[Stats] load failed: %v", err)
		return c.Send("❌ 读取历史失败，请稍后再试。")
	}
	room := roomDisplay(b)
	if r.Latest != nil && r.Latest.RoomName != "" {
		room = r.Latest.RoomName
	}
	data, err := charts.Render(r, view, room)
	if err != nil {
		log.Printf("[Stats] render failed: %v", err)
		return c.Send("❌ 生成图表失败，请重新选择日期。")
	}
	bindings, err := bot.userBindings(c.Sender().ID)
	if err != nil {
		return c.Send("❌ 读取账号信息失败，请稍后再试。")
	}
	photo := &telebot.Photo{File: telebot.FromReader(bytes.NewReader(data)), Caption: statsCaption(r, view, more)}
	return editOrSend(c, photo, statsMarkup(b.ID, period, view, more, len(bindings) > 1, time.Now()), telebot.ModeHTML)
}

func statsCaption(r history.Report, view string, more bool) string {
	var out strings.Builder
	if view == "hours" {
		fmt.Fprintf(&out, "<b>%s · 每小时耗电</b>", r.Start.Format("2006-01-02"))
	} else {
		last := r.End.Add(-time.Nanosecond)
		if last.Before(r.Start) {
			last = r.Start
		}
		fmt.Fprintf(&out, "<b>%s 至 %s · 每天耗电</b>", r.Start.Format("2006-01-02"), last.Format("2006-01-02"))
	}
	out.WriteString("\n耗电由余额变化估算；0表示读数未下降。")
	if r.WholeDegreeSteps() {
		out.WriteString("\n⚠️ 余额呈整度阶梯变化，小时归属不精确。")
	}
	if r.UnallocatedDrop > 0 {
		fmt.Fprintf(&out, "\n缺采期间另有 %s 度下降，未计入图中。", statsUsage(r.UnallocatedDrop))
	}
	if r.MeterChanged {
		out.WriteString("\n范围内更换过房间或电表，仅统计最后的电表。")
	}
	if more {
		out.WriteString("\n\n<b>统计说明</b>\n每30分钟自动采样；跨整点按时间分摊。\n橙色：仅有部分数据；—：无有效数据；未到：未来时段。\n缺采和余额上涨区间不计入耗电，统计可能偏低。\n日均只用完整日期，当天不参与。\n上游读数延迟更新会影响耗电归属。CSV保留原始采样、每小时及每天统计。")
	}
	return out.String()
}

func statsUsage(value float64) string {
	if value > 0 && value < 0.005 {
		return "&lt;0.01"
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func (bot *Bot) handleStatsDate(c telebot.Context) error {
	b, err := bot.findBinding(c.Sender().ID, c.Callback().Data)
	if err != nil {
		respond(c, "该账号不存在或已解绑")
		return nil
	}
	respond(c)
	bot.setState(c.Sender().ID, StateStats_WaitDate)
	bot.setTempData(c.Sender().ID, "stats_binding", strconv.FormatUint(uint64(b.ID), 10))
	return c.Send("📅 请输入日期，例如 <code>2026-11-05</code>；或起止日期 <code>2026-11-01 2026-11-05</code>。\n最多选择31天，以北京时间为准。发送 /cancel 取消。", telebot.ModeHTML)
}

func parseStatsDate(text string, now time.Time) (string, error) {
	fields := strings.Fields(text)
	if len(fields) < 1 || len(fields) > 2 {
		return "", fmt.Errorf("请输入一个日期或两个起止日期")
	}
	if len(fields) == 1 {
		fields = append(fields, fields[0])
	}
	var dates []string
	for _, f := range fields {
		d, err := time.ParseInLocation("2006-01-02", f, displayLoc)
		if err != nil {
			return "", err
		}
		dates = append(dates, d.Format("20060102"))
	}
	period := strings.Join(dates, "-")
	_, _, err := statsRange(period, now)
	return period, err
}

func (bot *Bot) onStatsDateInput(c telebot.Context, text string) error {
	period, err := parseStatsDate(text, time.Now())
	if err != nil {
		return c.Send("❌ 日期无效，请输入 YYYY-MM-DD，或两个起止日期；截至今天，最多31天。")
	}
	b, err := bot.findBinding(c.Sender().ID, bot.getTempData(c.Sender().ID, "stats_binding"))
	if err != nil {
		bot.clearState(c.Sender().ID)
		return c.Send("❌ 该账号已解绑，请重新选择。")
	}
	bot.clearState(c.Sender().ID)
	return bot.showStats(c, b, period)
}

// Defang spreadsheet formula prefixes in user/upstream-controlled text cells.
func csvText(s string) string {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + s
	}
	return s
}

func rawCSV(r history.Report) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("\xef\xbb\xbf") // Excel UTF-8
	w := csv.NewWriter(&out)
	if err := w.Write([]string{"采样时间（北京时间）", "房间", "电表标识", "剩余电量（度）", "来源"}); err != nil {
		return nil, err
	}
	for _, s := range r.RawSamples {
		if err := w.Write([]string{s.ObservedAt.In(displayLoc).Format("2006-01-02 15:04:05"), csvText(s.RoomName), csvText(s.MeterKey), strconv.FormatFloat(s.Balance, 'f', 4, 64), s.Source}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return out.Bytes(), w.Error()
}

func hourlyCSV(r history.Report) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("\xef\xbb\xbf")
	w := csv.NewWriter(&out)
	if err := w.Write([]string{"开始时间（北京时间）", "结束时间（北京时间）", "估算耗电（度）", "有效覆盖分钟", "状态"}); err != nil {
		return nil, err
	}
	for _, h := range r.Hours {
		usage := ""
		if h.Covered > 0 {
			usage = strconv.FormatFloat(h.Usage, 'f', 4, 64)
		}
		if err := w.Write([]string{h.Start.Format("2006-01-02 15:04:05"), h.End.Format("2006-01-02 15:04:05"), usage, fmt.Sprintf("%.1f", h.Covered.Minutes()), h.Status()}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return out.Bytes(), w.Error()
}

func dailyCSV(r history.Report) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("\xef\xbb\xbf")
	w := csv.NewWriter(&out)
	if err := w.Write([]string{"日期（北京时间）", "估算耗电（度）", "有效覆盖分钟", "状态"}); err != nil {
		return nil, err
	}
	for _, d := range r.Days() {
		usage := ""
		if d.Covered > 0 {
			usage = strconv.FormatFloat(d.Usage, 'f', 4, 64)
		}
		if err := w.Write([]string{d.Start.Format("2006-01-02"), usage, fmt.Sprintf("%.1f", d.Covered.Minutes()), d.Status()}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return out.Bytes(), w.Error()
}

func (bot *Bot) exportStats(c telebot.Context, b *model.Binding, r history.Report) error {
	for _, item := range []struct {
		name   string
		encode func(history.Report) ([]byte, error)
	}{{"samples", rawCSV}, {"hourly", hourlyCSV}, {"daily", dailyCSV}} {
		data, err := item.encode(r)
		if err != nil {
			return err
		}
		name := fmt.Sprintf("%s_%d_%s_%s.csv", item.name, b.ID, r.Start.Format("20060102"), r.End.Format("20060102"))
		if err := c.Send(&telebot.Document{File: telebot.FromReader(bytes.NewReader(data)), FileName: name, Caption: "北京时间；耗电为估算值，空白耗电单元格表示无有效数据。"}); err != nil {
			return err
		}
	}
	return nil
}
