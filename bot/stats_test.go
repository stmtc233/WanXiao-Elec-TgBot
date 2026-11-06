package bot

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wanxiao-elec-bot/history"
	"wanxiao-elec-bot/model"
	"wanxiao-elec-bot/wanxiao"

	"github.com/glebarez/sqlite"
	"gopkg.in/telebot.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func statsDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "bot.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMonitorSamplesDisabledAlertsAndDeduplicatesSharedAccounts(t *testing.T) {
	db := statsDB(t)
	for _, id := range []int64{100, 200} {
		if err := db.Create(&model.User{ID: id}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.User{}).Where("id = ?", id).Update("notify_enabled", false).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.Binding{UserID: id, Account: "same-student", CustomerCode: "1399"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	client := &wanxiao.Client{HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		inner, _ := json.Marshal(map[string]interface{}{"roomfullname": "测试宿舍", "roomverify": "room-a", "detaillist": []map[string]string{{"odd": "23.45", "businesstype": "electric"}}})
		outer, _ := json.Marshal(map[string]interface{}{"code_": 0, "body": string(inner)})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(outer))), Header: make(http.Header)}, nil
	})}}
	bot := &Bot{DB: db, Client: client, sessions: make(map[int64]*session)} // No Telegram connection: any alert would panic.
	bot.CheckLowBalance()
	if calls.Load() != 1 {
		t.Fatal("shared accounts queried more than once", calls.Load())
	}
	var samples []model.BalanceSample
	if err := db.Find(&samples).Error; err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 || samples[0].BindingID == samples[1].BindingID || !samples[0].ObservedAt.Equal(samples[1].ObservedAt) {
		t.Fatal(samples)
	}
	for _, s := range samples {
		if s.Balance != 23.45 || s.Source != "monitor" {
			t.Fatal(s)
		}
	}
}

func TestDateRangesUseLocalCalendarAndRejectInvalidDates(t *testing.T) {
	now := time.Date(2026, 11, 5, 15, 12, 0, 0, displayLoc)
	for _, tc := range []struct{ input, expected string }{
		{"2026-11-01", "20261101-20261101"}, {"2026-11-01 2026-11-05", "20261101-20261105"},
	} {
		p, err := parseStatsDate(tc.input, now)
		if err != nil || p != tc.expected {
			t.Fatal(p, err)
		}
	}
	for _, input := range []string{"2026-11-06", "2026-11-03 2026-11-01", "2026-09-01 2026-11-01", "2026-02-30", "2026-11-01 2026-11-02 extra"} {
		if _, err := parseStatsDate(input, now); err == nil {
			t.Fatal("accepted invalid range", input)
		}
	}
	start, end, err := statsRange("20261101-20261101", now)
	if err != nil || start.Hour() != 0 || end.Sub(start) != 24*time.Hour || start.Location() != displayLoc {
		t.Fatal(start, end, err)
	}
	rStart, rEnd, err := statsRange("24h", now)
	if err != nil || len(history.Analyze(nil, rStart, rEnd).Hours) != 24 {
		t.Fatal(rStart, rEnd, err)
	}
}

func TestCSVPreservesMissingValuesAndEscapesFormulaText(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, displayLoc)
	r := history.Analyze([]model.BalanceSample{{ObservedAt: start, Balance: 10, RoomName: "=FORMULA()", MeterKey: "a"}, {ObservedAt: start.Add(30 * time.Minute), Balance: 10, MeterKey: "a"}, {ObservedAt: start.Add(time.Hour), Balance: 10, MeterKey: "a"}}, start, start.Add(2*time.Hour))
	data, err := hourlyCSV(r)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\xef\xbb\xbf"))).ReadAll()
	if err != nil || len(rows) != 3 || rows[1][2] != "0.0000" || rows[2][2] != "" || rows[2][4] != "无数据" {
		t.Fatal(rows, err)
	}
	data, err = rawCSV(r)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\xef\xbb\xbf"))).ReadAll()
	if err != nil || rows[1][1] != "'=FORMULA()" {
		t.Fatal(rows, err)
	}
}

type statsContext struct {
	telebot.Context
	userID   int64
	data     string
	response string
	message  *telebot.Message
	sent     []interface{}
	calls    []statsCall
}

type statsCall struct {
	method string
	what   interface{}
	opts   []interface{}
}

func (c *statsContext) Sender() *telebot.User { return &telebot.User{ID: c.userID} }
func (c *statsContext) Callback() *telebot.Callback {
	return &telebot.Callback{Data: c.data, Message: c.message}
}
func (c *statsContext) Respond(r ...*telebot.CallbackResponse) error {
	if len(r) > 0 {
		c.response = r[0].Text
	}
	return nil
}
func (c *statsContext) EditOrSend(what interface{}, opts ...interface{}) error {
	c.sent = append(c.sent, what)
	c.calls = append(c.calls, statsCall{"editOrSend", what, opts})
	return nil
}
func (c *statsContext) Send(what interface{}, opts ...interface{}) error {
	c.sent = append(c.sent, what)
	c.calls = append(c.calls, statsCall{"send", what, opts})
	return nil
}
func (c *statsContext) EditCaption(caption string, opts ...interface{}) error {
	c.sent = append(c.sent, caption)
	c.calls = append(c.calls, statsCall{"editCaption", caption, opts})
	return nil
}

