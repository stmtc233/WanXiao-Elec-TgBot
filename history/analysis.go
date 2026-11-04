package history

import (
	"math"
	"time"

	"wanxiao-elec-bot/model"
)

// One missed scheduled observation leaves a gap, rather than assigning an
// unknown hour's consumption to an arbitrary hour. Small scheduling jitter is OK.
const MaxSampleGap = model.CheckInterval + 5*time.Minute

type Hour struct {
	Start    time.Time
	End      time.Time
	Usage    float64
	Covered  time.Duration
	Increase bool
	Gap      bool
	Partial  bool
}

func (h Hour) Complete() bool {
	return !h.Increase && !h.Gap && !h.Partial && h.Covered >= time.Hour-time.Minute
}

func (h Hour) Status() string {
	switch {
	case h.Increase:
		return "疑似充值/调整"
	case h.Gap:
		return "采样中断"
	case h.Covered == 0:
		return "无数据"
	case h.Partial:
		return "未满一小时"
	case !h.Complete():
		return "数据不完整"
	default:
		return "完整"
	}
}

type Increase struct {
	At    time.Time
	Delta float64
}

type Report struct {
	Start      time.Time
	End        time.Time
	Hours      []Hour
	Samples    []model.BalanceSample
	RawSamples []model.BalanceSample
	Latest     *model.BalanceSample
	Increases  []Increase
}

// Analyze expects samples in chronological order (as returned by Load). Only
// the meter present at the end of the requested range contributes to the report.
// Consumption is the observed decrease, apportioned by overlap in time. Neither
// increases nor gaps contribute fabricated zero/negative consumption.
func Analyze(samples []model.BalanceSample, start, end time.Time) Report {
	r := Report{Start: start, End: end}
	if !start.Before(end) {
		return r
	}
	for t := start.Truncate(time.Hour); t.Before(end); t = t.Add(time.Hour) {
		a, b := maxTime(t, start), minTime(t.Add(time.Hour), end)
		r.Hours = append(r.Hours, Hour{Start: a, End: b, Partial: b.Sub(a) < time.Hour})
	}
	for i := range samples {
		s := samples[i]
		if !s.ObservedAt.After(end) && valid(s) {
			copy := s
			r.Latest = &copy
		}
	}
	if r.Latest == nil {
		return r
	}
	key := r.Latest.MeterKey
	for _, s := range samples {
		if valid(s) && !s.ObservedAt.Before(start) && !s.ObservedAt.After(end) {
			r.RawSamples = append(r.RawSamples, s)
			if s.MeterKey == key {
				r.Samples = append(r.Samples, s)
			}
		}
	}
	for i := 1; i < len(samples); i++ {
		a, b := samples[i-1], samples[i]
		if !valid(a) || !valid(b) || a.MeterKey != key || b.MeterKey != key || !a.ObservedAt.Before(b.ObservedAt) {
			continue
		}
		from, to := maxTime(start, a.ObservedAt), minTime(end, b.ObservedAt)
		if !from.Before(to) {
			continue
		}
		gap := b.ObservedAt.Sub(a.ObservedAt) > MaxSampleGap
		delta := a.Balance - b.Balance
		increase := delta < -1e-6
		if increase && !b.ObservedAt.After(end) {
			r.Increases = append(r.Increases, Increase{At: b.ObservedAt, Delta: -delta})
		}
		for j := range r.Hours {
			h := &r.Hours[j]
			x, y := maxTime(h.Start, from), minTime(h.End, to)
			if !x.Before(y) {
				continue
			}
			h.Gap = h.Gap || gap
			h.Increase = h.Increase || increase
			if gap || increase {
				continue
			}
			h.Covered += y.Sub(x)
			h.Usage += math.Max(0, delta) * float64(y.Sub(x)) / float64(b.ObservedAt.Sub(a.ObservedAt))
		}
	}
	return r
}

func valid(s model.BalanceSample) bool {
	return !s.ObservedAt.IsZero() && !math.IsNaN(s.Balance) && !math.IsInf(s.Balance, 0)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

type Summary struct {
	Usage         float64
	CompleteHours int
	Covered       time.Duration
	Peak          *Hour
	DayUsage      float64
	NightUsage    float64
	Profile       [24]float64
	ProfileCount  [24]int
}

// Only complete hours are compared or used for forecasts. Partial observations
// remain visible/exportable without being compared to a whole hour.
func (r Report) Summary() Summary {
	var s Summary
	for i := range r.Hours {
		h := r.Hours[i]
		s.Usage += h.Usage
		s.Covered += h.Covered
		if !h.Complete() {
			continue
		}
		s.CompleteHours++
		if s.Peak == nil || h.Usage > s.Peak.Usage {
			copy := h
			s.Peak = &copy
		}
		hour := h.Start.Hour()
		s.Profile[hour] += h.Usage
		s.ProfileCount[hour]++
		if hour >= 8 && hour < 22 {
			s.DayUsage += h.Usage
		} else {
			s.NightUsage += h.Usage
		}
	}
	for i, count := range s.ProfileCount {
		if count > 0 {
			s.Profile[i] /= float64(count)
		}
	}
	return s
}
