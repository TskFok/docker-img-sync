package model

import "time"

type SyncTask struct {
	ID                 int32      `gorm:"column:id;primaryKey;autoIncrement"`
	Name               string     `gorm:"column:name"`
	SourceImage        string     `gorm:"column:source_image"`
	SourceCredentialID *int32     `gorm:"column:source_credential_id"`
	RegistryID         int32      `gorm:"column:registry_id"`
	DestRepository     string     `gorm:"column:dest_repository"`
	DestTag            string     `gorm:"column:dest_tag"`
	IntervalSeconds    int        `gorm:"column:interval_seconds"`
	Enabled            bool       `gorm:"column:enabled"`
	LastDigest         string     `gorm:"column:last_digest"`
	LastStatus         string     `gorm:"column:last_status"`
	ProgressPhase      string     `gorm:"column:progress_phase"`
	LastError          string     `gorm:"column:last_error"`
	LastSyncedAt       *time.Time `gorm:"column:last_synced_at"`
	NextRunAt          *time.Time `gorm:"column:next_run_at"`
	CreatedAt          time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;autoUpdateTime"`
}
