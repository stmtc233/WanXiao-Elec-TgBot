package bot

import (
	"context"
	"fmt"
	"testing"
	"time"

	"wanxiao-elec-bot/model"
	"wanxiao-elec-bot/wanxiao"
)

func TestIsDue(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		ago      time.Duration
		fails    int
		interval int
		want     bool
	}{
		{"never checked", 0, 0, 60, true},
		{"not yet", 30 * time.Minute, 0, 60, false},
		{"due", 61 * time.Minute, 0, 60, true},
		{"legacy 1 minute interval is clamped", 5 * time.Minute, 0, 1, false},
		{"one failure doubles wait", 90 * time.Minute, 1, 60, false},
		{"one failure after backoff", 121 * time.Minute, 1, 60, true},
		{"backoff capped at 6h", 6*time.Hour + time.Minute, 10, 120, true},
		{"cap never shortens interval", 7 * time.Hour, 2, 1440, false},
	}
	for _, tc := range cases {
		b := &model.Binding{FailCount: tc.fails}
		if tc.ago > 0 {
			b.LastAttempt = now.Add(-tc.ago)
		}
		if got := isDue(b, tc.interval, now); got != tc.want {
			t.Errorf("%s: isDue = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestParseAmount(t *testing.T) {
	ok := map[string]float64{"15": 15, " 50元 ": 50, "12.5": 12.5, "1": 1, "500": 500, "0.5e1": 5}
	for in, want := range ok {
		if got, valid := parseAmount(in); !valid || got != want {
			t.Errorf("parseAmount(%q) = %v,%v want %v", in, got, valid, want)
		}
	}
	for _, in := range []string{"", "abc", "0", "-5", "0.5", "501", "1.234", "NaN", "Inf"} {
		if _, valid := parseAmount(in); valid {
			t.Errorf("parseAmount(%q) should be invalid", in)
		}
	}
}

func TestParseThresholdInterval(t *testing.T) {
	if v, ok := parseThreshold("12.345度"); !ok || v != 12.35 {
		t.Errorf("parseThreshold = %v,%v", v, ok)
	}
	for _, in := range []string{"0", "-1", "1001", "x"} {
		if _, ok := parseThreshold(in); ok {
			t.Errorf("parseThreshold(%q) should be invalid", in)
		}
	}
	if v, ok := parseInterval("60分钟"); !ok || v != 60 {
		t.Errorf("parseInterval = %v,%v", v, ok)
	}
	for _, in := range []string{"1", "9", "1441", "1.5"} {
		if _, ok := parseInterval(in); ok {
			t.Errorf("parseInterval(%q) should be invalid", in)
		}
	}
}

func TestFormatNum(t *testing.T) {
	cases := map[float64]string{10: "10", 10.5: "10.5", 10.05: "10.05", 0: "0", 100: "100"}
	for in, want := range cases {
		if got := formatNum(in); got != want {
			t.Errorf("formatNum(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFriendlyErr(t *testing.T) {
	if got := friendlyErr(fmt.Errorf("wrap: %w", &wanxiao.APIError{Msg: "账号不存在"})); got != "账号不存在" {
		t.Errorf("api error: %q", got)
	}
	if got := friendlyErr(fmt.Errorf("request error: %w", context.DeadlineExceeded)); got != "连接完美校园服务器超时，请稍后再试" {
		t.Errorf("timeout: %q", got)
	}
	if got := friendlyErr(fmt.Errorf("boom")); got != "完美校园服务暂时不可用，请稍后再试" {
		t.Errorf("generic: %q", got)
	}
}

func TestSessions(t *testing.T) {
	bot := &Bot{sessions: make(map[int64]*session)}
	bot.setState(1, StateAddAccount_WaitAccount)
	bot.setTempData(1, "account", "abc")
	bot.setState(1, StateAddAccount_WaitCode)
	if bot.getState(1) != StateAddAccount_WaitCode || bot.getTempData(1, "account") != "abc" {
		t.Fatal("state transition should keep temp data")
	}

	bot.sessions[1].expires = time.Now().Add(-time.Second)
	if bot.getState(1) != StateNone || bot.getTempData(1, "account") != "" {
		t.Fatal("expired session should be ignored")
	}

	bot.setState(2, StateSettings_WaitInterval)
	bot.sessions[2].expires = time.Now().Add(-time.Second)
	bot.setState(3, StateSettings_WaitThreshold)
	bot.cleanupSessions()
	if len(bot.sessions) != 1 {
		t.Fatalf("cleanup left %d sessions", len(bot.sessions))
	}
	bot.clearState(3)
	if len(bot.sessions) != 0 {
		t.Fatal("clearState should delete the session")
	}
}

func TestBusyLock(t *testing.T) {
	bot := &Bot{}
	if !bot.tryLock(1) || bot.tryLock(1) {
		t.Fatal("second tryLock should fail")
	}
	bot.unlock(1)
	if !bot.tryLock(1) {
		t.Fatal("tryLock after unlock should succeed")
	}
}