func TestStatsCallbacksCannotAccessAnotherUsersBinding(t *testing.T) {
	db := statsDB(t)
	b := model.Binding{UserID: 100, Account: "private", CustomerCode: "1399"}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	bot := &Bot{DB: db}
	for _, view := range []string{"hours", "daily", "more-hours", "less-hours", "more-daily", "less-daily", "details", "csv", "summary"} {
		c := &statsContext{userID: 200, data: strconvID(b.ID) + ":24h:" + view}
		if err := bot.handleStatsCallback(c); err != nil {
			t.Fatal(err)
		}
		if len(c.sent) != 0 || c.response == "" {
			t.Fatal("foreign binding accessed", view)
		}
	}
}

func strconvID(id uint) string { return fmt.Sprint(id) }

func TestRemovedIntervalCallbacksAreNotRegistered(t *testing.T) {
	bot := &Bot{}
	handlers := bot.callbackHandlers()
	if handlers["set_inter"] != nil || handlers["inter_val"] != nil {
		t.Fatal("user-configurable interval endpoint remains")
	}
}

func TestStatsDashboardChartExportAndUnbind(t *testing.T) {
	db := statsDB(t)
	now := time.Now().UTC()
	b := model.Binding{UserID: 100, Account: "own", CustomerCode: "1399", RoomName: "测试宿舍", LastBalance: 20, LastCheck: now}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := db.Create(&model.BalanceSample{BindingID: b.ID, ObservedAt: now.Add(time.Duration(i-4) * 30 * time.Minute), Balance: float64(24 - i), MeterKey: "a", RoomName: "测试宿舍"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	bot := &Bot{DB: db, sessions: make(map[int64]*session)}
	c := &statsContext{userID: 100, data: strconvID(b.ID) + ":today:hours"}
	if err := bot.handleStatsCallback(c); err != nil {
		t.Fatal(err)
	}
	if len(c.sent) != 1 || c.calls[0].method != "editOrSend" {
		t.Fatal("expected one chart replacement", c.calls)
	}
	if _, ok := c.sent[0].(*telebot.Photo); !ok {
		t.Fatal("dashboard did not include chart")
	}
	c = &statsContext{userID: 100, data: strconvID(b.ID) + ":24h:csv"}
	if err := bot.handleStatsCallback(c); err != nil {
		t.Fatal(err)
	}
	if len(c.sent) != 3 {
		t.Fatal("expected raw, hourly and daily documents")
	}
	for _, sent := range c.sent {
		doc, ok := sent.(*telebot.Document)
		if !ok || !strings.HasSuffix(doc.FileName, ".csv") {
			t.Fatal(sent)
		}
	}
	c = &statsContext{userID: 100, data: strconvID(b.ID)}
	if err := bot.handleUnbindConfirmCallback(c); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&model.BalanceSample{}).Where("binding_id = ?", b.ID).Count(&count)
	if count != 0 {
		t.Fatal("unbinding left private history behind", count)
	}
}

func TestCoarseDashboardExplainsPrecisionInOnePhoto(t *testing.T) {
	db := statsDB(t)
	end := time.Now().UTC().Truncate(time.Hour)
	start := end.Add(-14 * time.Hour)
	b := model.Binding{UserID: 100, Account: "coarse", CustomerCode: "1399", RoomName: "测试宿舍", LastBalance: 11, LastCheck: end}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 28; i++ {
		if err := db.Create(&model.BalanceSample{BindingID: b.ID, ObservedAt: start.Add(time.Duration(i) * 30 * time.Minute), Balance: 20 - float64(i/3), MeterKey: "a", RoomName: b.RoomName}).Error; err != nil {
			t.Fatal(err)
		}
	}
	bot := &Bot{DB: db, sessions: make(map[int64]*session)}
	c := &statsContext{userID: 100, data: strconvID(b.ID) + ":24h:summary"}
	if err := bot.handleStatsCallback(c); err != nil {
		t.Fatal(err)
	}
	if len(c.sent) != 1 || c.calls[0].method != "editOrSend" {
		t.Fatal("dashboard sent extra messages", c.calls)
	}
	photo, ok := c.sent[0].(*telebot.Photo)
	if !ok || !strings.Contains(photo.Caption, "整度阶梯") || !strings.Contains(photo.Caption, "不精确") || strings.Contains(photo.Caption, "记录下降最多") {
		t.Fatal("coarse observations were presented as precise hourly patterns", c.sent)
	}
}
