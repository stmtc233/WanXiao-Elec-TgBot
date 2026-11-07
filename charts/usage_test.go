package charts

import (
	"bytes"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wanxiao-elec-bot/history"
	"wanxiao-elec-bot/model"
)

func TestCalendarDayKeepsMissingZeroPartialAndFutureDistinct(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	start := time.Date(2026, 11, 7, 0, 0, 0, 0, loc)
	end := start.Add(2*time.Hour + 15*time.Minute)
	// No data before 01:00, a complete flat hour, then a partial decrease.
	samples := []model.BalanceSample{
		{ObservedAt: start.Add(time.Hour), Balance: 20, MeterKey: "a"},
		{ObservedAt: start.Add(90 * time.Minute), Balance: 20, MeterKey: "a"},
		{ObservedAt: start.Add(2 * time.Hour), Balance: 20, MeterKey: "a"},
		{ObservedAt: end, Balance: 19.9, MeterKey: "a"},
	}
	r := history.Analyze(samples, start, end)
	panels := hourPanels(r)
	if len(panels) != 2 || len(panels[1].columns) != 12 {
		t.Fatal("calendar day lost its fixed hour positions")
	}
	cols := panels[0].columns
	if cols[0].covered != 0 || cols[0].future || cols[1].usage != 0 || !cols[1].complete || cols[2].complete || cols[2].covered != 15*time.Minute || !cols[3].future {
		t.Fatal("missing, zero, partial and future became indistinguishable", cols[:4])
	}
	data, err := Render(r, "hours", "示例宿舍")
	if err != nil {
		t.Fatal(err)
	}
	writeUsagePreview(t, "hours-today", data)
}

func TestMidnightAndEmptyReportsRenderWithoutFabricatedTotals(t *testing.T) {
	start := time.Date(2026, 11, 7, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	for _, end := range []time.Time{start, start.Add(12 * time.Hour)} {
		r := history.Analyze(nil, start, end)
		panels := hourPanels(r)
		if len(panels) != 2 {
			t.Fatal("empty calendar day did not preserve 24 slots")
		}
		for _, view := range []string{"hours", "daily"} {
			data, err := Render(r, view, "暂无数据")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := png.Decode(bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestDailyChartFitsWeekAndMonthIntoOnePhoto(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	for _, count := range []int{7, 31} {
		var samples []model.BalanceSample
		balance := 300.0
		for i := 0; i <= count*48; i++ {
			at := start.Add(time.Duration(i) * 30 * time.Minute)
			if i == 70 { // Keep a genuine internal gap visible.
				continue
			}
			balance -= (2 + math.Sin(float64(i/48))) / 48
			samples = append(samples, model.BalanceSample{ObservedAt: at, Balance: balance, MeterKey: "a"})
		}
		r := history.Analyze(samples, start, start.AddDate(0, 0, count))
		pages, err := RenderPages(r, "daily", "测试宿舍 301")
		if err != nil || len(pages) != 1 {
			t.Fatal("daily range was split across messages", err, len(pages))
		}
		img, err := png.Decode(bytes.NewReader(pages[0]))
		if err != nil || img.Bounds().Dx() != 900 || img.Bounds().Dy() > 1280 {
			t.Fatal("daily chart is not a compact photo", err)
		}
		name := "daily-week"
		if count == 31 {
			name = "daily-month"
		}
		writeUsagePreview(t, name, pages[0])
	}
}

func TestSmallAxisValuesRemainNumeric(t *testing.T) {
	if axisUsageText(0, 0.0002) != "0" || axisUsageText(0.0002, 0.0002) != "0.0002" || axisUsageText(1, 0.5) != "1" {
		t.Fatal("small-scale ticks were rounded to zero or repeated less-than labels")
	}
}

func writeUsagePreview(t *testing.T, name string, data []byte) {
	t.Helper()
	if dir := os.Getenv("WANXIAO_CHART_PREVIEW_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".png"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
}
