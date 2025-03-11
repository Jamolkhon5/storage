package domain

import (
	"github.com/google/uuid"
	"time"
)

type AccessType string
type ResourceType string

const (
	AccessTypeView AccessType = "view"
	AccessTypeEdit AccessType = "edit"
	AccessTypeFull AccessType = "full"

	ResourceTypeFile   ResourceType = "file"
	ResourceTypeFolder ResourceType = "folder"
)

type Share struct {
	ID           uuid.UUID    `json:"id" db:"id"`
	ResourceID   string       `json:"resource_id" db:"resource_id"`
	ResourceType ResourceType `json:"resource_type" db:"resource_type"`
	OwnerID      string       `json:"owner_id" db:"owner_id"`
	AccessType   AccessType   `json:"access_type" db:"access_type"`
	Token        string       `json:"token" db:"token"`
	ExpiresAt    *time.Time   `json:"expires_at,omitempty" db:"expires_at"`
	CreatedAt    time.Time    `json:"created_at" db:"created_at"`
	UserIDs      string       `json:"user_ids" db:"user_ids"` // Добавляем это поле
}
