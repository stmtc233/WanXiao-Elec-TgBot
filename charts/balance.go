package charts

import (
	"bytes"
	"fmt"
	"image/color"
	"image/png"
	"math"
	"time"

	"wanxiao-elec-bot/history"
)

// Zoom to the observed balances so a small decline around a large remaining
// balance stays visible. The labelled ticks make the nonzero origin explicit.
func balanceBounds(low, high float64) (float64, float64, float64) {
	padding := math.Max(0.1, (high-low)*0.12)
	rough := (high - low + padding*2) / 5
	base := math.Pow(10, math.Floor(math.Log10(rough)))
	step := base
	for _, factor := range []float64{1, 2, 2.5, 5, 10} {
		if factor*base >= rough {
			step = factor * base
			break
		}
	}
	return math.Floor((low-padding)/step) * step, math.Ceil((high+padding)/step) * step, step
}

func renderBalance(r history.Report, room string) ([]byte, error) {
	c, err := newCanvasSize(900, 820)
	if err != nil {
		return nil, err
	}
	defer c.close()
	c.text(50, 78, 36, "余额记录走势", ink)
	c.fitted(50, 117, 24, 800, room, muted)
	if len(r.Samples) > 0 {
		c.text(50, 151, 22, r.Samples[0].ObservedAt.In(r.Start.Location()).Format("01-02 15:04")+" 至 "+r.Samples[len(r.Samples)-1].ObservedAt.In(r.Start.Location()).Format("01-02 15:04")+" · 北京时间", muted)
	}
	metrics := []struct{ label, value string }{{"首次记录", "—"}, {"末次记录", "—"}, {"余额净变化", "—"}}
	if len(r.Samples) > 0 {
		first, last := r.Samples[0], r.Samples[len(r.Samples)-1]
		metrics[0].value = usageText(first.Balance) + " 度"
		metrics[1].value = usageText(last.Balance) + " 度"
		metrics[2].value = fmt.Sprintf("%+.2f 度", last.Balance-first.Balance)
	}
	for i, m := range metrics {
		x := 50 + i*270
		c.rect(x-4, 176, x+251, 264, background)
		c.text(x+10, 205, 21, m.label, muted)
		c.fitted(x+10, 249, 35, 228, m.value, ink)
	}
	c.rect(46, 282, 854, 351, background)
	if r.WholeDegreeSteps() {
		c.text(60, 311, 24, "本段余额呈整度跳变，无法精确定位真实用电时段。", ink)
	} else {
		c.text(60, 311, 24, "阶梯只表示余额读数，竖跳为记录到新余额的时刻。", ink)
	}
	c.text(60, 340, 22, "纵轴按本段余额放大；缺采处不连线。", muted)
	if len(r.Samples) < 2 {
		c.text(135, 470, 30, "需要至少两次余额记录才能查看走势", ink)
	} else {
		const x0, x1, y0, y1 = 125, 846, 388, 639
		low, high := r.Samples[0].Balance, r.Samples[0].Balance
		for _, s := range r.Samples {
			low = math.Min(low, s.Balance)
			high = math.Max(high, s.Balance)
		}
		low, high, step := balanceBounds(low, high)
		first, last := r.Samples[0].ObservedAt, r.Samples[len(r.Samples)-1].ObservedAt
		if !first.Before(last) {
			last = first.Add(time.Minute)
		}
		point := func(at time.Time, value float64) (int, int) {
			return x0 + int(float64(at.Sub(first))/float64(last.Sub(first))*(x1-x0)), y1 - int((value-low)/(high-low)*(y1-y0))
		}
		for i, s := range r.Samples {
			if i == 0 {
				continue
			}
			prev := r.Samples[i-1]
			if s.ObservedAt.Sub(prev.ObservedAt) > history.MaxSampleGap {
				xp, _ := point(prev.ObservedAt, prev.Balance)
				x, _ := point(s.ObservedAt, s.Balance)
				c.rect(xp, y0, x, y1, color.RGBA{255, 244, 226, 255})
				c.text(xp+3, y0+25, 19, "缺采", orange)
			}
		}
		for v := low; v <= high+step*0.01; v += step {
			_, y := point(first, v)
			c.line(x0, y, x1, y, grid, 1)
			precision := 1
			if step < 0.1 {
				precision = 2
			}
			c.rightText(112, y+8, 24, fmt.Sprintf("%.*f", precision, v), muted)
		}
		c.text(65, 370, 21, "度", muted)
		for i, s := range r.Samples {
			x, y := point(s.ObservedAt, s.Balance)
			if i > 0 {
				prev := r.Samples[i-1]
				if s.ObservedAt.Sub(prev.ObservedAt) <= history.MaxSampleGap {
					xp, yp := point(prev.ObservedAt, prev.Balance)
					c.line(xp, yp, x, yp, teal, 3)
					col := teal
					if s.Balance > prev.Balance {
						col = orange
					}
					c.line(x, yp, x, y, col, 3)
				}
			}
			c.rect(x-3, y-3, x+4, y+4, teal)
		}
		interval := 30 * time.Minute
		if last.Sub(first) > 4*time.Hour {
			interval = time.Hour
		}
		if last.Sub(first) > 8*time.Hour {
			interval = 2 * time.Hour
		}
		if last.Sub(first) > 18*time.Hour {
			interval = 6 * time.Hour
		}
		if last.Sub(first) > 3*24*time.Hour {
			interval = 24 * time.Hour
		}
		if last.Sub(first) > 12*24*time.Hour {
			interval = 7 * 24 * time.Hour
		}
		tick := first.Truncate(interval)
		if tick.Before(first) {
			tick = tick.Add(interval)
		}
		for ; !tick.After(last); tick = tick.Add(interval) {
			x, _ := point(tick, low)
			c.text(x-30, y1+35, 23, tick.In(r.Start.Location()).Format("15:04"), muted)
			if last.Sub(first) > 18*time.Hour {
				c.text(x-30, y1+62, 20, tick.In(r.Start.Location()).Format("01-02"), muted)
			}
		}
	}
	c.text(50, 741, 22, "线条保持上次读数，直到下一次采样；缺采区间留空。", muted)
	c.text(50, 775, 22, "余额下降的记录时刻，不等于实际用电集中发生的时刻。", muted)
	var out bytes.Buffer
	err = png.Encode(&out, c.img)
	return out.Bytes(), err
}
