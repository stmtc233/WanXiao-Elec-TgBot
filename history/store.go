package history

import (
	"math"
	"time"

	"wanxiao-elec-bot/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Save atomically updates the cache and appends history. The binding must still
// exist, so an in-flight query cannot recreate history after unbinding.
func Save(db *gorm.DB, sample model.BalanceSample, cache map[string]interface{}) error {
	if math.IsNaN(sample.Balance) || math.IsInf(sample.Balance, 0) || sample.ObservedAt.IsZero() {
		return gorm.ErrInvalidData
	}
	sample.ObservedAt = sample.ObservedAt.UTC()
	return db.Transaction(func(tx *gorm.DB) error {
		var binding model.Binding
		if err := tx.First(&binding, sample.BindingID).Error; err != nil {
			return err
		}
		// A slower, older request may finish after a newer one. Keep the cache
		// monotonic; the immutable observations are sorted by time for analysis.
		if !binding.LastCheck.After(sample.ObservedAt) {
			if err := tx.Model(&binding).Updates(cache).Error; err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&sample).Error
	})
}

// Load includes neighboring samples for interpolation at range boundaries.
func Load(db *gorm.DB, bindingID uint, start, end time.Time) ([]model.BalanceSample, error) {
	var samples []model.BalanceSample
	err := db.Where("binding_id = ? AND observed_at >= ? AND observed_at <= ?", bindingID,
		start.Add(-MaxSampleGap).UTC(), end.Add(MaxSampleGap).UTC()).
		Order("observed_at, id").Find(&samples).Error
	return samples, err
}
