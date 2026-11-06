package history

import (
	"testing"
	"time"

	"wanxiao-elec-bot/model"
)

func dailySamples(start, end time.Time, usagePerHour float64) []model.BalanceSample {
	var out []model.BalanceSample
	for date := start; !date.After(end); date = date.Add(30 * time.Minute) {
		out = append(out, model.BalanceSample{ObservedAt: date, Balance: 200 - date.Sub(start).Hours()*usagePerHour, MeterKey: "room-a"})
	}
	return out
}

func TestDaysSumCalendarDatesAndKeepPartialToday(t *testing.T) {
	start, end := at("2026-11-01 00:00"), at("2026-11-03 10:30")
	r := Analyze(dailySamples(start, end, 0.2), start, end)
	days := r.Days()
	if len(days) != 3 {
		t.Fatalf("expected three calendar days: %+v", days)
	}
	for i := 0; i < 2; i++ {
		d := days[i]
		near(t, d.Usage, 4.8)
		if !d.Complete() || d.Partial || d.Covered != 24*time.Hour || d.Status() != "完整" {
			t.Fatalf("full day lost coverage: %+v", d)
		}
	}
	near(t, days[2].Usage, 2.1)
	if days[2].Complete() || !days[2].Partial || days[2].Covered != 10*time.Hour+30*time.Minute || days[2].Status() != "未满一天" {
		t.Fatalf("current day was compared with whole days: %+v", days[2])
	}
	var sum float64
	for _, d := range days {
		sum += d.Usage
	}
	near(t, sum, r.Summary().Usage)
}

func TestDaysUseReportTimezoneAndSplitMidnight(t *testing.T) {
	start, end := at("2026-11-01 23:30"), at("2026-11-02 00:30")
	samples := []model.BalanceSample{
		{ObservedAt: start.UTC(), Balance: 10, MeterKey: "room-a"},
		{ObservedAt: start.Add(30 * time.Minute).UTC(), Balance: 9.6, MeterKey: "room-a"},
		{ObservedAt: end.UTC(), Balance: 9.4, MeterKey: "room-a"},
	}
	days := Analyze(samples, start, end).Days()
	if len(days) != 2 || days[0].Start.Format("2006-01-02") != "2026-11-01" || days[1].Start.Format("2006-01-02") != "2026-11-02" {
		t.Fatal("UTC observations must group by the report's local date", days)
	}
	near(t, days[0].Usage, 0.4)
	near(t, days[1].Usage, 0.2)
	for _, d := range days {
		if d.Covered != 30*time.Minute || !d.Partial || d.Complete() {
			t.Fatal(d)
		}
	}
}

func TestDaysDistinguishMissingDatesFromObservedZero(t *testing.T) {
	start, end := at("2026-11-01 00:00"), at("2026-11-04 00:00")
	r := Analyze(dailySamples(start.AddDate(0, 0, 1), start.AddDate(0, 0, 2), 0), start, end)
	days := r.Days()
	if len(days) != 3 || days[0].Covered != 0 || days[2].Covered != 0 || days[0].Status() != "无数据" || days[2].Status() != "无数据" {
		t.Fatalf("missing dates must remain visible: %+v", days)
	}
	if days[1].Usage != 0 || !days[1].Complete() || days[1].Covered != 24*time.Hour {
		t.Fatal("observed zero must retain full coverage", days[1])
	}
}

func TestDaysDoNotRestoreExcludedGapsOrRecharge(t *testing.T) {
	start, end := at("2026-11-01 00:00"), at("2026-11-03 00:00")
	samples := dailySamples(start, end, 0.2)
	// Day one loses a scheduled observation; day two contains a balance
	// increase. Neither interval can contribute an invented daily estimate.
	samples = append(samples[:12], samples[13:]...)
	for i := range samples {
		if !samples[i].ObservedAt.Before(at("2026-11-02 12:00")) {
			samples[i].Balance += 20
		}
	}
	r := Analyze(samples, start, end)
	days := r.Days()
	if len(days) != 2 || !days[0].Gap || days[0].Increase || days[0].Complete() || !days[1].Increase || days[1].Complete() {
		t.Fatal(days)
	}
	near(t, days[0].Usage, 4.6)
	near(t, days[1].Usage, 4.7)
	if days[0].Covered != 23*time.Hour || days[1].Covered != 23*time.Hour+30*time.Minute {
		t.Fatal(days)
	}
	near(t, days[0].Usage+days[1].Usage, r.Summary().Usage)
}

func TestDaysDoNotCombineDifferentMeters(t *testing.T) {
	start, end := at("2026-11-01 00:00"), at("2026-11-03 00:00")
	samples := dailySamples(start, end, 0.2)
	for i := range samples {
		if !samples[i].ObservedAt.Before(at("2026-11-02 00:00")) {
			samples[i].MeterKey = "room-b"
			samples[i].Balance -= 100
		}
	}
	r := Analyze(samples, start, end)
	days := r.Days()
	if !r.MeterChanged || len(days) != 2 || days[0].Usage != 0 || days[0].Covered != 0 || days[0].Complete() {
		t.Fatal("earlier meter was included in a daily total", days)
	}
	near(t, days[1].Usage, 4.8)
	if !days[1].Complete() {
		t.Fatal(days[1])
	}
}

func TestDaysRangeEndIsExclusiveAndInvalidRangesAreEmpty(t *testing.T) {
	start, end := at("2026-11-01 00:00"), at("2026-11-02 00:00")
	days := (Report{Start: start, End: end}).Days()
	if len(days) != 1 || !days[0].End.Equal(end) || days[0].Partial || days[0].Complete() {
		t.Fatal(days)
	}
	for _, r := range []Report{{}, {Start: end, End: start}, {Start: start, End: start}} {
		if len(r.Days()) != 0 {
			t.Fatal("invalid range produced daily data", r.Days())
		}
	}
}
