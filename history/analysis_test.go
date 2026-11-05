package history

import (
	"math"
	"testing"
	"time"

	"wanxiao-elec-bot/model"
)

var testLoc = time.FixedZone("CST", 8*60*60)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, testLoc)
	if err != nil {
		panic(err)
	}
	return t
}

func sample(s string, b float64) model.BalanceSample {
	return model.BalanceSample{ObservedAt: at(s), Balance: b, MeterKey: "room-a", RoomName: "测试宿舍"}
}

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-8 {
		t.Fatalf("got %.10f want %.10f", got, want)
	}
}

func TestHourBoundaryInterpolationAndClipping(t *testing.T) {
	r := Analyze([]model.BalanceSample{
		sample("2026-11-01 07:50", 10), sample("2026-11-01 08:20", 9.4),
		sample("2026-11-01 08:50", 8.8), sample("2026-11-01 09:20", 8.2),
	}, at("2026-11-01 08:00"), at("2026-11-01 09:00"))
	if len(r.Hours) != 1 || !r.Hours[0].Complete() {
		t.Fatalf("expected complete hour: %+v", r.Hours)
	}
	near(t, r.Hours[0].Usage, 1.2)
	if r.Hours[0].Covered != time.Hour {
		t.Fatal(r.Hours[0].Covered)
	}
	if len(r.RawSamples) != 2 {
		t.Fatal("boundary neighbors must not be exported as raw readings in the selected range")
	}
}

func TestIncreaseIsNotNegativeOrZeroConsumption(t *testing.T) {
	r := Analyze([]model.BalanceSample{sample("2026-11-01 08:00", 10), sample("2026-11-01 08:30", 30), sample("2026-11-01 09:00", 29)}, at("2026-11-01 08:00"), at("2026-11-01 09:00"))
	near(t, r.Hours[0].Usage, 1)
	if r.Hours[0].Complete() || !r.Hours[0].Increase || r.Hours[0].Covered != 30*time.Minute || len(r.Increases) != 1 {
		t.Fatalf("%+v", r)
	}
	if r.Summary().CompleteHours != 0 {
		t.Fatal("recharge hour must not enter comparisons")
	}
}

func TestMissedCheckLeavesGapAndKnownZeroIsComplete(t *testing.T) {
	r := Analyze([]model.BalanceSample{sample("2026-11-01 08:00", 10), sample("2026-11-01 09:00", 9), sample("2026-11-01 09:30", 9), sample("2026-11-01 10:00", 9)}, at("2026-11-01 08:00"), at("2026-11-01 10:00"))
	if !r.Hours[0].Gap || r.Hours[0].Covered != 0 || r.Hours[0].Complete() {
		t.Fatal(r.Hours[0])
	}
	if !r.Hours[1].Complete() || r.Hours[1].Usage != 0 {
		t.Fatal("recorded zero is different from missing", r.Hours[1])
	}
	if r.Summary().CompleteHours != 1 {
		t.Fatal(r.Summary())
	}
}

func TestMeterChangesNeverBecomeUsage(t *testing.T) {
	a, b, c := sample("2026-11-01 08:00", 100), sample("2026-11-01 08:30", 5), sample("2026-11-01 09:00", 4)
	b.MeterKey, c.MeterKey = "room-b", "room-b"
	r := Analyze([]model.BalanceSample{a, b, c}, at("2026-11-01 08:00"), at("2026-11-01 09:00"))
	near(t, r.Summary().Usage, 1)
	if len(r.RawSamples) != 3 || len(r.Samples) != 2 {
		t.Fatal("export all raw samples, chart only selected meter")
	}
	if r.Hours[0].Complete() {
		t.Fatal("changed-meter interval cannot be complete")
	}
}

func TestPartialHourExcludedFromAverages(t *testing.T) {
	r := Analyze([]model.BalanceSample{sample("2026-11-01 08:00", 10), sample("2026-11-01 08:30", 9)}, at("2026-11-01 08:00"), at("2026-11-01 08:30"))
	near(t, r.Summary().Usage, 1)
	if !r.Hours[0].Partial || r.Summary().CompleteHours != 0 || r.Hours[0].Status() != "未满一小时" {
		t.Fatal(r)
	}
}

