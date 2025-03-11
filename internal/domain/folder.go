package domain

import (
	"github.com/jmoiron/sqlx/types"
	"time"
)

type SharedUser struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Lastname   string `json:"lastname"`
	Photo      string `json:"photo"`
	AccessType string `json:"access_type"` // Добавляем тип доступа для каждого пользователя
}

type ShareInfo struct {
	IsShared    bool         `json:"is_shared"`
	SharedUsers []SharedUser `json:"shared_users"`
}

type Folder struct {
	ID              int64          `json:"id" db:"id"`
	Name            string         `json:"name" db:"name"`
	OwnerID         string         `json:"owner_id" db:"owner_id"`
	ParentID        *int64         `json:"parent_id,omitempty" db:"parent_id"`
	Path            string         `json:"path" db:"path"`
	Level           int            `json:"level" db:"level"`
	SizeBytes       int64          `json:"size_bytes" db:"size_bytes"`
	FilesCount      int            `json:"files_count" db:"files_count"`
	CreatedAt       time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at" db:"updated_at"`
	DeletedAt       *time.Time     `json:"deleted_at,omitempty" db:"deleted_at"`
	RestorePath     *string        `json:"restore_path,omitempty" db:"restore_path"`
	RestoreParentID *int64         `json:"restore_parent_id,omitempty" db:"restore_parent_id"`
	ShareInfo       *ShareInfo     `json:"share_info,omitempty"`
	Metadata        types.JSONText `json:"metadata" db:"metadata"` // Добавляем это поле
}

type FolderContent struct {
	Folder  Folder   `json:"folder"`
	Files   []File   `json:"files"`
	Folders []Folder `json:"subfolders"`
}
