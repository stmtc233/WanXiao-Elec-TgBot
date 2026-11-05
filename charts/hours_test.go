package charts

import (
	"bytes"
	"encoding/csv"
	"image/png"
	"os"
	"strconv"
	"testing"
	"time"

	"wanxiao-elec-bot/history"
	"wanxiao-elec-bot/model"
)

func steppedReport(t *testing.T) history.Report {
	t.Helper()
	file, err := os.Open("testdata/stepped_samples.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	loc := time.FixedZone("CST", 8*60*60)
	var samples []model.BalanceSample
	for _, row := range rows[1:] {
		stamp, err := time.ParseInLocation("2006-01-02 15:04:05", row[0], loc)
		if err != nil {
			t.Fatal(err)
		}
		balance, err := strconv.ParseFloat(row[3], 64)
		if err != nil {
			t.Fatal(err)
		}
		samples = append(samples, model.BalanceSample{ObservedAt: stamp, RoomName: row[1], MeterKey: row[2], Balance: balance, Source: row[4]})
	}
	return history.Analyze(samples, time.Date(2026, 11, 4, 11, 0, 0, 0, loc), time.Date(2026, 11, 5, 10, 43, 38, 0, loc))
}

func TestSteppedReadingsRemainHonestAndFitOneReadablePhoto(t *testing.T) {
	r := steppedReport(t)
	if !r.WholeDegreeSteps() || r.MeterChanged || r.Summary().Usage != 3 || r.UnallocatedDrop != 1 {
		t.Fatal("wrong interpretation of stepped balances", r)
	}
	visible := visibleHours(r)
	if len(visible) != 11 || visible[0].Start.Hour() != 0 || visible[len(visible)-1].Start.Hour() != 10 {
		t.Fatal("blank hours crowded out the actual records", visible)
	}
	if len(r.Hours) != 24 || len(r.RawSamples) != 21 {
		t.Fatal("trimming changed export data")
	}
	pages, err := RenderPages(r, "hours", "测试宿舍")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatal("expected one readable image", len(pages))
	}
	img, err := png.Decode(bytes.NewReader(pages[0]))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 900 || img.Bounds().Dy() > 1280 {
		t.Fatal("photo would shrink the text excessively", img.Bounds())
	}
}

func TestHourPagesPreserveAllIntervalsAndInternalGaps(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	var samples []model.BalanceSample
	for i := 0; i <= 96; i++ {
		if i == 20 {
			continue
		}
		samples = append(samples, model.BalanceSample{ObservedAt: start.Add(time.Duration(i) * 30 * time.Minute), Balance: 100 - float64(i)*0.1, MeterKey: "a"})
	}
	r := history.Analyze(samples, start, start.Add(48*time.Hour))
	if len(visibleHours(r)) != 48 {
		t.Fatal("internal gap was trimmed")
	}
	pages, err := RenderPages(r, "hours", "测试宿舍")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 4 {
		t.Fatal("48 hours should be four pages", len(pages))
	}
	for _, data := range pages {
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil || img.Bounds().Dy() > 1280 {
			t.Fatal("unreadable photo size", err)
		}
	}
}

func TestTinyPositiveUsageNeverLooksLikeAnExactZero(t *testing.T) {
	if usageText(0) != "0" || usageText(0.0003) != "<0.01" || usageText(0.9997) != "1" {
		t.Fatal("display rounding hides positive readings")
	}
}

func TestBalanceAxisShowsSmallChangesAndHandlesFlatOrNegativeBalances(t *testing.T) {
	for _, limits := range [][2]float64{{215.6, 219.6}, {219.6, 219.6}, {-3, -1}} {
		low, high, step := balanceBounds(limits[0], limits[1])
		if low >= high || step <= 0 || low > limits[0] || high < limits[1] {
			t.Fatal(low, high, step)
		}
		if limits[0] > 200 && low < 200 {
			t.Fatal("axis collapsed small changes around a large balance", low, high)
		}
	}
}
