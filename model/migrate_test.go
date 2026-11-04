package model

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestExistingDatabaseSeedsOnlyKnownCacheAndKeepsSettings(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, notify_threshold REAL, notify_enabled NUMERIC, check_interval INTEGER)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO users VALUES (123, 20, 0, 1440)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Binding{}); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	bindings := []Binding{{UserID: 123, Account: "a", CustomerCode: "1399", RoomName: "测试宿舍", LastCheck: stamp, LastBalance: 50}, {UserID: 123, Account: "b", CustomerCode: "1399"}}
	if err := db.Create(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Migrate(db); err != nil {
			t.Fatal(err)
		}
	}
	var samples []BalanceSample
	if err := db.Find(&samples).Error; err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || samples[0].Balance != 50 || !samples[0].ObservedAt.Equal(stamp) {
		t.Fatal(samples)
	}
	var user User
	if err := db.First(&user, 123).Error; err != nil {
		t.Fatal(err)
	}
	if user.NotifyEnabled || user.NotifyThreshold != 20 {
		t.Fatal("migration changed notification preferences", user)
	}
}
