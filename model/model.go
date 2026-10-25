package model

import (
	"time"
)

type User struct {
	ID        int64 `gorm:"primaryKey"` // Telegram User ID
	CreatedAt time.Time
	UpdatedAt time.Time

	// Settings
	NotifyThreshold float64 `gorm:"default:10.0"` // Low balance threshold
	NotifyEnabled   bool    `gorm:"default:true"`
	CheckInterval   int     `gorm:"default:60"` // In minutes

	Bindings []Binding `gorm:"foreignKey:UserID"`
}

type Binding struct {
	ID           uint   `gorm:"primaryKey"`
	UserID       int64  `gorm:"uniqueIndex:idx_binding_user_account_code"`
	Account      string `gorm:"uniqueIndex:idx_binding_user_account_code"`
	CustomerCode string `gorm:"uniqueIndex:idx_binding_user_account_code"`

	// Cache/Display info
	RoomName    string
	RoomVerify  string
	LastBalance float64
	LastCheck   time.Time // Last successful query

	// Cached student info for recharge (saves one request per recharge)
	RealName   string
	SchoolName string

	// Monitor state
	LastAttempt time.Time  // Last background check, successful or not
	FailCount   int        // Consecutive background check failures, used for backoff
	AlertedAt   *time.Time // When the last low balance alert was sent; nil = not alerted
}
