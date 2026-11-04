package bot

import (
	"bytes"
	"encoding/csv"
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
		return bot.showStats(c, &bindings[0], "24h")
	}
	var rows []telebot.Row
	for _, b := range bindings {
		rows = append(rows, telebot.Row{{Text: "🏠 " + roomDisplay(&b), Unique: btnStatsEndpoint.Unique, Data: fmt.Sprintf("%d:24h:summary", b.ID)}})
	}
	return editOrSend(c, "📊 <b>用电统计</b>\n\n请选择要查看的账号：", inlineMarkup(rows...), telebot.ModeHTML)
}

func statsMarkup(id uint, period string, hours int) *telebot.ReplyMarkup {
	button := func(text, p, v string) telebot.Btn {
		return telebot.Btn{Text: text, Unique: btnStatsEndpoint.Unique, Data: fmt.Sprintf("%d:%s:%s", id, p, v)}
	}
	rows := []telebot.Row{
		{button("近24小时", "24h", "summary"), button("近48小时", "48h", "summary")},
		{button("近7天", "7d", "summary"), button("近30天", "30d", "summary")},
		{button("本月", "month", "summary"), button("上月", "prevmonth", "summary"), {Text: "📅 选日期", Unique: btnStatsDate.Unique, Data: strconv.FormatUint(uint64(id), 10)}},
	}
	if hours <= 48 {
		rows = append(rows, telebot.Row{button("📊 每小时耗电", period, "hours"), button("🟩 小时热力图", period, "heatmap")})
	} else {
		rows = append(rows, telebot.Row{button("🟩 小时热力图", period, "heatmap")})
	}
	rows = append(rows, telebot.Row{button("🕘 时段平均", period, "profile"), button("📈 余额趋势", period, "balance")},
		telebot.Row{button("📄 导出CSV", period, "csv"), button("🔄 更新统计", period, "summary")}, telebot.Row{{Text: "↩️ 选择账号", Unique: btnStatsBack.Unique}})
	return inlineMarkup(rows...)
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

func (bot *Bot) showStats(c telebot.Context, b *model.Binding, period string) error {
	r, err := bot.loadStats(b, period)
	if err != nil {
		log.Printf("[Stats] load failed: %v", err)
		return c.Send("❌ 读取历史失败，请稍后再试。")
	}
	s := r.Summary()
	room := roomDisplay(b)
	if r.Latest != nil && r.Latest.RoomName != "" {
		room = r.Latest.RoomName
	}
	var out strings.Builder
	fmt.Fprintf(&out, "📊 <b>用电统计 · %s</b>\n%s 至 %s（北京时间）\n\n", esc(room), r.Start.Format("01-02 15:04"), r.End.Format("01-02 15:04"))
	if !b.LastCheck.IsZero() {
		fmt.Fprintf(&out, "🔋 账号最新余额：<code>%.2f</code> 度（%s）\n", b.LastBalance, formatAgo(b.LastCheck))
		if room != roomDisplay(b) {
			fmt.Fprintf(&out, "当前绑定房间：%s\n", esc(roomDisplay(b)))
		}
	}
	fmt.Fprintf(&out, "⚡ 已记录估算耗电：<code>%.2f</code> 度\n🕘 完整小时：<code>%d / %d</code>\n", s.Usage, s.CompleteHours, len(r.Hours))
	if s.CompleteHours > 0 {
		average := (s.DayUsage + s.NightUsage) / float64(s.CompleteHours)
		fmt.Fprintf(&out, "📉 完整小时平均：<code>%.2f</code> 度/小时\n", average)
		if s.Peak != nil {
			fmt.Fprintf(&out, "🔝 耗电最高：%s–%s，约 <code>%.2f</code> 度\n", s.Peak.Start.Format("01-02 15:04"), s.Peak.End.Format("15:04"), s.Peak.Usage)
		}
		fmt.Fprintf(&out, "☀️ 白天（08–22点）：约 %.2f 度\n🌙 夜间（22–08点）：约 %.2f 度\n<i>时段对比只含完整小时。</i>\n", s.DayUsage, s.NightUsage)
	} else {
		out.WriteString("⏳ 尚无完整小时，正在积累采样数据。\n")
	}
	if len(r.Increases) > 0 {
		fmt.Fprintf(&out, "\n💡 检测到 %d 次余额上涨，疑似充值/调整，相关区间不计入耗电。\n", len(r.Increases))
	}
	for _, sample := range r.RawSamples {
		if r.Latest != nil && sample.MeterKey != r.Latest.MeterKey {
			out.WriteString("\n🏠 范围内更换过房间/电表，统计只含末次观测的电表；原始CSV保留全部采样。\n")
			break
		}
	}
	// Forecast against the recent week, regardless of the selected historical range.
	if b.LastBalance > 0 && time.Since(b.LastCheck) < time.Hour {
		if recent, err := bot.loadStats(b, "7d"); err == nil {
			weekly := recent.Summary()
			if weekly.CompleteHours >= 72 && weekly.DayUsage+weekly.NightUsage > 0 {
				days := b.LastBalance / (weekly.DayUsage + weekly.NightUsage) * float64(weekly.CompleteHours) / 24
				fmt.Fprintf(&out, "\n🔮 按近7天有效小时估算，当前余额约可用 %.1f 天。\n", days)
			}
		}
	}
	out.WriteString("\n每小时 00、30 分自动采样，预警共用这次检查。\n耗电由余额差估算；跨整点按时间分摊。缺失或充值期间可能低估，余额延迟更新会影响小时归属。")
	if err := editOrSend(c, out.String(), statsMarkup(b.ID, period, len(r.Hours)), telebot.ModeHTML); err != nil {
		return err
	}
	view := "hours"
	if len(r.Hours) > 48 {
		view = "heatmap"
	}
	return bot.sendStatsChart(c, b, r, view)
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
	if !bot.tryLock(c.Sender().ID) {
		respond(c, "正在生成统计，请稍候…")
		return nil
	}
	defer bot.unlock(c.Sender().ID)
	respond(c, "正在读取历史…")
	bot.clearState(c.Sender().ID)
	if parts[2] == "summary" {
		return bot.showStats(c, b, parts[1])
	}
	r, err := bot.loadStats(b, parts[1])
	if err != nil {
		log.Printf("[Stats] load failed: %v", err)
		return c.Send("❌ 读取历史失败，请稍后再试。")
	}
	if parts[2] == "csv" {
		return bot.exportStats(c, b, r)
	}
	return bot.sendStatsChart(c, b, r, parts[2])
}

func (bot *Bot) sendStatsChart(c telebot.Context, b *model.Binding, r history.Report, view string) error {
	room := roomDisplay(b)
	if r.Latest != nil && r.Latest.RoomName != "" {
		room = r.Latest.RoomName
	}
	data, err := charts.Render(r, view, room)
	if err != nil {
		log.Printf("[Stats] render failed: %v", err)
		return c.Send("❌ 生成图表失败，请重新选择时间范围。")
	}
	return c.Send(&telebot.Photo{File: telebot.FromReader(bytes.NewReader(data)), Caption: "耗电基于余额差估算；灰色表示无有效数据。"})
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

func (bot *Bot) exportStats(c telebot.Context, b *model.Binding, r history.Report) error {
	for _, item := range []struct {
		name   string
		encode func(history.Report) ([]byte, error)
	}{{"samples", rawCSV}, {"hourly", hourlyCSV}} {
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
