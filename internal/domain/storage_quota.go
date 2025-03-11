package domain

import "time"

type StorageQuota struct {
	ID              int64     `json:"id" db:"id"`
	OwnerID         string    `json:"owner_id" db:"owner_id"`
	TotalBytesLimit int64     `json:"total_bytes_limit" db:"total_bytes_limit"`
	UsedBytes       int64     `json:"used_bytes" db:"used_bytes"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

type QuotaInfo struct {
	TotalSpace     int64   `json:"total_space"`
	UsedSpace      int64   `json:"used_space"`
	AvailableSpace int64   `json:"available_space"`
	UsagePercent   float64 `json:"usage_percent"`
}
