package domain

import (
	"github.com/google/uuid"
	"time"
)

type File struct {
	UUID            uuid.UUID              `json:"uuid" db:"uuid"`
	Name            string                 `json:"name" db:"name"`
	MIMEType        string                 `json:"mime_type" db:"mime_type"`
	SizeBytes       int64                  `json:"size_bytes" db:"size_bytes"`
	FolderID        int64                  `json:"folder_id" db:"folder_id"`
	OwnerID         string                 `json:"owner_id" db:"owner_id"`
	CreatedAt       time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at" db:"updated_at"`
	DeletedAt       *time.Time             `json:"deleted_at,omitempty" db:"deleted_at"`
	RestorePath     *string                `json:"restore_path,omitempty" db:"restore_path"`
	RestoreFolderID *int64                 `json:"restore_folder_id,omitempty" db:"restore_folder_id"`
	CurrentVersion  int                    `json:"current_version" db:"current_version"`
	ContextType     *string                `json:"context_type,omitempty" db:"context_type"` // Изменено на *string
	Metadata        map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
}

type FileUpload struct {
	Name     string
	MIMEType string
	Size     int64
	FolderID int64
	OwnerID  string
	FileData []byte
}

type FileDownload struct {
	File *File
	Data []byte
}

// FileContext представляет контекст использования файла
type FileContext struct {
	Type string `json:"type" db:"context_type"` // chat, group, project, task, test
}

// FileUploadResponse представляет ответ на загрузку файла
// FileUploadResponse представляет ответ на загрузку файла
type FileUploadResponse struct {
	UUID        uuid.UUID `json:"uuid"`
	Name        string    `json:"name"`
	MIMEType    string    `json:"mime_type"`
	SizeBytes   int64     `json:"size_bytes"`
	OwnerID     string    `json:"owner_id"`
	ContextType *string   `json:"context_type,omitempty"` // Изменяем на указатель
	CreatedAt   time.Time `json:"created_at"`
}
