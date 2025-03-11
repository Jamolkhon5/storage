package domain

import (
	"github.com/google/uuid"
	"time"
)

const (
	RecordingsFolderName = "conferense_recordings"
	RecordingFolderType  = "recordings"
)

type Recording struct {
	FileUUID  uuid.UUID `json:"file_uuid" db:"file_uuid"`
	RoomID    string    `json:"room_id" db:"room_id"`
	EgressID  string    `json:"egress_id" db:"egress_id"`
	UserID    string    `json:"user_id" db:"user_id"`
	FolderID  int64     `json:"folder_id" db:"folder_id"`
	S3Path    string    `json:"s3_path" db:"s3_path"`
	Verified  bool      `json:"verified" db:"verified"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type RecordingsFolder struct {
	Folder
	Type string `json:"type" db:"type"`
}
