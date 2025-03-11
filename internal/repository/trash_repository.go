package repository

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/jmoiron/sqlx"
	"log"
	"strconv"
	"strings"
	"synxrondrive/internal/domain"
	"time"
)

type TrashRepository struct {
	db *sqlx.DB
}

func NewTrashRepository(db *sqlx.DB) *TrashRepository {
	return &TrashRepository{db: db}
}

// GetSettings получает настройки корзины для пользователя
func (r *TrashRepository) GetSettings(ctx context.Context, ownerID string) (*domain.TrashSettings, error) {
	var settings domain.TrashSettings
	query := `SELECT * FROM trash_settings WHERE owner_id = $1`

	err := r.db.GetContext(ctx, &settings, query, ownerID)
	if err != nil {
		// Если настройки не найдены, создаем настройки по умолчанию
		if err := r.CreateDefaultSettings(ctx, ownerID); err != nil {
			return nil, fmt.Errorf("failed to create default settings: %w", err)
		}
		return r.GetSettings(ctx, ownerID)
	}

	// Проверяем, нужно ли конвертировать интервал PostgreSQL в Go-формат
	if settings.RetentionPeriod != "" && strings.Contains(settings.RetentionPeriod, ":") {
		parts := strings.Split(settings.RetentionPeriod, ":")
		if len(parts) == 3 {
			hours, errH := strconv.Atoi(parts[0])
			minutes, errM := strconv.Atoi(parts[1])
			seconds, errS := strconv.Atoi(parts[2])

			if errH == nil && errM == nil && errS == nil {
				// Создаем форматированную строку длительности для фронтенда
				if seconds == 0 && minutes == 0 {
					settings.RetentionPeriod = fmt.Sprintf("%dh", hours)
				} else if seconds == 0 {
					settings.RetentionPeriod = fmt.Sprintf("%dh%dm", hours, minutes)
				} else {
					settings.RetentionPeriod = fmt.Sprintf("%dh%dm%ds", hours, minutes, seconds)
				}
			}
		}
	}

	return &settings, nil
}

// CreateDefaultSettings создает настройки корзины по умолчанию
func (r *TrashRepository) CreateDefaultSettings(ctx context.Context, ownerID string) error {
	query := `
        INSERT INTO trash_settings (owner_id, retention_period)
        VALUES ($1, '01:00:00'::interval)
        ON CONFLICT (owner_id) DO NOTHING
    `
	_, err := r.db.ExecContext(ctx, query, ownerID)
	return err
}

// UpdateSettings обновляет настройки корзины
func (r *TrashRepository) UpdateSettings(ctx context.Context, settings *domain.TrashSettings) error {
	var intervalStr string

	// Проверяем, не является ли settings.RetentionPeriod уже форматом "HH:MM:SS"
	if strings.Contains(settings.RetentionPeriod, ":") {
		intervalStr = settings.RetentionPeriod
	} else {
		// Пытаемся парсить как Go duration
		duration, err := time.ParseDuration(settings.RetentionPeriod)
		if err != nil {
			return fmt.Errorf("invalid retention period format: %w", err)
		}

		// Конвертируем в формат PostgreSQL interval
		totalHours := int(duration.Hours())
		minutes := int(duration.Minutes()) % 60
		seconds := int(duration.Seconds()) % 60
		intervalStr = fmt.Sprintf("%02d:%02d:%02d", totalHours, minutes, seconds)
	}

	query := `
        UPDATE trash_settings
        SET retention_period = $1::interval
        WHERE owner_id = $2
        RETURNING updated_at
    `
	return r.db.QueryRowContext(ctx, query, intervalStr, settings.OwnerID).Scan(&settings.UpdatedAt)
}

