package bot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"wanxiao-elec-bot/model"

	"gopkg.in/telebot.v3"
)

// Exercise the real Telebot context/transport, rather than only a context
// double: photo edits and caption edits use different Telegram endpoints.
func TestStatsNavigationReusesTelegramMessage(t *testing.T) {
	type request struct {
		method string
		fields map[string]string
	}
	var mu sync.Mutex
	var requests []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fields := make(map[string]string)
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			if err := r.ParseMultipartForm(2 << 20); err != nil {
				t.Error(err)
			}
			if r.MultipartForm != nil {
				defer r.MultipartForm.RemoveAll()
			}
			for k := range r.Form {
				fields[k] = r.FormValue(k)
			}
		} else {
			if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
				t.Error(err)
			}
		}
		parts := strings.Split(r.URL.Path, "/")
		method := parts[len(parts)-1]
		mu.Lock()
		requests = append(requests, request{method, fields})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if method == "answerCallbackQuery" {
			fmt.Fprint(w, `{"ok":true,"result":true}`)
			return
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":42,"chat":{"id":100,"type":"private"},"photo":[{"file_id":"chart","width":900,"height":980}]}}`)
	}))
	defer server.Close()
	telegram, err := telebot.NewBot(telebot.Settings{Token: "test", URL: server.URL, Offline: true, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	db := statsDB(t)
	first := model.Binding{UserID: 100, Account: "first", CustomerCode: "1399", RoomName: "宿舍一"}
	second := model.Binding{UserID: 100, Account: "second", CustomerCode: "1399", RoomName: "宿舍二"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	bot := &Bot{B: telegram, DB: db, sessions: make(map[int64]*session)}
	context := func(data string, photo bool) telebot.Context {
		message := &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: 100}, Text: "请选择账号"}
		if photo {
			message.Text = ""
			message.Photo = &telebot.Photo{File: telebot.File{FileID: "chart"}}
		}
		return telegram.NewContext(telebot.Update{Callback: &telebot.Callback{ID: "callback", Sender: &telebot.User{ID: 100}, Message: message, Data: data}})
	}
	for i, data := range []string{
		fmt.Sprintf("%d:today:hours", first.ID), // Text picker becomes a photo.
		fmt.Sprintf("%d:7d:daily", first.ID),    // Existing photo changes mode.
		fmt.Sprintf("%d:7d:more-daily", first.ID),
	} {
		if err := bot.handleStatsCallback(context(data, i > 0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := bot.handleStats(context("", true)); err != nil {
		t.Fatal(err)
	}
	if err := bot.handleStatsCallback(context(fmt.Sprintf("%d:today:hours", second.ID), true)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var edits []request
	for _, r := range requests {
		if r.method == "answerCallbackQuery" {
			continue
		}
		if r.method != "editMessageMedia" && r.method != "editMessageCaption" {
			t.Fatalf("navigation sent a new message: %s", r.method)
		}
		if r.fields["message_id"] != "42" || r.fields["chat_id"] != "100" {
			t.Fatalf("edited another message: %+v", r)
		}
		var markup telebot.ReplyMarkup
		if err := json.Unmarshal([]byte(r.fields["reply_markup"]), &markup); err != nil || len(markup.InlineKeyboard) == 0 {
			t.Fatalf("edit lost its controls: %+v, %v", r, err)
		}
		edits = append(edits, r)
	}
	if len(edits) != 5 || edits[3].method != "editMessageCaption" {
		t.Fatalf("unexpected navigation: %+v", edits)
	}
}
