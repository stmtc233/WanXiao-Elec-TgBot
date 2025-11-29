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
	NotifyEnabled   bool    `gorm:"default:false"`
	CheckInterval   int     `gorm:"default:60"` // In minutes

	Bindings []Binding `gorm:"foreignKey:UserID"`
}

type Binding struct {
	ID           uint  `gorm:"primaryKey"`
	UserID       int64 `gorm:"index"`
	Account      string
	CustomerCode string

	// Cache/Display info
	RoomName    string
	LastBalance float64
	LastCheck   time.Time
}
