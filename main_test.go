package main

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"wanxiao-elec-bot/history"
	"wanxiao-elec-bot/model"
)

func TestDatabaseSupportsConcurrentCollectors(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := openDB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	for i := 1; i <= 5; i++ {
		if err := db.Create(&model.Binding{ID: uint(i), UserID: 1, Account: fmt.Sprint(i), CustomerCode: "1399"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 25)
	for i := 1; i <= 5; i++ {
		wg.Add(1)
		go func(id uint) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				stamp := time.Now().UTC()
				err := history.Save(db, model.BalanceSample{BindingID: id, ObservedAt: stamp, Balance: float64(j)}, map[string]interface{}{"last_check": stamp, "last_balance": float64(j)})
				if err != nil {
					errs <- err
				}
			}
		}(uint(i))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var count int64
	db.Model(&model.BalanceSample{}).Count(&count)
	if count != 25 {
		t.Fatal("lost concurrent observations", count)
	}
}