// GetTrashItems получает все элементы в корзине пользователя
func (r *TrashRepository) GetTrashItems(ctx context.Context, ownerID string) ([]domain.TrashItem, error) {
	var items []domain.TrashItem

	query := `
        WITH folder_sizes AS (
            SELECT 
                f.id,
                COALESCE(SUM(fi.size_bytes), 0) as total_size
            FROM folders f
            LEFT JOIN files fi ON fi.folder_id = f.id
            WHERE f.deleted_at IS NOT NULL
            GROUP BY f.id
        ),
        deleted_folders AS (
            SELECT DISTINCT
                f.id::text as id,
                f.name,
                'folder' as type,
                f.path,
                COALESCE(fs.total_size, 0) as size,
                f.deleted_at,
                f.restore_path,
                f.restore_path as original_path,
                NULL::text as mime_type
            FROM folders f
            LEFT JOIN folder_sizes fs ON f.id = fs.id
            WHERE f.owner_id = $1 AND f.deleted_at IS NOT NULL
        ),
        deleted_files AS (
            SELECT 
                uuid::text as id,
                name,
                'file' as type,
                (SELECT path FROM folders WHERE id = folder_id) as path,
                size_bytes as size,
                deleted_at,
                (SELECT path FROM folders WHERE id = restore_folder_id) as restore_path,
                (SELECT path FROM folders WHERE id = restore_folder_id) as original_path,
                mime_type
            FROM files 
            WHERE owner_id = $1 AND deleted_at IS NOT NULL
        )
        SELECT 
            id, 
            name, 
            type, 
            path, 
            size, 
            deleted_at, 
            restore_path, 
            original_path,
            mime_type
        FROM deleted_folders
        UNION ALL
        SELECT 
            id, 
            name, 
            type, 
            path, 
            size, 
            deleted_at, 
            restore_path, 
            original_path,
            mime_type
        FROM deleted_files
        ORDER BY deleted_at DESC`

	err := r.db.SelectContext(ctx, &items, query, ownerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get trash items: %w", err)
	}

	settings, err := r.GetSettings(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get trash settings: %w", err)
	}

	// Получаем длительность периода хранения
	retentionHours := 1 * time.Hour
	if settings.RetentionPeriod != "" {
		// Сначала проверяем, соответствует ли формат PostgreSQL интервалу (часы:минуты:секунды)
		if strings.Contains(settings.RetentionPeriod, ":") {
			parts := strings.Split(settings.RetentionPeriod, ":")
			if len(parts) == 3 {
				hours, errH := strconv.Atoi(parts[0])
				minutes, errM := strconv.Atoi(parts[1])
				seconds, errS := strconv.Atoi(parts[2])

				if errH == nil && errM == nil && errS == nil {
					retentionHours = time.Duration(hours)*time.Hour +
						time.Duration(minutes)*time.Minute +
						time.Duration(seconds)*time.Second
				} else {
					log.Printf("warning: invalid PostgreSQL interval format: %v, using default", settings.RetentionPeriod)
				}
			} else {
				log.Printf("warning: invalid PostgreSQL interval format: %v, using default", settings.RetentionPeriod)
			}
		} else {
			// Пытаемся парсить как Go duration
			duration, err := time.ParseDuration(settings.RetentionPeriod)
			if err != nil {
				log.Printf("warning: invalid retention period format: %v, using default", err)
			} else {
				retentionHours = duration
			}
		}
	}

	now := time.Now()
	for i := range items {
		expiresAt := items[i].DeletedAt.Add(retentionHours)
		remaining := expiresAt.Sub(now)

		if remaining < 0 {
			items[i].ExpiresIn = "скоро будет удалено"
		} else {
			items[i].ExpiresIn = formatDuration(remaining)
		}
	}

	return items, nil
}

// EmptyTrash полностью очищает корзину пользователя
func (r *TrashRepository) EmptyTrash(ctx context.Context, ownerID string) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Сначала удаляем превью файлов
	_, err = tx.ExecContext(ctx, `
        DELETE FROM file_previews 
        WHERE file_uuid IN (
            SELECT uuid FROM files 
            WHERE owner_id = $1 AND deleted_at IS NOT NULL
        )
    `, ownerID)
	if err != nil {
		return fmt.Errorf("failed to delete file previews: %w", err)
	}

	// Затем удаляем версии файлов
	_, err = tx.ExecContext(ctx, `
        DELETE FROM file_versions 
        WHERE file_uuid IN (
            SELECT uuid FROM files 
            WHERE owner_id = $1 AND deleted_at IS NOT NULL
        )
    `, ownerID)
	if err != nil {
		return fmt.Errorf("failed to delete file versions: %w", err)
	}

	// Теперь удаляем файлы
	_, err = tx.ExecContext(ctx, `
        DELETE FROM files 
        WHERE owner_id = $1 AND deleted_at IS NOT NULL
    `, ownerID)
	if err != nil {
		return fmt.Errorf("failed to delete files from trash: %w", err)
	}

	// И наконец удаляем папки
	_, err = tx.ExecContext(ctx, `
        DELETE FROM folders 
        WHERE owner_id = $1 AND deleted_at IS NOT NULL
    `, ownerID)
	if err != nil {
		return fmt.Errorf("failed to delete folders from trash: %w", err)
	}

	return tx.Commit()
}

