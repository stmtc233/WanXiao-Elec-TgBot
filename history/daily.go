package history

import "time"

// Day groups the report's estimated hourly consumption by calendar date in the
// report's timezone. Start and End are clipped to the selected report range;
// Partial distinguishes an unfinished/selected portion from a full day.
type Day struct {
	Start    time.Time
	End      time.Time
	Usage    float64
	Covered  time.Duration
	Increase bool
	Gap      bool
	Partial  bool
}

// Complete days alone are suitable for daily averages and comparisons. As with
// Hour.Complete, a minute of scheduling jitter is tolerated, but unknown time,
// recharge intervals and selected portions of a day never become full days.
func (d Day) Complete() bool {
	return d.Start.Before(d.End) && !d.Partial && !d.Increase && !d.Gap && d.Covered >= d.End.Sub(d.Start)-time.Minute
}

func (d Day) Status() string {
	switch {
	case d.Increase:
		return "疑似充值/调整"
	case d.Gap:
		return "采样中断"
	case d.Covered == 0:
		return "无数据"
	case d.Partial:
		return "未满一天"
	case !d.Complete():
		return "数据不完整"
	default:
		return "完整"
	}
}

// Days preserves empty dates so that missing observations are not mistaken for
// zero consumption. It only sums the conservative hourly estimates from
// Analyze: excluded gaps and balance increases are never added back in.
func (r Report) Days() []Day {
	if !r.Start.Before(r.End) {
		return nil
	}
	loc := r.Start.Location()
	date := time.Date(r.Start.Year(), r.Start.Month(), r.Start.Day(), 0, 0, 0, 0, loc)
	var out []Day
	for date.Before(r.End) {
		next := date.AddDate(0, 0, 1)
		start, end := maxTime(date, r.Start), minTime(next, r.End)
		d := Day{Start: start, End: end, Partial: start.After(date) || end.Before(next)}
		for _, h := range r.Hours {
			from, to := maxTime(start, h.Start), minTime(end, h.End)
			if !from.Before(to) || !h.Start.Before(h.End) {
				continue
			}
			// Normally hours meet at local midnight. Proportional overlap also
			// handles reports whose timezone has a nonintegral UTC offset.
			fraction := float64(to.Sub(from)) / float64(h.End.Sub(h.Start))
			d.Usage += h.Usage * fraction
			d.Covered += time.Duration(float64(h.Covered) * fraction)
			d.Increase = d.Increase || h.Increase
			d.Gap = d.Gap || h.Gap
		}
		out = append(out, d)
		date = next
	}
	return out
}