func TestSparseAndInvalidData(t *testing.T) {
	start, end := at("2026-11-01 08:00"), at("2026-11-01 10:00")
	for _, samples := range [][]model.BalanceSample{nil, {sample("2026-11-01 08:30", 3)}, {sample("2026-11-01 08:00", math.NaN()), sample("2026-11-01 08:30", 3)}} {
		r := Analyze(samples, start, end)
		if r.Summary().Usage != 0 || r.Summary().CompleteHours != 0 {
			t.Fatal(r)
		}
	}
}

func TestProfileUsesCompleteHoursOnly(t *testing.T) {
	r := Report{Hours: []Hour{
		{Start: at("2026-11-01 08:00"), End: at("2026-11-01 09:00"), Covered: time.Hour, Usage: 1},
		{Start: at("2026-11-02 08:00"), End: at("2026-11-02 09:00"), Covered: time.Hour, Usage: 3},
		{Start: at("2026-11-03 08:00"), End: at("2026-11-03 09:00"), Covered: 30 * time.Minute, Usage: 9},
		{Start: at("2026-11-01 23:00"), End: at("2026-11-02 00:00"), Covered: time.Hour, Usage: 2},
	}}
	s := r.Summary()
	near(t, s.Profile[8], 2)
	near(t, s.DayUsage, 4)
	near(t, s.NightUsage, 2)
	if s.ProfileCount[8] != 2 || s.CompleteHours != 3 {
		t.Fatal(s)
	}
}

func TestLegacyCachedTypeIsNotAFalseMeterChange(t *testing.T) {
	a := sample("2026-11-01 00:40", 20)
	a.Source = "cached"
	a.MeterKey = "same-room\x1f"
	b := sample("2026-11-01 01:26", 19)
	b.MeterKey = "same-room\x1f0"
	c := sample("2026-11-01 01:56", 18)
	c.MeterKey = b.MeterKey
	r := Analyze([]model.BalanceSample{a, b, c}, at("2026-11-01 00:00"), at("2026-11-01 02:00"))
	if r.MeterChanged || len(r.Samples) != 3 || r.UnallocatedDrop != 1 || !r.Hours[0].Gap {
		t.Fatal(r)
	}
	if r.RawSamples[0].MeterKey != a.MeterKey {
		t.Fatal("normalization changed original export")
	}
	near(t, r.Summary().Usage, 1)
}

func TestCachedTypeNeverJoinsAmbiguousMeters(t *testing.T) {
	a := sample("2026-11-01 00:00", 100)
	a.Source = "cached"
	a.MeterKey = "same-room\x1f"
	b := sample("2026-11-01 00:30", 50)
	b.MeterKey = "same-room\x1f0"
	c := sample("2026-11-01 01:00", 4)
	c.MeterKey = "same-room\x1f1"
	normalized := resolveCachedMeterKeys([]model.BalanceSample{a, b, c})
	if normalized[0].MeterKey != a.MeterKey {
		t.Fatal("ambiguous cached type was invented")
	}
	r := Analyze([]model.BalanceSample{a, b, c}, a.ObservedAt, c.ObservedAt)
	if !r.MeterChanged || r.Summary().Usage != 0 {
		t.Fatal(r)
	}
}

func TestWholeDegreeWarningRequiresRepeatedJumpsAndRejectsFineReadings(t *testing.T) {
	var samples []model.BalanceSample
	start := at("2026-11-01 00:00")
	for i := 0; i < 13; i++ {
		samples = append(samples, model.BalanceSample{ObservedAt: start.Add(time.Duration(i) * 30 * time.Minute), Balance: 20 - float64(i/3), MeterKey: "a"})
	}
	r := Analyze(samples, start, start.Add(6*time.Hour))
	if !r.WholeDegreeSteps() {
		t.Fatal("repeated integer steps not recognized")
	}
	samples[1].Balance -= 0.01
	r = Analyze(samples, start, start.Add(6*time.Hour))
	if r.WholeDegreeSteps() {
		t.Fatal("fine readings were mislabelled as integer steps")
	}
}
