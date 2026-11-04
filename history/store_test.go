package history

import (
	"path/filepath"
	"testing"
	"time"

	"wanxiao-elec-bot/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
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

func TestAtomicSaveDuplicatesAndLateResults(t *testing.T) {
	db := testDB(t)
	b := model.Binding{UserID: 1, Account: "a", CustomerCode: "1399"}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	write := func(when time.Time, balance float64) error {
		return Save(db, model.BalanceSample{BindingID: b.ID, ObservedAt: when, Balance: balance, MeterKey: "a"}, map[string]interface{}{"last_check": when, "last_balance": balance})
	}
	if err := write(now, 10); err != nil {
		t.Fatal(err)
	}
	if err := write(now, 10); err != nil {
		t.Fatal(err)
	}
	if err := write(now.Add(-time.Minute), 11); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&model.BalanceSample{}).Count(&count)
	if count != 2 {
		t.Fatal("duplicate observation was not suppressed", count)
	}
	if err := db.First(&b, b.ID).Error; err != nil {
		t.Fatal(err)
	}
	if b.LastBalance != 10 || !b.LastCheck.Equal(now) {
		t.Fatal("late result overwrote latest cache", b)
	}
	if err := db.Delete(&b).Error; err != nil {
		t.Fatal(err)
	}
	if err := write(now.Add(time.Minute), 9); err == nil {
		t.Fatal("unbound account accepted an in-flight sample")
	}
	db.Model(&model.BalanceSample{}).Count(&count)
	if count != 2 {
		t.Fatal(count)
	}
}

func TestLoadDoesNotMixBindings(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	for _, id := range []uint{1, 2} {
		if err := db.Create(&model.BalanceSample{BindingID: id, ObservedAt: now, Balance: float64(id)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	samples, err := Load(db, 1, now.Add(-time.Hour), now)
	if err != nil || len(samples) != 1 || samples[0].BindingID != 1 {
		t.Fatal(samples, err)
	}
}
