package bot

import (
	"encoding/csv"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"wanxiao-elec-bot/history"
	"wanxiao-elec-bot/model"

	"gopkg.in/telebot.v3"
)

func TestCalendarStatsNavigationAtMidnight(t *testing.T) {
	for _, now := range []time.Time{
		time.Date(2026, 11, 1, 0, 0, 0, 0, displayLoc),
		time.Date(2026, 11, 1, 13, 25, 0, 0, displayLoc),
	} {
		today := time.Date(2026, 11, 1, 0, 0, 0, 0, displayLoc)
		start, end, err := statsRange("today", now.UTC())
		if err != nil || !start.Equal(today) || !end.Equal(now) {
			t.Fatal("today lost local boundaries", start, end, err)
		}
		start, end, err = statsRange("yesterday", now.UTC())
		if err != nil || !start.Equal(today.AddDate(0, 0, -1)) || !end.Equal(today) {
			t.Fatal("yesterday did not cross month correctly", start, end, err)
		}
		markup := statsMarkup(1, "today", "hours", false, false, now)
		for _, row := range markup.InlineKeyboard {
			for _, btn := range row {
				if strings.Contains(btn.Text, "后一天") {
					t.Fatal("future date navigation offered", btn)
				}
			}
		}
		if statsView("today", now) != "hours" || chartPeriod("today", "hours", now) != "today" {
			t.Fatal("empty midnight window selected the previous date")
		}
		monthStart, monthEnd, _ := statsRange("month", now)
		caption := statsCaption(history.Analyze(nil, monthStart, monthEnd), "daily", true)
		if strings.Contains(caption, "2026-10-31") {
			t.Fatal("empty first-of-month range displayed reversed dates", caption)
		}
		for _, expanded := range []bool{false, true} {
			markup = statsMarkup(1, "month", "daily", expanded, true, now)
			want := ":month:more-daily"
			if expanded {
				want = ":month:less-daily"
			}
			found := false
			for _, row := range markup.InlineKeyboard {
				for _, btn := range row {
					found = found || strings.HasSuffix(btn.Data, want)
					if len(btn.Data) > 64 {
						t.Fatal("Telegram callback data exceeds limit", btn)
					}
				}
			}
			if !found {
				t.Fatal("more/less lost the selected daily mode on first of month", markup)
			}
		}
	}
	now := time.Date(2026, 11, 7, 15, 0, 0, 0, displayLoc)
	if got := chartPeriod("20261001-20261031", "hours", now); got != "20261031-20261031" {
		t.Fatal("switching to hourly lost historical selection", got)
	}
	if got := chartPeriod("20261031-20261031", "daily", now); got != "20261025-20261031" {
		t.Fatal("switching to daily lost historical selection", got)
	}
	if got := chartPeriod("today", "daily", now); got != "7d" {
		t.Fatal("today did not open recent daily overview", got)
	}
	if got := chartPeriod("yesterday", "daily", now); got != "20261031-20261106" {
		t.Fatal("yesterday did not anchor daily overview", got)
	}
	if statsView("20261031-20261031", now) != "hours" || statsView("20261001-20261031", now) != "daily" {
		t.Fatal("custom single/multiple dates selected wrong chart")
	}
}

func TestDailyCSVKeepsMissingAndIncreasesDistinctFromZero(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, displayLoc)
	r := history.Analyze(nil, start, start.AddDate(0, 0, 3))
	r.Hours[0].Covered = time.Hour // observed unchanged balance, not missing
	r.Hours[48].Increase = true
	data, err := dailyCSV(r)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\xef\xbb\xbf"))).ReadAll()
	if err != nil || len(rows) != 4 {
		t.Fatal(rows, err)
	}
	if rows[1][1] != "0.0000" || rows[1][2] != "60.0" || rows[2][1] != "" || rows[2][3] != "无数据" || rows[3][1] != "" || rows[3][3] != "疑似充值/调整" {
		t.Fatal("export fabricated zero consumption for unknown periods", rows)
	}
}

func TestStatsCaptionRemainsWithinTelegramLimitWithAllWarnings(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, displayLoc)
	var samples []model.BalanceSample
	for i := 0; i <= 12; i++ {
		samples = append(samples, model.BalanceSample{ObservedAt: start.Add(time.Duration(i) * 30 * time.Minute), Balance: 10 - float64(i/3), MeterKey: "a"})
	}
	r := history.Analyze(samples, start, start.Add(7*time.Hour))
	r.UnallocatedDrop, r.MeterChanged = 0.0001, true
	if !r.WholeDegreeSteps() {
		t.Fatal("fixture did not exercise coarse readings")
	}
	caption := statsCaption(r, "daily", true)
	if utf8.RuneCountInString(caption) > 1024 || !strings.Contains(caption, "&lt;0.01") || !strings.Contains(caption, "整度阶梯") || !strings.Contains(caption, "更换过房间") {
		t.Fatal("caption exceeds Telegram limit or lost data warnings", caption)
	}
}

func TestSingleAccountStatsOpensOneHourlyPhoto(t *testing.T) {
	db := statsDB(t)
	b := model.Binding{UserID: 100, Account: "own", CustomerCode: "1399"}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	bot := &Bot{DB: db, sessions: make(map[int64]*session)}
	c := &statsContext{userID: 100}
	if err := bot.handleStats(c); err != nil {
		t.Fatal(err)
	}
	if len(c.sent) != 1 {
		t.Fatal("opening stats emitted multiple messages", c.calls)
	}
	photo, ok := c.sent[0].(*telebot.Photo)
	if !ok || !strings.Contains(photo.Caption, "每小时耗电") {
		t.Fatal("single account did not open hourly photo", c.sent)
	}
}

func TestDailyMoreAndLessDoNotSwitchToHoursForSingleDate(t *testing.T) {
	db := statsDB(t)
	b := model.Binding{UserID: 100, Account: "own", CustomerCode: "1399"}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	bot := &Bot{DB: db, sessions: make(map[int64]*session)}
	for _, action := range []string{"daily", "more-daily", "less-daily"} {
		c := &statsContext{userID: 100, data: fmt.Sprintf("%d:20261101-20261101:%s", b.ID, action)}
		if err := bot.handleStatsCallback(c); err != nil {
			t.Fatal(err)
		}
		if len(c.sent) != 1 || c.calls[0].method != "editOrSend" {
			t.Fatal("view navigation sent extra messages", c.calls)
		}
		photo, ok := c.sent[0].(*telebot.Photo)
		if !ok || !strings.Contains(photo.Caption, "每天耗电") || strings.Contains(photo.Caption, "统计说明") != (action == "more-daily") {
			t.Fatal("expanding details changed the selected chart", c.sent)
		}
	}
}