// RestoreItem восстанавливает элемент из корзины
func (r *TrashRepository) RestoreItem(ctx context.Context, itemID string, itemType string, ownerID string) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if itemType == "file" {
		// Сначала получаем информацию о файле
		var file domain.File
		err = tx.GetContext(ctx, &file, "SELECT * FROM files WHERE uuid = $1", itemID)
		if err != nil {
			return fmt.Errorf("failed to get file info: %w", err)
		}

		// Обновляем метаданные папок (увеличиваем размер и количество файлов)
		updateFoldersQuery := `
            WITH RECURSIVE folder_tree AS (
                -- Получаем папку, в которую восстанавливаем файл
                SELECT id, parent_id
                FROM folders 
                WHERE id = (
                    SELECT restore_folder_id 
                    FROM files 
                    WHERE uuid = $1
                )
                
                UNION ALL
                
                -- Получаем все родительские папки
                SELECT f.id, f.parent_id
                FROM folders f
                INNER JOIN folder_tree ft ON f.id = ft.parent_id
            )
            UPDATE folders f
            SET 
                size_bytes = f.size_bytes + $2,
                files_count = f.files_count + 1,
                updated_at = CURRENT_TIMESTAMP
            FROM folder_tree ft
            WHERE f.id = ft.id
        `

		_, err = tx.ExecContext(ctx, updateFoldersQuery, itemID, file.SizeBytes)
		if err != nil {
			return fmt.Errorf("failed to update folder metadata: %w", err)
		}

		// Восстанавливаем файл
		restoreFileQuery := `
            UPDATE files
            SET 
                deleted_at = NULL,
                folder_id = restore_folder_id,
                restore_folder_id = NULL,
                restore_path = NULL
            WHERE uuid = $1 
            AND owner_id = $2 
            AND deleted_at IS NOT NULL
            RETURNING uuid
        `

		var restoredUUID string
		err = tx.QueryRowContext(ctx, restoreFileQuery, itemID, ownerID).Scan(&restoredUUID)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("file not found or already restored")
			}
			return fmt.Errorf("failed to restore file: %w", err)
		}

	} else {
		// Восстанавливаем папку и все её подпапки
		restoreFolderQuery := `
            WITH RECURSIVE subfolder AS (
                -- Базовая папка
                SELECT 
                    id, 
                    parent_id, 
                    restore_parent_id, 
                    path, 
                    restore_path,
                    size_bytes,
                    files_count
                FROM folders
                WHERE id = $1::bigint 
                AND owner_id = $2 
                AND deleted_at IS NOT NULL

                UNION ALL

                -- Все подпапки
                SELECT 
                    f.id, 
                    f.parent_id, 
                    f.restore_parent_id, 
                    f.path, 
                    f.restore_path,
                    f.size_bytes,
                    f.files_count
                FROM folders f
                INNER JOIN subfolder s ON f.parent_id = s.id
                WHERE f.owner_id = $2 
                AND f.deleted_at IS NOT NULL
            )
            UPDATE folders f
            SET 
                deleted_at = NULL,
                parent_id = s.restore_parent_id,
                path = s.restore_path,
                restore_parent_id = NULL,
                restore_path = NULL,
                updated_at = CURRENT_TIMESTAMP
            FROM subfolder s
            WHERE f.id = s.id
            RETURNING f.id
        `

		rows, err := tx.QueryContext(ctx, restoreFolderQuery, itemID, ownerID)
		if err != nil {
			return fmt.Errorf("failed to restore folder: %w", err)
		}
		defer rows.Close()

		// Проверяем, была ли восстановлена хотя бы одна папка
		restoredAny := false
		for rows.Next() {
			restoredAny = true
			var id int64
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("failed to scan restored folder id: %w", err)
			}
		}
		if err = rows.Err(); err != nil {
			return fmt.Errorf("error iterating over restored folders: %w", err)
		}

		if !restoredAny {
			return fmt.Errorf("folder not found or already restored")
		}

		// Обновляем метаданные родительских папок
		updateParentFoldersQuery := `
            WITH RECURSIVE parent_folders AS (
                -- Получаем родительскую папку
                SELECT 
                    id, 
                    parent_id,
                    (
                        SELECT COALESCE(SUM(size_bytes), 0)
                        FROM folders
                        WHERE parent_id = f.id AND deleted_at IS NULL
                    ) as children_size,
                    (
                        SELECT COALESCE(SUM(files_count), 0)
                        FROM folders
                        WHERE parent_id = f.id AND deleted_at IS NULL
                    ) as children_files
                FROM folders f
                WHERE id = (
                    SELECT restore_parent_id
                    FROM folders
                    WHERE id = $1::bigint
                )

                UNION ALL

                SELECT 
                    f.id, 
                    f.parent_id,
                    (
                        SELECT COALESCE(SUM(size_bytes), 0)
                        FROM folders
                        WHERE parent_id = f.id AND deleted_at IS NULL
                    ) as children_size,
                    (
                        SELECT COALESCE(SUM(files_count), 0)
                        FROM folders
                        WHERE parent_id = f.id AND deleted_at IS NULL
                    ) as children_files
                FROM folders f
                INNER JOIN parent_folders pf ON f.id = pf.parent_id
            )
            UPDATE folders f
            SET 
                size_bytes = pf.children_size,
                files_count = pf.children_files,
                updated_at = CURRENT_TIMESTAMP
            FROM parent_folders pf
            WHERE f.id = pf.id
        `

		_, err = tx.ExecContext(ctx, updateParentFoldersQuery, itemID)
		if err != nil {
			return fmt.Errorf("failed to update parent folders metadata: %w", err)
		}
	}

	return tx.Commit()
}

