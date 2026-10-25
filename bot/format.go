package bot

import (
	"errors"
	"fmt"
	"html"
	"net"
	"strings"
	"time"
	_ "time/tzdata"

	"wanxiao-elec-bot/model"
	"wanxiao-elec-bot/wanxiao"

	"gopkg.in/telebot.v3"
)

var esc = html.EscapeString

// displayLoc is the time zone used when showing times to users.
var displayLoc = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.Local
}()

// roomDisplay returns the best human-readable name of a binding.
func roomDisplay(b *model.Binding) string {
	if b.RoomName != "" {
		return b.RoomName
	}
	return b.Account
}

// formatAgo renders a relative time like "5 分钟前".
func formatAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}

func formatNum(v float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.2f", v), "0"), ".0")
}

// friendlyErr converts an internal error into a message suitable for users.
// The raw error is expected to be logged by the caller.
func friendlyErr(err error) string {
	var apiErr *wanxiao.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Msg == "" {
			return "完美校园返回了未知错误"
		}
		return apiErr.Msg
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "连接完美校园服务器超时，请稍后再试"
	}
	return "完美校园服务暂时不可用，请稍后再试"
}

// isUnreachable reports whether a send error means the user can no longer be messaged.
func isUnreachable(err error) bool {
	return errors.Is(err, telebot.ErrBlockedByUser) ||
		errors.Is(err, telebot.ErrUserIsDeactivated) ||
		errors.Is(err, telebot.ErrChatNotFound)
}

// edit edits a message, ignoring "message is not modified" errors.
func (bot *Bot) edit(msg telebot.Editable, what interface{}, opts ...interface{}) error {
	_, err := bot.B.Edit(msg, what, opts...)
	if errors.Is(err, telebot.ErrMessageNotModified) {
		return nil
	}
	return err
}

// editOrSend edits the callback message when triggered by a button, otherwise sends a new one.
func editOrSend(c telebot.Context, what interface{}, opts ...interface{}) error {
	err := c.EditOrSend(what, opts...)
	if errors.Is(err, telebot.ErrMessageNotModified) {
		return nil
	}
	return err
}

// respond answers a callback query (no-op for non-callback updates).
func respond(c telebot.Context, text ...string) {
	if c.Callback() == nil {
		return
	}
	resp := &telebot.CallbackResponse{}
	if len(text) > 0 {
		resp.Text = text[0]
	}
	_ = c.Respond(resp)
}

func inlineMarkup(rows ...telebot.Row) *telebot.ReplyMarkup {
	menu := &telebot.ReplyMarkup{}
	menu.Inline(rows...)
	return menu
}

// noBindingsReply is shown when the user has no account yet.
func noBindingsReply(c telebot.Context) error {
	return editOrSend(c, "你还没有绑定任何账号。\n点击下方按钮添加一个吧 👇",
		inlineMarkup(telebot.Row{btnAddAccount}))
}
