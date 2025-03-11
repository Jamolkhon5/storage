package repository

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"log"
	"synxrondrive/internal/domain"
)

type RecordingRepository struct {
	db *sqlx.DB
}

func NewRecordingRepository(db *sqlx.DB) *RecordingRepository {
	return &RecordingRepository{db: db}
}

func (r *RecordingRepository) SaveRecording(ctx context.Context, recording *domain.Recording) error {
	query := `
        INSERT INTO recordings (file_uuid, room_id, egress_id, user_id, folder_id)
        VALUES ($1, $2, $3, $4, $5)
        RETURNING created_at`

	err := r.db.QueryRowContext(ctx, query,
		recording.FileUUID, recording.RoomID, recording.EgressID,
		recording.UserID, recording.FolderID).Scan(&recording.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to save recording: %w", err)
	}

	return nil
}

// UpdateRecordingStatus обновляет статус проверки записи
func (r *RecordingRepository) UpdateRecordingStatus(ctx context.Context, fileUUID uuid.UUID, verified bool) error {
	// Сначала проверим наличие колонки verified
	var hasVerifiedColumn bool
	err := r.db.QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 
            FROM information_schema.columns 
            WHERE table_name = 'recordings' 
            AND column_name = 'verified'
        )
    `).Scan(&hasVerifiedColumn)

	if err != nil {
		return fmt.Errorf("failed to check column existence: %w", err)
	}

	// Если колонки нет, добавим её
	if !hasVerifiedColumn {
		_, err := r.db.ExecContext(ctx, `
            ALTER TABLE recordings 
            ADD COLUMN verified BOOLEAN DEFAULT FALSE
        `)
		if err != nil {
			return fmt.Errorf("failed to add verified column: %w", err)
		}
	}

	// Теперь обновляем статус
	query := `
        UPDATE recordings 
        SET verified = $1
        WHERE file_uuid = $2
    `

	_, err = r.db.ExecContext(ctx, query, verified, fileUUID)
	if err != nil {
		return fmt.Errorf("failed to update recording status: %w", err)
	}

	return nil
}

// GetRecordingByEgressID получает запись по EgressID
func (r *RecordingRepository) GetRecordingByEgressID(ctx context.Context, egressID string) (*domain.Recording, error) {
	query := `
        SELECT r.file_uuid, r.room_id, r.egress_id, r.user_id, r.folder_id, r.created_at,
               f.name, f.size_bytes, f.mime_type
        FROM recordings r
        JOIN files f ON r.file_uuid = f.uuid
        WHERE r.egress_id = $1
    `

	var recording domain.Recording
	err := r.db.GetContext(ctx, &recording, query, egressID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recording: %w", err)
	}

	return &recording, nil
}

// GetOrCreateRecordingsFolder находит или создает папку "Записи видеовстреч"
func (r *RecordingRepository) GetOrCreateRecordingsFolder(ctx context.Context, userID string) (*domain.Folder, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Сначала получим корневую папку пользователя
	var rootFolder domain.Folder
	err = tx.QueryRowContext(ctx, `
        SELECT 
            id, name, owner_id, parent_id, path, level 
        FROM folders 
        WHERE owner_id = $1 
        AND parent_id IS NULL 
        AND name = 'Root'
        AND deleted_at IS NULL
    `, userID).Scan(
		&rootFolder.ID, &rootFolder.Name, &rootFolder.OwnerID,
		&rootFolder.ParentID, &rootFolder.Path, &rootFolder.Level,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			// Если корневой папки нет, создаем ее
			log.Printf("Root folder not found for user %s, creating one", userID)
			err = tx.QueryRowContext(ctx, `
                INSERT INTO folders (name, owner_id, path, level)
                VALUES ('Root', $1, '/', 0)
                RETURNING id, path, level
            `, userID).Scan(&rootFolder.ID, &rootFolder.Path, &rootFolder.Level)

			if err != nil {
				return nil, fmt.Errorf("failed to create root folder: %w", err)
			}

			rootFolder.Name = "Root"
			rootFolder.OwnerID = userID
		} else {
			return nil, fmt.Errorf("failed to get root folder: %w", err)
		}
	}

	log.Printf("Found root folder for user %s: ID=%d", userID, rootFolder.ID)

	// Теперь ищем папку "Записи видеовстреч" внутри корневой папки
	var recordingsFolder domain.Folder
	err = tx.QueryRowContext(ctx, `
        SELECT 
            id, name, owner_id, parent_id, path, level, 
            size_bytes, files_count, created_at, updated_at, 
            deleted_at, restore_path, restore_parent_id, metadata
        FROM folders 
        WHERE owner_id = $1 
        AND name = $2 
        AND parent_id = $3
        AND metadata->>'type' = $4
        AND deleted_at IS NULL
    `, userID, domain.RecordingsFolderName, rootFolder.ID, domain.RecordingFolderType).
		Scan(
			&recordingsFolder.ID, &recordingsFolder.Name, &recordingsFolder.OwnerID, &recordingsFolder.ParentID,
			&recordingsFolder.Path, &recordingsFolder.Level, &recordingsFolder.SizeBytes, &recordingsFolder.FilesCount,
			&recordingsFolder.CreatedAt, &recordingsFolder.UpdatedAt, &recordingsFolder.DeletedAt,
			&recordingsFolder.RestorePath, &recordingsFolder.RestoreParentID, &recordingsFolder.Metadata,
		)

	if err == nil {
		log.Printf("Found existing recordings folder: ID=%d", recordingsFolder.ID)

		// ДОБАВЛЯЕМ: Обновляем фактический размер и количество файлов в папке
		if err := r.updateRecordingsFolderStats(ctx, tx, recordingsFolder.ID); err != nil {
			log.Printf("Warning: Failed to update recordings folder stats: %v", err)
			// Продолжаем выполнение даже при ошибке
		}

		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("failed to commit transaction: %w", err)
		}
		return &recordingsFolder, nil
	}

	// Если папка не найдена, создаем новую
	if err == sql.ErrNoRows {
		// Создаем метаданные для папки
		metadata := fmt.Sprintf(`{"type": "%s"}`, domain.RecordingFolderType)

		// Построение пути для новой папки
		folderPath := fmt.Sprintf("%s/%s", rootFolder.Path, domain.RecordingsFolderName)
		if rootFolder.Path == "/" {
			folderPath = fmt.Sprintf("/%s", domain.RecordingsFolderName)
		}

		log.Printf("Creating new recordings folder with path: %s under parent ID: %d", folderPath, rootFolder.ID)

		// Создаем новую папку "Записи видеовстреч"
		err = tx.QueryRowContext(ctx, `
            INSERT INTO folders (
                name, 
                owner_id, 
                parent_id,
                path, 
                level, 
                metadata,
                size_bytes,
                files_count
            )
            VALUES (
                $1, 
                $2,
                $3,
                $4, 
                $5, 
                $6::jsonb,
                0,
                0
            )
            RETURNING id, created_at, updated_at, size_bytes, files_count
        `,
			domain.RecordingsFolderName,
			userID,
			rootFolder.ID,
			folderPath,
			rootFolder.Level+1,
			metadata,
		).Scan(
			&recordingsFolder.ID,
			&recordingsFolder.CreatedAt,
			&recordingsFolder.UpdatedAt,
			&recordingsFolder.SizeBytes,
			&recordingsFolder.FilesCount,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to create recordings folder: %w", err)
		}

		// Заполняем остальные поля папки
		recordingsFolder.Name = domain.RecordingsFolderName
		recordingsFolder.OwnerID = userID
		recordingsFolder.ParentID = &rootFolder.ID // Устанавливаем parent_id на корневую папку
		recordingsFolder.Path = folderPath
		recordingsFolder.Level = rootFolder.Level + 1
		recordingsFolder.Metadata = []byte(metadata)

		log.Printf("Successfully created recordings folder with ID: %d", recordingsFolder.ID)

		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("failed to commit transaction: %w", err)
		}

		return &recordingsFolder, nil
	}

	// Если другая ошибка, возвращаем ее
	return nil, fmt.Errorf("failed to check recordings folder existence: %w", err)
}

func (r *RecordingRepository) UpdateFolderMetadata(ctx context.Context, folderID int64, deltaSize int64, deltaCount int) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Обновляем информацию о текущей папке и всех родительских папках
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
            files_count = f.files_count + $3,
            updated_at = CURRENT_TIMESTAMP
        WHERE f.id IN (SELECT id FROM folder_tree)`

	_, err = tx.ExecContext(ctx, updateQuery, folderID, deltaSize, deltaCount)
	if err != nil {
		return fmt.Errorf("failed to update folder metadata: %w", err)
	}

	return tx.Commit()
}

