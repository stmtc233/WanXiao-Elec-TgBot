package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wanxiao-elec-bot/bot"
	"wanxiao-elec-bot/model"

	"github.com/glebarez/sqlite"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Config struct {
	BotToken string `json:"bot_token"`
}

// dbDSN enables WAL and a busy timeout so concurrent handlers and the scheduler
// don't fail with "database is locked".
const dbDSN = "wanxiao.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

func loadToken() string {
	if token := os.Getenv("BOT_TOKEN"); token != "" {
		return token
	}
	// Try reading from config.json
	file, err := os.Open("config.json")
	if err != nil {
		return ""
	}
	defer file.Close()
	var config Config
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		log.Printf("读取 config.json 失败: %v", err)
		return ""
	}
	return config.BotToken
}

func openDB() (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(dbDSN), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}

	// Remove duplicate bindings left by older versions before adding the unique index.
	if db.Migrator().HasTable(&model.Binding{}) {
		if err := db.Exec(`DELETE FROM bindings WHERE id NOT IN (
			SELECT MIN(id) FROM bindings GROUP BY user_id, account, customer_code)`).Error; err != nil {
			return nil, err
		}
	}

	if err := db.AutoMigrate(&model.User{}, &model.Binding{}); err != nil {
		return nil, err
	}
	return db, nil
}

func main() {
	token := loadToken()
	if token == "" {
		log.Fatal("BOT_TOKEN 环境变量未设置，且无法从 config.json 读取 bot_token")
	}

	db, err := openDB()
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}

	b, err := bot.NewBot(token, db)
	if err != nil {
		log.Fatal(err)
	}

	// Scheduler: check every minute (logic inside handles interval per user).
	// SkipIfStillRunning prevents overlapping runs when a check takes longer than a minute.
	c := cron.New(cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))
	if _, err := c.AddFunc("* * * * *", b.CheckLowBalance); err != nil {
		log.Fatal(err)
	}
	c.Start()

	go b.Start()
	log.Println("Bot started...")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Println("Shutting down...")
	b.Stop()
	select {
	case <-c.Stop().Done():
	case <-time.After(30 * time.Second):
		log.Println("Timed out waiting for running checks")
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.Close()
	}
	log.Println("Bye")
}
