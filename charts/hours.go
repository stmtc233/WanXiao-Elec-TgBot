package charts

import (
	"bytes"
	"fmt"
	"image/color"
	"image/png"
	"math"
	"strings"

	"wanxiao-elec-bot/history"

	"golang.org/x/image/font"
)

// RenderPages keeps hour labels legible in Telegram's photo previews. Each
// page contains at most 12 hours, and a new date starts on a new page.
func RenderPages(r history.Report, view, room string) ([][]byte, error) {
	if view != "hours" {
		data, err := Render(r, view, room)
		if err != nil {
			return nil, err
		}
		return [][]byte{data}, nil
	}
	if len(r.Hours) > 48 {
		return nil, fmt.Errorf("逐小时图最多显示48小时，请选择日期或热力图")
	}
	hours := visibleHours(r)
	var groups [][]history.Hour
	for _, h := range hours {
		if len(groups) == 0 || len(groups[len(groups)-1]) == 12 || groups[len(groups)-1][0].Start.Format("2006-01-02") != h.Start.Format("2006-01-02") {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], h)
	}
	if len(groups) == 0 {
		groups = append(groups, nil)
	}
	var pages [][]byte
	for i, group := range groups {
		data, err := renderHourPage(r, group, room, i+1, len(groups))
		if err != nil {
			return nil, err
		}
		pages = append(pages, data)
	}
	return pages, nil
}