// DeleteItemPermanently окончательно удаляет элемент из корзины
func (r *TrashRepository) DeleteItemPermanently(ctx context.Context, itemID string, itemType string, ownerID string) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if itemType == "file" {
		// Сначала удаляем запись из recordings, если она есть
		_, err = tx.ExecContext(ctx, `
            DELETE FROM recordings 
            WHERE file_uuid = $1
        `, itemID)
		if err != nil {
			return fmt.Errorf("failed to delete recording: %w", err)
		}

		// Затем удаляем превью файла
		_, err = tx.ExecContext(ctx, `
            DELETE FROM file_previews 
            WHERE file_uuid = $1
        `, itemID)
		if err != nil {
			return fmt.Errorf("failed to delete file preview: %w", err)
		}

		// Затем удаляем версии файла
		_, err = tx.ExecContext(ctx, `
            DELETE FROM file_versions 
            WHERE file_uuid = $1
        `, itemID)
		if err != nil {
			return fmt.Errorf("failed to delete file versions: %w", err)
		}

		// И наконец удаляем сам файл
		query := `DELETE FROM files WHERE uuid = $1 AND owner_id = $2 AND deleted_at IS NOT NULL`
		result, err := tx.ExecContext(ctx, query, itemID, ownerID)
		if err != nil {
			return fmt.Errorf("failed to delete file permanently: %w", err)
		}

		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to get affected rows: %w", err)
		}

		if rows == 0 {
			return fmt.Errorf("file not found in trash")
		}
	} else {
		// Для удаления папки сначала получаем список всех файлов в этой папке и её подпапках
		var fileUUIDs []string
		fileQuery := `
            WITH RECURSIVE subfolder AS (
                SELECT id FROM folders
                WHERE id = $1::bigint AND owner_id = $2 AND deleted_at IS NOT NULL
                UNION
                SELECT f.id FROM folders f
                INNER JOIN subfolder s ON f.parent_id = s.id
                WHERE f.owner_id = $2 AND f.deleted_at IS NOT NULL
            )
            SELECT uuid::text FROM files
            WHERE folder_id IN (SELECT id FROM subfolder)
            AND deleted_at IS NOT NULL
        `
		err := tx.SelectContext(ctx, &fileUUIDs, fileQuery, itemID, ownerID)
		if err != nil {
			return fmt.Errorf("failed to get files in folder: %w", err)
		}

		// Удаляем файлы и связанные записи
		if len(fileUUIDs) > 0 {
			if err := r.deleteFileRelatedRecords(ctx, tx, fileUUIDs, 1000); err != nil {
				return fmt.Errorf("failed to delete files and related records in folder: %w", err)
			}
		}

		// Удаляем саму папку и её подпапки
		folderQuery := `
            WITH RECURSIVE subfolder AS (
                SELECT id FROM folders
                WHERE id = $1::bigint AND owner_id = $2 AND deleted_at IS NOT NULL
                UNION
                SELECT f.id FROM folders f
                INNER JOIN subfolder s ON f.parent_id = s.id
                WHERE f.owner_id = $2 AND f.deleted_at IS NOT NULL
            )
            DELETE FROM folders WHERE id IN (SELECT id FROM subfolder)
        `
		result, err := tx.ExecContext(ctx, folderQuery, itemID, ownerID)
		if err != nil {
			return fmt.Errorf("failed to delete folder permanently: %w", err)
		}

		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to get affected rows: %w", err)
		}

		if rows == 0 {
			return fmt.Errorf("folder not found in trash")
		}
	}

	return tx.Commit()
}

