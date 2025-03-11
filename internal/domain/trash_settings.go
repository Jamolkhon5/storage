// trash_settings.go

package domain

import "time"

// TrashSettings представляет настройки корзины для пользователя
type TrashSettings struct {
	ID              int64     `json:"id" db:"id"`
	OwnerID         string    `json:"owner_id" db:"owner_id"`
	RetentionPeriod string    `json:"retention_period" db:"retention_period"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// TrashItem представляет элемент в корзине (может быть файлом или папкой)
type TrashItem struct {
	ID           string    `json:"id" db:"id"`
	Name         string    `json:"name" db:"name"`
	Type         string    `json:"type" db:"type"`
	Path         string    `json:"path" db:"path"`
	Size         int64     `json:"size" db:"size"`
	DeletedAt    time.Time `json:"deleted_at" db:"deleted_at"`
	RestorePath  string    `json:"restore_path" db:"restore_path"`
	ExpiresIn    string    `json:"expires_in"` // Это поле вычисляемое
	OriginalPath string    `json:"original_path" db:"original_path"`
	MIMEType     *string   `json:"mime_type,omitempty" db:"mime_type"` // Изменено на указатель
}

// DeleteInfo содержит информацию об удаляемом файле
type DeleteInfo struct {
	UUID    string `db:"uuid"`
	OwnerID string `db:"owner_id"`
	Name    string `db:"name"`
}
