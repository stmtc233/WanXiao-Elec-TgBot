// Package charts renders reports locally with embedded fonts; no browser,
// external chart service, Python, or system font installation is required.
package charts

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"sync"
	"time"

	"wanxiao-elec-bot/history"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed fonts/NotoSansSC-Regular.ttf
var fontBytes []byte

//go:embed fonts/OFL.txt
var fontLicense string

var parsedFont = sync.OnceValues(func() (*opentype.Font, error) {
	if fontLicense == "" {
		return nil, fmt.Errorf("missing embedded font license")
	}
	return opentype.Parse(fontBytes)
})

var (
	background = color.RGBA{244, 247, 251, 255}
	ink        = color.RGBA{28, 44, 66, 255}
	muted      = color.RGBA{105, 120, 139, 255}
	grid       = color.RGBA{224, 231, 239, 255}
	teal       = color.RGBA{14, 146, 144, 255}
	orange     = color.RGBA{219, 141, 39, 255}
	missing    = color.RGBA{226, 231, 237, 255}
)

type canvas struct {
	img   *image.RGBA
	faces map[int]font.Face
	font  *opentype.Font
}

func newCanvas(height int) (*canvas, error) {
	return newCanvasSize(1240, height)
}

func newCanvasSize(width, height int) (*canvas, error) {
	f, err := parsedFont()
	if err != nil {
		return nil, err
	}
	c := &canvas{img: image.NewRGBA(image.Rect(0, 0, width, height)), faces: make(map[int]font.Face), font: f}
	c.rect(0, 0, width, height, background)
	c.rect(24, 24, width-24, height-24, color.White)
	return c, nil
}

func (c *canvas) close() {
	for _, f := range c.faces {
		_ = f.Close()
	}
}
func (c *canvas) face(size int) font.Face {
	if f := c.faces[size]; f != nil {
		return f
	}
	f, err := opentype.NewFace(c.font, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	} // valid embedded font + fixed sizes only
	c.faces[size] = f
	return f
}

func (c *canvas) text(x, y, size int, s string, col color.Color) {
	d := font.Drawer{Dst: c.img, Src: image.NewUniform(col), Face: c.face(size), Dot: fixed.P(x, y)}
	d.DrawString(s)
}

func (c *canvas) fitted(x, y, size, width int, s string, col color.Color) {
	f := c.face(size)
	if font.MeasureString(f, s).Ceil() <= width {
		c.text(x, y, size, s, col)
		return
	}
	runes := []rune(s)
	for len(runes) > 0 && font.MeasureString(f, string(runes)+"…").Ceil() > width {
		runes = runes[:len(runes)-1]
	}
	c.text(x, y, size, string(runes)+"…", col)
}

func (c *canvas) rect(x0, y0, x1, y1 int, col color.Color) {
	draw.Draw(c.img, image.Rect(x0, y0, x1, y1), image.NewUniform(col), image.Point{}, draw.Src)
}