// MoveToTrash перемещает элемент в корзину
func (r *TrashRepository) MoveToTrash(ctx context.Context, itemID string, itemType string, ownerID string) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()

	if itemType == "file" {
		// Сначала получаем информацию о файле для обновления метаданных папки
		var file struct {
			FolderID  int64 `db:"folder_id"`
			SizeBytes int64 `db:"size_bytes"`
		}
		err := tx.GetContext(ctx, &file,
			`SELECT folder_id, size_bytes FROM files WHERE uuid = $1 AND owner_id = $2`,
			itemID, ownerID)
		if err != nil {
			return fmt.Errorf("failed to get file info: %w", err)
		}

		// Обновляем метаданные папок (уменьшаем размер и количество файлов)
		updateFoldersQuery := `
            WITH RECURSIVE folder_tree AS (
                -- Начальная папка
                SELECT id, parent_id
                FROM folders 
                WHERE id = $1
                
                UNION ALL
                
                -- Все родительские папки
                SELECT f.id, f.parent_id
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

		_, err = tx.ExecContext(ctx, updateFoldersQuery, file.FolderID, file.SizeBytes)
		if err != nil {
			return fmt.Errorf("failed to update folder metadata: %w", err)
		}

		// Перемещаем файл в корзину
		query := `
            UPDATE files
            SET deleted_at = $3,
                restore_folder_id = folder_id,
                restore_path = (
                    SELECT path FROM folders WHERE id = folder_id
                )
            WHERE uuid = $1 AND owner_id = $2 AND deleted_at IS NULL
            RETURNING uuid`

		result, err := tx.ExecContext(ctx, query, itemID, ownerID, now)
		if err != nil {
			return fmt.Errorf("failed to move file to trash: %w", err)
		}

		rows, err := result.RowsAffected()
		if err != nil || rows == 0 {
			return fmt.Errorf("file not found or already in trash")
		}

	} else {
		// Для папок оставляем текущую логику
		query := `
            WITH RECURSIVE subfolder AS (
                SELECT id, parent_id, path
                FROM folders
                WHERE id = $1::bigint AND owner_id = $2 AND deleted_at IS NULL
                UNION ALL
                SELECT f.id, f.parent_id, f.path
                FROM folders f
                INNER JOIN subfolder s ON f.parent_id = s.id
                WHERE f.owner_id = $2 AND f.deleted_at IS NULL
            )
            UPDATE folders f
            SET deleted_at = $3,
                restore_parent_id = parent_id,
                restore_path = f.path
            FROM subfolder s
            WHERE f.id = s.id
            RETURNING f.id`

		rows, err := tx.QueryContext(ctx, query, itemID, ownerID, now)
		if err != nil {
			return fmt.Errorf("failed to move folder to trash: %w", err)
		}
		defer rows.Close()

		if !rows.Next() {
			return fmt.Errorf("folder not found or already in trash")
		}
	}

	return tx.Commit()
}

// formatDuration форматирует продолжительность в человекочитаемый формат
func formatDuration(d time.Duration) string {
	days := d / (24 * time.Hour)
	hours := (d % (24 * time.Hour)) / time.Hour
	minutes := (d % time.Hour) / time.Minute

	if days > 0 {
		return fmt.Sprintf("%d дней %d часов", days, hours)
	} else if hours > 0 {
		return fmt.Sprintf("%d часов %d минут", hours, minutes)
	} else {
		return fmt.Sprintf("%d минут", minutes)
	}
}

// RunCleanup запускает процедуру очистки корзины в базе данных
func (r *TrashRepository) RunCleanup(ctx context.Context) ([]domain.DeleteInfo, error) {
	// Начинаем транзакцию для атомарного удаления данных
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Находим файлы, подлежащие удалению
	var filesToDelete []domain.DeleteInfo
	findQuery := `
        SELECT f.uuid::text, f.owner_id, f.name, f.size_bytes
        FROM files f
        JOIN trash_settings ts ON f.owner_id = ts.owner_id
        WHERE f.deleted_at IS NOT NULL
        AND f.deleted_at + ts.retention_period::interval < CURRENT_TIMESTAMP
    `

	type ExtendedDeleteInfo struct {
		domain.DeleteInfo
		Name      string `db:"name"`
		SizeBytes int64  `db:"size_bytes"`
	}

	var extendedInfo []ExtendedDeleteInfo
	err = tx.SelectContext(ctx, &extendedInfo, findQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to find files for cleanup: %w", err)
	}

	// Преобразуем в стандартный формат DeleteInfo
	filesToDelete = make([]domain.DeleteInfo, len(extendedInfo))
	for i, info := range extendedInfo {
		filesToDelete[i] = info.DeleteInfo
	}

	// Если нет файлов для удаления, просто возвращаем пустой результат
	if len(filesToDelete) == 0 {
		return []domain.DeleteInfo{}, nil
	}

	// Собираем UUID файлов для использования в запросах
	var fileUUIDs []string
	for _, file := range filesToDelete {
		fileUUIDs = append(fileUUIDs, file.UUID)
	}

	// Удаляем связанные записи
	if err := r.deleteFileRelatedRecords(ctx, tx, fileUUIDs, 1000); err != nil {
		return nil, fmt.Errorf("failed to run database cleanup: %w", err)
	}

	// Фиксируем транзакцию
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Сохраняем дополнительную информацию о файлах (имя и размер)
	result := make([]domain.DeleteInfo, len(extendedInfo))
	for i, info := range extendedInfo {
		result[i] = domain.DeleteInfo{
			UUID:    info.UUID,
			OwnerID: info.OwnerID,
			Name:    info.Name,
		}
	}

	return result, nil
}

func (r *TrashRepository) BeginTx(ctx context.Context) (*sqlx.Tx, error) {
	return r.db.BeginTxx(ctx, nil)
}

// deleteFileRelatedRecords удаляет записи, связанные с файлами
func (r *TrashRepository) deleteFileRelatedRecords(ctx context.Context, tx *sqlx.Tx, fileUUIDs []string, batchSize int) error {
	// Если список пустой, нечего делать
	if len(fileUUIDs) == 0 {
		return nil
	}

	// Разбиваем на батчи для оптимизации
	batches := make([][]string, 0, (len(fileUUIDs)+batchSize-1)/batchSize)
	for i := 0; i < len(fileUUIDs); i += batchSize {
		end := i + batchSize
		if end > len(fileUUIDs) {
			end = len(fileUUIDs)
		}
		batches = append(batches, fileUUIDs[i:end])
	}

	// Обрабатываем каждый батч
	for _, batch := range batches {
		// Формируем строку с UUID для SQL запроса
		uuidPlaceholders := make([]string, len(batch))
		uuidArgs := make([]interface{}, len(batch))
		for i, uuid := range batch {
			uuidPlaceholders[i] = fmt.Sprintf("$%d", i+1)
			uuidArgs[i] = uuid
		}
		uuidList := strings.Join(uuidPlaceholders, ", ")

		// 1. Сначала удаляем записи из recordings
		deleteRecordingsQuery := fmt.Sprintf(`
            DELETE FROM recordings 
            WHERE file_uuid::text IN (%s)
        `, uuidList)
		_, err := tx.ExecContext(ctx, deleteRecordingsQuery, uuidArgs...)
		if err != nil {
			return fmt.Errorf("failed to delete recordings: %w", err)
		}

		// 2. Затем удаляем превью
		deletePreviewsQuery := fmt.Sprintf(`
            DELETE FROM file_previews 
            WHERE file_uuid::text IN (%s)
        `, uuidList)
		_, err = tx.ExecContext(ctx, deletePreviewsQuery, uuidArgs...)
		if err != nil {
			return fmt.Errorf("failed to delete file previews: %w", err)
		}

		// 3. Затем удаляем версии файлов
		deleteVersionsQuery := fmt.Sprintf(`
            DELETE FROM file_versions 
            WHERE file_uuid::text IN (%s)
        `, uuidList)
		_, err = tx.ExecContext(ctx, deleteVersionsQuery, uuidArgs...)
		if err != nil {
			return fmt.Errorf("failed to delete file versions: %w", err)
		}

		// 4. И наконец удаляем сами файлы
		deleteFilesQuery := fmt.Sprintf(`
            DELETE FROM files 
            WHERE uuid::text IN (%s)
        `, uuidList)
		_, err = tx.ExecContext(ctx, deleteFilesQuery, uuidArgs...)
		if err != nil {
			return fmt.Errorf("failed to delete files: %w", err)
		}
	}

	return nil
}