// Trim only blank edges. Internal gaps, increases, and an isolated observation
// remain visible; a blank edge is explained in the image instead of drawn as
// a full-height grey column that looks like consumption.
func visibleHours(r history.Report) []history.Hour {
	first, last := -1, -1
	for i, h := range r.Hours {
		visible := h.Covered > 0 || h.Gap || h.Increase
		for _, s := range r.Samples {
			if !s.ObservedAt.Before(h.Start) && s.ObservedAt.Before(h.End) {
				visible = true
				break
			}
		}
		if visible {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return nil
	}
	return r.Hours[first : last+1]
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

func renderHourPage(r history.Report, hours []history.Hour, room string, page, count int) ([]byte, error) {
	const width, rowHeight, bodyTop = 900, 52, 430
	bodyHeight := len(hours) * rowHeight
	if len(hours) == 0 {
		bodyHeight = 100
	}
	c, err := newCanvasSize(width, bodyTop+bodyHeight+174)
	if err != nil {
		return nil, err
	}
	defer c.close()
	c.text(50, 78, 36, "小时用电记录", ink)
	c.fitted(50, 117, 24, 800, room, muted)
	c.text(50, 151, 22, r.Start.Format("01-02 15:04")+" 至 "+r.End.Format("01-02 15:04")+" · 所选范围", muted)
	if len(hours) > 0 {
		c.text(50, 185, 24, fmt.Sprintf("%s · %s–%s · 第%d/%d张", hours[0].Start.Format("01-02"), hours[0].Start.Format("15:04"), hours[len(hours)-1].End.Format("15:04"), page, count), ink)
	} else {
		c.text(50, 185, 24, "等待可比较的采样记录 · 北京时间", muted)
	}
	s := r.Summary()
	metrics := []struct{ label, value string }{{"全范围有效区间下降", usageText(s.Usage) + " 度"}, {"有效区间平均", "—"}, {"末次记录余额", "—"}}
	if s.Covered > 0 {
		metrics[1].value = fmt.Sprintf("%.2f 度/时", s.Usage/s.Covered.Hours())
	}
	if s.Covered == 0 {
		metrics[0].value = "—"
	}
	if r.Latest != nil {
		metrics[2].value = usageText(r.Latest.Balance) + " 度"
	}
	for i, m := range metrics {
		x := 50 + i*270
		c.rect(x-4, 207, x+251, 290, background)
		c.text(x+10, 235, 20, m.label, muted)
		c.fitted(x+10, 277, 34, 228, m.value, ink)
	}
	if r.WholeDegreeSteps() {
		c.rect(46, 306, 854, 379, color.RGBA{255, 245, 225, 255})
		c.text(60, 336, 25, "本段余额呈整度阶梯变化，小时分配精度有限。", ink)
		c.text(60, 367, 23, "0只是余额未下降，不能据此判断没有用电。", muted)
	} else {
		c.rect(46, 306, 854, 379, background)
		c.text(60, 336, 24, "按相邻采样的余额下降估算，跨整点按时间分摊。", ink)
		c.text(60, 367, 23, "0表示余额未下降；缺采和上涨区间不计入累计。", muted)
	}
	c.text(52, 411, 21, "时段（北京时间）", muted)
	c.text(238, 411, 21, "余额下降", muted)
	c.rightText(721, 411, 21, "度", muted)
	c.text(759, 411, 21, "采样情况", muted)
	maximum := 0.0
	for _, h := range r.Hours {
		if h.Covered > 0 {
			maximum = math.Max(maximum, h.Usage)
		}
	}
	for i, h := range hours {
		y := bodyTop + i*rowHeight
		if i%2 == 0 {
			c.rect(46, y, 854, y+rowHeight, background)
		}
		c.text(52, y+35, 26, h.Start.Format("15:04")+"–"+h.End.Format("15:04"), ink)
		state := "齐全"
		col := teal
		if !h.Complete() {
			col = orange
			state = "有缺段"
		}
		if h.Partial {
			state = "未满小时"
		}
		if h.Gap {
			state = "缺采"
		}
		if h.Increase {
			state = "含上涨"
		}
		if h.Covered == 0 {
			state = "无记录"
			if h.Gap {
				state = "缺采"
			}
			if h.Increase {
				state = "上涨"
			}
			for j := 0; j < 3; j++ {
				c.rect(238+j*25, y+24, 251+j*25, y+27, missing)
			}
			c.rightText(721, y+37, 32, "—", muted)
		} else {
			c.rect(238, y+20, 577, y+32, grid)
			if h.Usage > 0 && maximum > 0 {
				c.rect(238, y+16, 238+max(3, int(h.Usage/maximum*339)), y+36, col)
			} else {
				c.rect(238, y+20, 243, y+32, muted)
			}
			c.rightText(721, y+37, 34, usageText(h.Usage), col)
		}
		c.fitted(759, y+32, 21, 93, state, muted)
		if h.Covered > 0 && !h.Complete() {
			c.text(759, y+49, 15, fmt.Sprintf("%.0f分钟", h.Covered.Minutes()), muted)
		}
	}
	if len(hours) == 0 {
		c.text(90, 480, 30, "此范围还没有连续采样", ink)
		c.text(90, 519, 24, "每30分钟采样；有相邻余额后即可形成记录。", muted)
	}
	y := bodyTop + bodyHeight + 35
	omitted := len(r.Hours) - len(visibleHours(r))
	if omitted > 0 {
		c.text(50, y, 21, fmt.Sprintf("已收起首尾无记录的%d个小时；CSV保留完整所选范围。", omitted), muted)
	} else {
		c.text(50, y, 21, "每行一个时段；数值表示该时段记录到的余额下降。", muted)
	}
	c.text(50, y+34, 21, "采样齐全仅指覆盖时长，不代表电表按小时更新。", muted)
	c.text(50, y+68, 21, "橙色：有缺段或未满小时；—：没有有效记录。", muted)
	if r.UnallocatedDrop > 0 {
		c.text(50, y+102, 21, fmt.Sprintf("另有%s度下降处于缺采区间，未分配到小时累计。", usageText(r.UnallocatedDrop)), muted)
	} else {
		c.text(50, y+102, 21, "原始读数与完整所选范围可导出CSV核对。", muted)
	}
	var out bytes.Buffer
	err = png.Encode(&out, c.img)
	return out.Bytes(), err
}