func (c *canvas) line(x0, y0, x1, y1 int, col color.Color, width int) {
	dx, dy := x1-x0, y1-y0
	n := max(abs(dx), abs(dy))
	if n == 0 {
		c.rect(x0-width/2, y0-width/2, x0+width/2+1, y0+width/2+1, col)
		return
	}
	for i := 0; i <= n; i++ {
		x, y := x0+dx*i/n, y0+dy*i/n
		c.rect(x-width/2, y-width/2, x+width/2+1, y+width/2+1, col)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (c *canvas) header(title, room string, r history.Report) {
	c.text(60, 77, 30, title, ink)
	c.fitted(60, 114, 21, 1120, room, muted)
	c.text(60, 146, 18, r.Start.Format("2006-01-02 15:04")+" 至 "+r.End.Format("2006-01-02 15:04")+" · 北京时间", muted)
}

func (c *canvas) footer(lines ...string) {
	y := c.img.Bounds().Dy() - 52 - (len(lines)-1)*28
	for _, s := range lines {
		c.text(60, y, 18, s, muted)
		y += 28
	}
}

// Render supports hourly and daily consumption, plus secondary analysis views.
func Render(r history.Report, view, room string) ([]byte, error) {
	if view == "balance" {
		return renderBalance(r, room)
	}
	if view == "hours" {
		return renderHours(r, room)
	}
	if view == "daily" {
		return renderDaily(r, room)
	}
	height := 720
	if view == "heatmap" {
		height = 330 + len(days(r))*34
	}
	c, err := newCanvas(height)
	if err != nil {
		return nil, err
	}
	defer c.close()
	switch view {
	case "profile":
		c.header("各时段平均耗电", room, r)
		c.profile(r)
	case "heatmap":
		c.header("小时耗电热力图", room, r)
		c.heatmap(r)
	default:
		return nil, fmt.Errorf("未知图表类型")
	}
	var out bytes.Buffer
	err = png.Encode(&out, c.img)
	return out.Bytes(), err
}

func (c *canvas) axes(maxValue float64) (int, int, int, int) {
	return c.axesRange(0, maxValue)
}

func (c *canvas) axesRange(minValue, maxValue float64) (int, int, int, int) {
	x0, y0, x1, y1 := 104, 205, 1180, 542
	for i := 0; i <= 4; i++ {
		y := y1 - (y1-y0)*i/4
		c.line(x0, y, x1, y, grid, 1)
		precision := 1
		if maxValue-minValue < 1 {
			precision = 2
		}
		if maxValue-minValue < 0.1 {
			precision = 3
		}
		if maxValue-minValue >= 1000 {
			precision = 0
		}
		c.text(45, y+6, 17, fmt.Sprintf("%.*f", precision, minValue+(maxValue-minValue)*float64(i)/4), muted)
	}
	c.text(62, 183, 18, "度", muted)
	return x0, y0, x1, y1
}

func scale(v float64) float64 {
	if v <= 0 {
		return 1
	}
	return v * 1.2
}

func (c *canvas) profile(r history.Report) {
	s := r.Summary()
	var largest float64
	for _, v := range s.Profile {
		largest = math.Max(largest, v)
	}
	maximum := scale(largest)
	x0, y0, x1, y1 := c.axes(maximum)
	step := float64(x1-x0) / 24
	for i, v := range s.Profile {
		x, w := x0+int(float64(i)*step)+3, int(step)-6
		if s.ProfileCount[i] == 0 {
			c.rect(x, y0, x+w, y1, missing)
		} else {
			y := y1 - int(v/maximum*float64(y1-y0))
			c.rect(x, y, x+w, y1, teal)
			if v == 0 {
				c.line(x, y1-2, x+w, y1-2, teal, 3)
			}
			c.text(x, y-8, 15, fmt.Sprintf("%.2f", v), ink)
		}
		if i%2 == 0 {
			c.text(x, y1+28, 17, fmt.Sprintf("%02d", i), muted)
		}
		c.text(x, y1+55, 14, fmt.Sprintf("%d次", s.ProfileCount[i]), muted)
	}
	c.footer("只按完整小时计算各时段的平均值；下方次数为有效小时数量。", "灰色：该时段没有完整数据；按余额下降估算。")
}

func days(r history.Report) []time.Time {
	var out []time.Time
	for d := time.Date(r.Start.Year(), r.Start.Month(), r.Start.Day(), 0, 0, 0, 0, r.Start.Location()); d.Before(r.End); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

func heatColor(v, maxValue float64) color.RGBA {
	t := 0.0
	if maxValue > 0 {
		t = math.Sqrt(math.Max(0, math.Min(1, v/maxValue)))
	}
	return color.RGBA{uint8(227 - 213*t), uint8(245 - 99*t), uint8(243 - 99*t), 255}
}

func (c *canvas) heatmap(r history.Report) {
	dates := days(r)
	var largest float64
	lookup := make(map[string]history.Hour)
	for _, h := range r.Hours {
		largest = math.Max(largest, h.Usage)
		lookup[h.Start.Format("2006-01-02 15")] = h
	}
	for hour := 0; hour < 24; hour++ {
		c.text(153+hour*42, 185, 16, fmt.Sprintf("%02d", hour), muted)
	}
	for row, d := range dates {
		y := 202 + row*34
		c.text(58, y+22, 18, d.Format("01-02"), ink)
		for hour := 0; hour < 24; hour++ {
			x := 146 + hour*42
			h, ok := lookup[d.Add(time.Duration(hour)*time.Hour).Format("2006-01-02 15")]
			if !ok || h.Covered == 0 {
				c.rect(x, y, x+39, y+30, missing)
				if h.Gap || h.Increase {
					c.line(x, y+29, x+38, y+29, orange, 3)
				}
				continue
			}
			c.rect(x, y, x+39, y+30, heatColor(h.Usage, largest))
			if !h.Complete() {
				c.line(x, y+29, x+38, y+29, orange, 3)
			}
			col := ink
			if largest > 0 && h.Usage/largest > 0.4 {
				col = color.RGBA{255, 255, 255, 255}
			}
			c.text(x+3, y+21, 13, fmt.Sprintf("%.1f", h.Usage), col)
		}
	}
	c.footer(fmt.Sprintf("单位：度，每格对应一个小时   颜色越深耗电越多   色阶上限 %.2f 度", largest), "灰色：无有效数据   橙色下划线：不完整   数据基于余额下降估算。")
}
