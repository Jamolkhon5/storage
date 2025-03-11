package repository

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"synxrondrive/internal/domain"
)

type FileRepository struct {
	db *sqlx.DB
}

func NewFileRepository(db *sqlx.DB) *FileRepository {
	return &FileRepository{db: db}
}

func (r *FileRepository) Create(ctx context.Context, file *domain.File) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Вставляем файл
	query := `
        INSERT INTO files (uuid, name, mime_type, size_bytes, folder_id, owner_id)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING created_at, updated_at`

	err = tx.QueryRowContext(
		ctx,
		query,
		file.UUID,
		file.Name,
		file.MIMEType,
		file.SizeBytes,
		file.FolderID,
		file.OwnerID,
	).Scan(&file.CreatedAt, &file.UpdatedAt)
	if err != nil {
		return err
	}

	// Обновляем метаданные папки и всех родительских папок
	updateQuery := `
        WITH RECURSIVE folder_tree AS (
            -- Начальная папка
            SELECT id, parent_id, size_bytes, files_count
            FROM folders 
            WHERE id = $1
            
            UNION ALL
            
            -- Все родительские папки
            SELECT f.id, f.parent_id, f.size_bytes, f.files_count
            FROM folders f
            INNER JOIN folder_tree ft ON f.id = ft.parent_id
        )
        UPDATE folders f
        SET 
            size_bytes = f.size_bytes + $2,
            files_count = f.files_count + 1,
            updated_at = CURRENT_TIMESTAMP
        WHERE f.id IN (SELECT id FROM folder_tree)`

	_, err = tx.ExecContext(ctx, updateQuery, file.FolderID, file.SizeBytes)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (r *FileRepository) GetByUUID(ctx context.Context, uuid uuid.UUID) (*domain.File, error) {
	var file domain.File
	query := `SELECT * FROM files WHERE uuid = $1`

	err := r.db.GetContext(ctx, &file, query, uuid)
	if err != nil {
		return nil, err
	}

	return &file, nil
}

func (r *FileRepository) GetByFolder(ctx context.Context, folderID int64) ([]domain.File, error) {
	var files []domain.File
	query := `SELECT * FROM files WHERE folder_id = $1 ORDER BY name`

	err := r.db.SelectContext(ctx, &files, query, folderID)
	if err != nil {
		return nil, err
	}

	return files, nil
}

// В file_repository.go
func (r *FileRepository) Update(ctx context.Context, file *domain.File) error {
	query := `
        UPDATE files 
        SET size_bytes = $1, 
            current_version = $2,
            updated_at = CURRENT_TIMESTAMP
        WHERE uuid = $3
    `
	_, err := r.db.ExecContext(ctx, query, file.SizeBytes, file.CurrentVersion, file.UUID)
	if err != nil {
		return fmt.Errorf("error updating file: %w", err)
	}
	return nil
}

// Delete для FileRepository
func (r *FileRepository) Delete(ctx context.Context, uuid uuid.UUID) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Сначала получаем информацию о файле
	var file domain.File
	err = tx.GetContext(ctx, &file, "SELECT * FROM files WHERE uuid = $1", uuid)
	if err != nil {
		return err
	}

	// Обновляем метаданные папок (уменьшаем размер и количество файлов)
	updateFoldersQuery := `
        WITH RECURSIVE folder_tree AS (
            -- Начальная папка
            SELECT id, parent_id, size_bytes, files_count
            FROM folders 
            WHERE id = $1
            
            UNION ALL
            
            -- Все родительские папки
            SELECT f.id, f.parent_id, f.size_bytes, f.files_count
            FROM folders f
            INNER JOIN folder_tree ft ON f.id = ft.parent_id
        )
        UPDATE folders f
        SET 
            size_bytes = CASE 
                WHEN f.size_bytes >= $2 THEN f.size_bytes - $2
                ELSE 0
            END,
            files_count = CASE 
                WHEN f.files_count > 0 THEN f.files_count - 1
                ELSE 0
            END,
            updated_at = CURRENT_TIMESTAMP
        FROM folder_tree ft
        WHERE f.id = ft.id`

	// Выполняем обновление метаданных
	_, err = tx.ExecContext(ctx, updateFoldersQuery, file.FolderID, file.SizeBytes)
	if err != nil {
		return fmt.Errorf("failed to update folder metadata: %w", err)
	}

	// Удаляем файл
	_, err = tx.ExecContext(ctx, "DELETE FROM files WHERE uuid = $1", uuid)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return tx.Commit()
}

