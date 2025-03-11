// domain/file_version.go
package domain

import (
	"github.com/google/uuid"
	"time"
)

type FileVersion struct {
	ID            int64      `json:"id" db:"id"`
	FileUUID      uuid.UUID  `json:"file_uuid" db:"file_uuid"`
	VersionNumber int        `json:"version_number" db:"version_number"`
	S3Key         string     `json:"s3_key" db:"s3_key"`
	SizeBytes     int64      `json:"size_bytes" db:"size_bytes"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}
