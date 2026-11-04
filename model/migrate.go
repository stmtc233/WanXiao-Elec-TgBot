package model

import (
	"strings"

	"gorm.io/gorm"
)

func MeterKey(verify, room, kind string) string {
	if verify == "" {
		verify = room
	}
	return strings.Join([]string{verify, kind}, "\x1f")
}

// Migrate adds history without inventing older readings. A cached balance can
// seed one observation at its original LastCheck time. Running this is idempotent.
func Migrate(db *gorm.DB) error {
	if db.Migrator().HasTable(&Binding{}) {
		if err := db.Exec(`DELETE FROM bindings WHERE id NOT IN (
			SELECT MIN(id) FROM bindings GROUP BY user_id, account, customer_code)`).Error; err != nil {
			return err
		}
	}
	if err := db.AutoMigrate(&User{}, &Binding{}, &BalanceSample{}); err != nil {
		return err
	}
	var bindings []Binding
	if err := db.Where("NOT EXISTS (SELECT 1 FROM balance_samples WHERE binding_id = bindings.id)").Find(&bindings).Error; err != nil {
		return err
	}
	for _, b := range bindings {
		if b.LastCheck.IsZero() {
			continue
		}
		s := BalanceSample{BindingID: b.ID, ObservedAt: b.LastCheck.UTC(),
			MeterKey: MeterKey(b.RoomVerify, b.RoomName, b.MeterType), RoomName: b.RoomName,
			Balance: b.LastBalance, Source: "cached"}
		if err := db.Create(&s).Error; err != nil {
			return err
		}
	}
	return nil
}
