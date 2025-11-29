package main

import (
	"encoding/json"
	"log"
	"os"

	"wanxiao-elec-bot/bot"
	"wanxiao-elec-bot/model"

	"github.com/robfig/cron/v3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type Config struct {
	BotToken string `json:"bot_token"`
}

func main() {
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		// Try reading from config.json
		file, err := os.Open("config.json")
		if err == nil {
			var config Config
			decoder := json.NewDecoder(file)
			if err := decoder.Decode(&config); err == nil {
				token = config.BotToken
			}
			file.Close()
		}
	}

	if token == "" {
		log.Fatal("BOT_TOKEN 环境变量未设置，且无法从 config.json 读取 bot_token")
	}

	db, err := gorm.Open(sqlite.Open("wanxiao.db"), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	// Auto migrate
	db.AutoMigrate(&model.User{}, &model.Binding{})

	b, err := bot.NewBot(token, db)
	if err != nil {
		log.Fatal(err)
	}

	// Scheduler
	c := cron.New()

	// Check every minute (logic inside handles interval per user)
	c.AddFunc("* * * * *", b.CheckLowBalance)

	c.Start()

	log.Println("Bot started...")
	b.Start()
}