// Добавляем функцию для обновления размера файла в папке
func (r *RecordingRepository) UpdateRecordingFileSize(ctx context.Context, fileUUID uuid.UUID, newSize int64) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Получаем текущий размер файла и ID папки
	var oldSize int64
	var folderID int64

	err = tx.QueryRowContext(ctx, `
        SELECT size_bytes, folder_id FROM files WHERE uuid = $1
    `, fileUUID).Scan(&oldSize, &folderID)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	// Обновляем размер файла
	_, err = tx.ExecContext(ctx, `
        UPDATE files SET size_bytes = $1, updated_at = CURRENT_TIMESTAMP WHERE uuid = $2
    `, newSize, fileUUID)
	if err != nil {
		return fmt.Errorf("failed to update file size: %w", err)
	}

	// Обновляем размер текущей версии файла
	_, err = tx.ExecContext(ctx, `
        UPDATE file_versions 
        SET size_bytes = $1 
        WHERE file_uuid = $2 AND version_number = (
            SELECT current_version FROM files WHERE uuid = $2
        )
    `, newSize, fileUUID)
	if err != nil {
		return fmt.Errorf("failed to update file version size: %w", err)
	}

	// Вычисляем разницу в размере
	deltaSize := newSize - oldSize

	// Если размер изменился, обновляем метаданные папки
	if deltaSize != 0 {
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
                updated_at = CURRENT_TIMESTAMP
            WHERE f.id IN (SELECT id FROM folder_tree)`

		_, err = tx.ExecContext(ctx, updateQuery, folderID, deltaSize)
		if err != nil {
			return fmt.Errorf("failed to update folder size: %w", err)
		}
	}

	return tx.Commit()
}

func (r *RecordingRepository) updateRecordingsFolderStats(ctx context.Context, tx *sqlx.Tx, folderID int64) error {
	// Получаем актуальную информацию о размере и количестве файлов в папке и всех её подпапках
	var totalSize int64
	var fileCount int

	// Запрос для получения всех файлов в папке и всех её подпапках
	query := `
        WITH RECURSIVE folder_tree AS (
            -- Начальная папка
            SELECT id FROM folders WHERE id = $1 AND deleted_at IS NULL
            
            UNION ALL
            
            -- Все подпапки (рекурсивно)
            SELECT f.id
            FROM folders f
            INNER JOIN folder_tree ft ON f.parent_id = ft.id
            WHERE f.deleted_at IS NULL
        )
        SELECT 
            COALESCE(SUM(f.size_bytes), 0) as total_size,
            COUNT(f.uuid) as file_count
        FROM files f
        WHERE f.folder_id IN (SELECT id FROM folder_tree)
        AND f.deleted_at IS NULL
    `

	err := tx.QueryRowContext(ctx, query, folderID).Scan(&totalSize, &fileCount)
	if err != nil {
		return fmt.Errorf("failed to calculate folder stats: %w", err)
	}

	log.Printf("[updateRecordingsFolderStats] Folder %d calculated stats: size=%d bytes, files=%d",
		folderID, totalSize, fileCount)

	// Обновляем размер и количество файлов в папке
	_, err = tx.ExecContext(ctx, `
        UPDATE folders
        SET size_bytes = $1,
            files_count = $2,
            updated_at = CURRENT_TIMESTAMP
        WHERE id = $3
    `, totalSize, fileCount, folderID)

	if err != nil {
		return fmt.Errorf("failed to update folder stats: %w", err)
	}

	return nil
}