func (r *FileRepository) GetRootFolder(ctx context.Context, ownerID string) (*domain.Folder, error) {
	var folder domain.Folder
	query := `SELECT * FROM folders WHERE owner_id = $1 AND name = 'Root' LIMIT 1`

	err := r.db.GetContext(ctx, &folder, query, ownerID)
	if err != nil {
		return nil, err
	}

	return &folder, nil
}

// GetFileVersions получает все версии файла
func (r *FileRepository) GetFileVersions(ctx context.Context, fileUUID uuid.UUID) ([]domain.FileVersion, error) {
	var versions []domain.FileVersion
	query := `
        SELECT * FROM file_versions 
        WHERE file_uuid = $1 AND deleted_at IS NULL 
        ORDER BY version_number DESC
    `
	err := r.db.SelectContext(ctx, &versions, query, fileUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get file versions: %w", err)
	}
	return versions, nil
}

// UpdateFileVersion обновляет версию файла
func (r *FileRepository) UpdateFileVersion(ctx context.Context, fileUUID uuid.UUID, version int) error {
	query := `UPDATE files SET current_version = $1 WHERE uuid = $2`
	_, err := r.db.ExecContext(ctx, query, version, fileUUID)
	if err != nil {
		return fmt.Errorf("failed to update file version: %w", err)
	}
	return nil
}

