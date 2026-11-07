package charts

import (
	"bytes"
	"fmt"
	"image/color"
	"image/png"
	"math"
	"strings"
	"time"

	"wanxiao-elec-bot/history"

	"golang.org/x/image/font"
)

// RenderPages returns one chart per request so changing a date does not flood
// the conversation. Twelve-column panels retain readable values on a phone.
func RenderPages(r history.Report, view, room string) ([][]byte, error) {
	data, err := Render(r, view, room)
	if err != nil {
		return nil, err
	}
	return [][]byte{data}, nil
}

func usageText(value float64) string {
	if value == 0 {
		return "0"
	}
	if value > 0 && value < 0.005 {
		return "<0.01"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", value), "0"), ".")
}

func (c *canvas) rightText(right, y, size int, text string, col color.Color) {
	c.text(right-font.MeasureString(c.face(size), text).Ceil(), y, size, text, col)
}

func (c *canvas) centered(x, y, size int, text string, col color.Color) {
	c.text(x-font.MeasureString(c.face(size), text).Ceil()/2, y, size, text, col)
}

type usageColumn struct {
	label, sublabel string
	usage           float64
	covered         time.Duration
	complete        bool
	future          bool
}

type usagePanel struct {
	title   string
	columns []usageColumn
}

// Hour slots remain fixed across dates, including missing data and hours that
// have not arrived yet. Neither kind is drawn as a consumption bar.
func hourPanels(r history.Report) []usagePanel {
	if r.End.Before(r.Start) {
		return nil
	}
	end := r.End
	midnight := time.Date(r.Start.Year(), r.Start.Month(), r.Start.Day(), 0, 0, 0, 0, r.Start.Location())
	if r.Start.Equal(midnight) && !r.End.After(midnight.AddDate(0, 0, 1)) {
		end = midnight.AddDate(0, 0, 1)
	}
	lookup := make(map[int64]history.Hour, len(r.Hours))
	for _, h := range r.Hours {
		lookup[h.Start.Truncate(time.Hour).Unix()] = h
	}
	var panels []usagePanel
	for at := r.Start.Truncate(time.Hour); at.Before(end); at = at.Add(time.Hour) {
		if len(panels) == 0 || len(panels[len(panels)-1].columns) == 12 {
			panels = append(panels, usagePanel{title: at.Format("01-02") + " · " + at.Format("15:04")})
		}
		h := lookup[at.Unix()]
		col := usageColumn{label: at.Format("15"), usage: h.Usage, covered: h.Covered, complete: h.Complete(), future: !at.Before(r.End)}
		panels[len(panels)-1].columns = append(panels[len(panels)-1].columns, col)
	}
	return panels
}

func renderHours(r history.Report, room string) ([]byte, error) {
	if len(r.Hours) > 48 {
		return nil, fmt.Errorf("请选择一天查看每小时用电")
	}
	panels := hourPanels(r)
	if len(panels) > 4 {
		return nil, fmt.Errorf("请选择一天查看每小时用电")
	}
	return renderUsage(r, room, "每小时耗电", panels, false)
}

func renderDaily(r history.Report, room string) ([]byte, error) {
	days := r.Days()
	if len(days) > 31 {
		return nil, fmt.Errorf("每天耗电最多显示31天")
	}
	var panels []usagePanel
	for i, d := range days {
		if i%12 == 0 {
			panels = append(panels, usagePanel{title: d.Start.Format("01-02")})
		}
		panels[len(panels)-1].columns = append(panels[len(panels)-1].columns, usageColumn{
			label: d.Start.Format("02"), sublabel: weekday(d.Start), usage: d.Usage,
			covered: d.Covered, complete: d.Complete(),
		})
	}
	for i := range panels {
		last := min((i+1)*12, len(days)) - 1
		panels[i].title += " 至 " + days[last].Start.Format("01-02")
	}
	return renderUsage(r, room, "每天耗电", panels, true)
}

func weekday(at time.Time) string {
	return [...]string{"日", "一", "二", "三", "四", "五", "六"}[at.Weekday()]
}

func renderUsage(r history.Report, room, title string, panels []usagePanel, daily bool) ([]byte, error) {
	panelHeight := 260
	if len(panels) > 2 {
		panelHeight = 220
	}
	if len(panels) > 3 {
		panelHeight = 200
	}
	if len(panels) == 1 {
		panelHeight = 330
	}
	if len(panels) == 0 {
		panels = []usagePanel{{}}
		panelHeight = 330
	}
	height := 360 + len(panels)*panelHeight + 100
	c, err := newCanvasSize(900, height)
	if err != nil {
		return nil, err
	}
	defer c.close()
	c.text(50, 78, 36, title, ink)
	c.fitted(50, 120, 24, 800, room, muted)
	dateLabel := r.Start.Format("2006.01.02")
	if daily {
		last := r.End.Add(-time.Nanosecond)
		if last.Before(r.Start) {
			last = r.Start
		}
		dateLabel = r.Start.Format("01.02") + " — " + last.Format("01.02")
	}
	c.rightText(850, 78, 23, dateLabel, muted)

	s := r.Summary()
	c.text(50, 175, 22, "估算用电", muted)
	total := "—"
	if s.Covered > 0 {
		total = usageText(s.Usage)
	}
	c.fitted(48, 247, 64, 365, total, ink)
	totalWidth := font.MeasureString(c.face(64), total).Ceil()
	c.text(58+min(totalWidth, 355), 244, 24, "度", muted)

	if !daily {
		c.text(495, 175, 21, "有效记录", muted)
		c.text(495, 223, 29, fmt.Sprintf("%.1f 小时", s.Covered.Hours()), ink)
	}
	if daily {
		var sum float64
		var count int
		for _, d := range r.Days() {
			if d.Complete() {
				sum += d.Usage
				count++
			}
		}
		c.text(495, 175, 21, "完整日均", muted)
		value := "—"
		if count > 0 {
			value = usageText(sum/float64(count)) + " 度"
		}
		c.fitted(495, 223, 29, 185, value, ink)
	}
	c.text(710, 175, 21, "记录余额", muted)
	balance := "—"
	if r.Latest != nil {
		balance = usageText(r.Latest.Balance)
	}
	c.fitted(710, 223, 29, 140, balance, ink)
	c.line(50, 275, 850, 275, grid, 1)
	legend := []struct {
		x     int
		col   color.Color
		label string
	}{{50, teal, "完整"}, {180, orange, "部分记录"}, {355, missing, "— 无数据"}}
	for _, l := range legend {
		c.rect(l.x, 303, l.x+13, 316, l.col)
		c.text(l.x+23, 318, 21, l.label, muted)
	}
	c.rightText(850, 318, 20, "单位：度 · 北京时间", muted)

	largest := 0.0
	for _, p := range panels {
		for _, col := range p.columns {
			if col.covered > 0 {
				largest = math.Max(largest, col.usage)
			}
		}
	}
	maximum := usageMaximum(largest)
	for i, p := range panels {
		c.usagePanel(p, 352+i*panelHeight, panelHeight, maximum, daily)
	}
	footerY := height - 75
	note := "按余额下降估算；0 表示余额未下降。"
	if r.WholeDegreeSteps() {
		note = "余额呈整度阶梯，无法精确定位实际用电时段。"
	}
	if s.Covered == 0 {
		note = "暂无可比较的余额记录，请稍后查看。"
	}
	c.text(50, footerY, 22, note, muted)
	c.text(50, footerY+32, 21, "橙色含未结束或缺失时段；缺采与余额上涨不计入耗电。", muted)
	var out bytes.Buffer
	err = png.Encode(&out, c.img)
	return out.Bytes(), err
}

// A common, zero-based scale makes all panels directly comparable.
func usageMaximum(largest float64) float64 {
	if largest <= 0 {
		return 1
	}
	rough := largest / 2.7
	base := math.Pow(10, math.Floor(math.Log10(rough)))
	for _, factor := range []float64{1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10} {
		if factor*base >= rough {
			return factor * base * 3
		}
	}
	return largest * 1.2
}

func axisUsageText(value, step float64) string {
	if value == 0 {
		return "0"
	}
	precision := max(0, int(-math.Floor(math.Log10(step))))
	precision = min(6, precision+1)
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.*f", precision, value), "0"), ".")
}

func (c *canvas) usagePanel(p usagePanel, top, height int, maximum float64, daily bool) {
	c.text(50, top+25, 23, p.title, ink)
	const x0, x1 = 112, 850
	y0, y1 := top+50, top+height-45
	if daily {
		y1 -= 20
	}
	for i := 0; i <= 3; i++ {
		y := y1 - (y1-y0)*i/3
		c.line(x0, y, x1, y, grid, 1)
		c.rightText(95, y+7, 20, axisUsageText(maximum*float64(i)/3, maximum/3), muted)
	}
	if len(p.columns) == 0 {
		c.centered(480, (y0+y1)/2, 28, "暂无用电记录", muted)
		return
	}
	// Always reserve twelve columns for hour panels; the same hour has the
	// same position even when later hours are not available yet.
	count := len(p.columns)
	if !daily {
		count = 12
	}
	step := float64(x1-x0) / float64(count)
	barWidth := min(52, int(step*0.58))
	for i, col := range p.columns {
		x := x0 + int((float64(i)+0.5)*step)
		c.centered(x, y1+31, 23, col.label, ink)
		if daily {
			c.centered(x, y1+57, 18, col.sublabel, muted)
		}
		if col.future {
			c.centered(x, y1-12, 18, "未到", muted)
			continue
		}
		if col.covered <= 0 {
			c.centered(x, y1-12, 24, "—", muted)
			continue
		}
		colr := teal
		if !col.complete {
			colr = orange
		}
		y := y1 - int(col.usage/maximum*float64(y1-y0))
		if col.usage > 0 {
			c.rect(x-barWidth/2, min(y, y1-3), x+(barWidth+1)/2, y1, colr)
		} else {
			c.line(x-barWidth/2, y1-2, x+barWidth/2, y1-2, colr, 2)
		}
		size := 22
		if daily && count <= 7 {
			size = 27
		}
		c.centered(x, y-12, size, usageText(col.usage), colr)
	}
}
