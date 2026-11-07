package charts

import (
	"bytes"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/image/font/sfnt"
	"wanxiao-elec-bot/history"
	"wanxiao-elec-bot/model"
)

func TestChartsRenderOfflineWithChineseFont(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	var samples []model.BalanceSample
	balance := 80.0
	for i := 0; i <= 7*48; i++ {
		at := start.Add(time.Duration(i) * 30 * time.Minute)
		if i == 60 {
			balance += 50
		}
		balance -= 0.1 + 0.15*(1+math.Sin(float64(at.Hour()-14)*math.Pi/12))
		if i == 100 {
			continue
		}
		samples = append(samples, model.BalanceSample{ObservedAt: at, Balance: balance, MeterKey: "a", RoomName: "测试宿舍 301"})
	}
	for _, view := range []string{"hours", "daily", "heatmap", "profile", "balance"} {
		t.Run(view, func(t *testing.T) {
			end := start.Add(7 * 24 * time.Hour)
			from := start
			if view == "hours" {
				from = end.Add(-24 * time.Hour)
			}
			r := history.Analyze(samples, from, end)
			data, err := Render(r, view, "测试宿舍 301")
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil || img.Bounds().Dx() != expectedWidth(view) || img.Bounds().Dy() < 500 {
				t.Fatal("invalid chart", err)
			}
			if dir := os.Getenv("WANXIAO_CHART_PREVIEW_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, view+".png"), data, 0644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	f, err := parsedFont()
	if err != nil {
		t.Fatal(err)
	}
	var buf sfnt.Buffer
	for _, r := range "每小时估算耗电热力图剩余趋势青色橙灰完整数据充值调整采样中断北京时间测试宿舍" {
		index, err := f.GlyphIndex(&buf, r)
		if err != nil || index == 0 {
			t.Fatal(fmt.Sprintf("missing glyph %c", r), err)
		}
	}
}

func TestEmptyReportRendersAndLongHourlyRangeIsRejected(t *testing.T) {
	start := time.Now().Truncate(time.Hour)
	r := history.Analyze(nil, start, start.Add(24*time.Hour))
	if _, err := Render(r, "hours", "暂无数据"); err != nil {
		t.Fatal(err)
	}
	r = history.Analyze(nil, start, start.Add(7*24*time.Hour))
	if _, err := Render(r, "hours", "长范围"); err == nil {
		t.Fatal("unreadable hourly range accepted")
	}
}

func expectedWidth(view string) int {
	if view == "hours" || view == "daily" || view == "balance" {
		return 900
	}
	return 1240
}