// DeleteVersion помечает версию как удаленную
func (r *FileRepository) DeleteVersion(ctx context.Context, tx *sqlx.Tx, fileUUID uuid.UUID, versionNumber int) error {
	query := `
        UPDATE file_versions 
        SET deleted_at = CURRENT_TIMESTAMP 
        WHERE file_uuid = $1 AND version_number = $2
    `
	result, err := tx.ExecContext(ctx, query, fileUUID, versionNumber)
	if err != nil {
		return fmt.Errorf("failed to delete version: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("version not found")
	}

	return nil
}

func (r *FileRepository) CheckFileExists(ctx context.Context, folderID int64, fileName string) (*domain.File, error) {
	var file domain.File
	query := `
        SELECT * FROM files 
        WHERE folder_id = $1 
        AND name = $2 
        AND deleted_at IS NULL
        LIMIT 1
    `
	err := r.db.GetContext(ctx, &file, query, folderID, fileName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error checking file existence: %w", err)
	}
	return &file, nil
}

func (r *FileRepository) CreateFileVersion(ctx context.Context, tx *sqlx.Tx, version *domain.FileVersion) error {
	query := `
        INSERT INTO file_versions (file_uuid, version_number, s3_key, size_bytes)
        VALUES ($1, $2, $3, $4)
        RETURNING id, created_at`

	return tx.QueryRowContext(ctx, query,
		version.FileUUID,
		version.VersionNumber,
		version.S3Key,
		version.SizeBytes,
	).Scan(&version.ID, &version.CreatedAt)
}

func (r *FileRepository) UpdateFileSize(ctx context.Context, tx *sqlx.Tx, fileUUID uuid.UUID, size int64) error {
	query := `
        UPDATE files
        SET size_bytes = $1, updated_at = CURRENT_TIMESTAMP
        WHERE uuid = $2
    `
	_, err := tx.ExecContext(ctx, query, size, fileUUID)
	return err
}

func (r *FileRepository) BeginTx(ctx context.Context) (*sqlx.Tx, error) {
	return r.db.BeginTxx(ctx, nil)
}

func (r *FileRepository) GetCurrentVersion(ctx context.Context, tx *sqlx.Tx, fileUUID uuid.UUID) (int, error) {
	var version int
	query := "SELECT current_version FROM files WHERE uuid = $1"
	err := tx.QueryRowContext(ctx, query, fileUUID).Scan(&version)
	return version, err
}

func (r *FileRepository) DeletePreview(ctx context.Context, tx *sqlx.Tx, fileUUID uuid.UUID) error {
	query := `DELETE FROM file_previews WHERE file_uuid = $1`

	// Если транзакция передана, используем её
	if tx != nil {
		_, err := tx.ExecContext(ctx, query, fileUUID)
		if err != nil {
			return fmt.Errorf("failed to delete preview within transaction: %w", err)
		}
		return nil
	}

	// Если транзакция не передана, используем обычное соединение
	_, err := r.db.ExecContext(ctx, query, fileUUID)
	if err != nil {
		return fmt.Errorf("failed to delete preview: %w", err)
	}
	return nil
}

// CreateContextFile создает файл с контекстом в базе данных
func (r *FileRepository) CreateContextFile(ctx context.Context, file *domain.File) error {
	query := `
        INSERT INTO files (
            uuid, 
            name, 
            mime_type, 
            size_bytes, 
            owner_id, 
            context_type, 
            current_version
        )
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        RETURNING created_at, updated_at
    `

	err := r.db.QueryRowContext(
		ctx,
		query,
		file.UUID,
		file.Name,
		file.MIMEType,
		file.SizeBytes,
		file.OwnerID,
		file.ContextType, // теперь это указатель, sqlx автоматически обработает NULL
		1,
	).Scan(&file.CreatedAt, &file.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create context file in repository: %w", err)
	}

	return nil
}

// UpdateFileName обновляет имя файла
func (r *FileRepository) UpdateFileName(ctx context.Context, fileUUID uuid.UUID, newName string) error {
	query := `
        UPDATE files 
        SET name = $1,
            updated_at = CURRENT_TIMESTAMP
        WHERE uuid = $2
    `
	result, err := r.db.ExecContext(ctx, query, newName, fileUUID)
	if err != nil {
		return fmt.Errorf("failed to update file name: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("file not found")
	}

	return nil
}

// UpdateFileFolder обновляет ID папки файла
func (r *FileRepository) UpdateFileFolder(ctx context.Context, tx *sqlx.Tx, fileUUID uuid.UUID, newFolderID int64, oldFolderID int64) error {
	// Обновляем ID папки файла
	updateFileQuery := `
        UPDATE files 
        SET folder_id = $1,
            updated_at = CURRENT_TIMESTAMP
        WHERE uuid = $2
    `
	result, err := tx.ExecContext(ctx, updateFileQuery, newFolderID, fileUUID)
	if err != nil {
		return fmt.Errorf("failed to update file folder: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("file not found")
	}

	// Получаем размер файла для обновления метаданных папок
	var fileSize int64
	err = tx.QueryRowContext(ctx,
		"SELECT size_bytes FROM files WHERE uuid = $1",
		fileUUID,
	).Scan(&fileSize)
	if err != nil {
		return fmt.Errorf("failed to get file size: %w", err)
	}

	// Обновляем метаданные старой папки (уменьшаем размер и количество файлов)
	updateOldFolderQuery := `
        WITH RECURSIVE folder_tree AS (
            SELECT id, parent_id FROM folders WHERE id = $1
            UNION ALL
            SELECT f.id, f.parent_id 
            FROM folders f
            INNER JOIN folder_tree ft ON f.id = ft.parent_id
        )
        UPDATE folders f
        SET 
            size_bytes = f.size_bytes - $2,
            files_count = f.files_count - 1,
            updated_at = CURRENT_TIMESTAMP
        WHERE f.id IN (SELECT id FROM folder_tree)
    `
	_, err = tx.ExecContext(ctx, updateOldFolderQuery, oldFolderID, fileSize)
	if err != nil {
		return fmt.Errorf("failed to update old folder metadata: %w", err)
	}

	// Обновляем метаданные новой папки (увеличиваем размер и количество файлов)
	updateNewFolderQuery := `
        WITH RECURSIVE folder_tree AS (
            SELECT id, parent_id FROM folders WHERE id = $1
            UNION ALL
            SELECT f.id, f.parent_id 
            FROM folders f
            INNER JOIN folder_tree ft ON f.id = ft.parent_id
        )
        UPDATE folders f
        SET 
            size_bytes = f.size_bytes + $2,
            files_count = f.files_count + 1,
            updated_at = CURRENT_TIMESTAMP
        WHERE f.id IN (SELECT id FROM folder_tree)
    `
	_, err = tx.ExecContext(ctx, updateNewFolderQuery, newFolderID, fileSize)
	if err != nil {
		return fmt.Errorf("failed to update new folder metadata: %w", err)
	}

	return nil
}

func (r *FileRepository) GetDB() *sqlx.DB {
	return r.db
}

// GetDeletedFileInfo получает информацию о файле, даже если он помечен как удаленный
func (r *FileRepository) GetDeletedFileInfo(ctx context.Context, uuid uuid.UUID) (*domain.File, error) {
	var file domain.File
	query := `SELECT * FROM files WHERE uuid = $1`

	err := r.db.GetContext(ctx, &file, query, uuid)
	if err != nil {
		return nil, err
	}

	return &file, nil
}
