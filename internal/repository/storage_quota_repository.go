package repository

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/jmoiron/sqlx"
	"log"
	"synxrondrive/internal/domain"
)

type StorageQuotaRepository struct {
	db *sqlx.DB
}

func NewStorageQuotaRepository(db *sqlx.DB) *StorageQuotaRepository {
	return &StorageQuotaRepository{db: db}
}

func (r *StorageQuotaRepository) GetQuota(ctx context.Context, ownerID string) (*domain.StorageQuota, error) {
	var quota domain.StorageQuota

	err := r.db.GetContext(ctx, &quota,
		`SELECT * FROM storage_quotas WHERE owner_id = $1`,
		ownerID)

	if err != nil {
		// Если квота не найдена, создаем новую с дефолтным лимитом
		if err == sql.ErrNoRows {
			quota = domain.StorageQuota{
				OwnerID:         ownerID,
				TotalBytesLimit: 5368709120, // 5GB
				UsedBytes:       0,
			}

			err = r.Create(ctx, &quota)
			if err != nil {
				return nil, fmt.Errorf("failed to create quota: %w", err)
			}
			return &quota, nil
		}
		return nil, fmt.Errorf("failed to get quota: %w", err)
	}

	return &quota, nil
}

func (r *StorageQuotaRepository) Create(ctx context.Context, quota *domain.StorageQuota) error {
	query := `
        INSERT INTO storage_quotas (owner_id, total_bytes_limit, used_bytes)
        VALUES ($1, $2, $3)
        RETURNING id, created_at, updated_at`

	return r.db.QueryRowContext(ctx, query,
		quota.OwnerID,
		quota.TotalBytesLimit,
		quota.UsedBytes,
	).Scan(&quota.ID, &quota.CreatedAt, &quota.UpdatedAt)
}

func (r *StorageQuotaRepository) UpdateUsedSpace(ctx context.Context, ownerID string, deltaBytes int64) error {
	query := `
        UPDATE storage_quotas 
        SET used_bytes = GREATEST(0, used_bytes + $1),
            updated_at = CURRENT_TIMESTAMP
        WHERE owner_id = $2`

	result, err := r.db.ExecContext(ctx, query, deltaBytes, ownerID)
	if err != nil {
		return fmt.Errorf("failed to update used space: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("quota not found for owner: %s", ownerID)
	}

	return nil
}

func (r *StorageQuotaRepository) UpdateQuotaLimit(ctx context.Context, ownerID string, newLimit int64) error {
	query := `
        UPDATE storage_quotas 
        SET total_bytes_limit = $1,
            updated_at = CURRENT_TIMESTAMP
        WHERE owner_id = $2`

	result, err := r.db.ExecContext(ctx, query, newLimit, ownerID)
	if err != nil {
		return fmt.Errorf("failed to update quota limit: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("quota not found for owner: %s", ownerID)
	}

	return nil
}

func (r *StorageQuotaRepository) CalculateAndUpdateUsedSpace(ctx context.Context, ownerID string) error {
	// Более полный запрос, который учитывает и обычные файлы, и записи конференций
	query := `
    WITH all_file_sizes AS (
        SELECT 
            f.owner_id,
            COALESCE(SUM(
                CASE 
                    WHEN r.file_uuid IS NOT NULL THEN f.size_bytes -- Учитываем размер для файлов записей
                    ELSE 
                        (SELECT fv.size_bytes 
                         FROM file_versions fv 
                         WHERE fv.file_uuid = f.uuid 
                         AND fv.version_number = f.current_version 
                         AND fv.deleted_at IS NULL
                         LIMIT 1)
                END
            ), 0) as total_size
        FROM files f
        LEFT JOIN recordings r ON f.uuid = r.file_uuid -- Добавляем связь с записями
        WHERE f.deleted_at IS NULL 
        AND f.owner_id = $1
        GROUP BY f.owner_id
    )
    UPDATE storage_quotas sq
    SET used_bytes = afs.total_size,
        updated_at = CURRENT_TIMESTAMP
    FROM all_file_sizes afs 
    WHERE sq.owner_id = afs.owner_id`

	log.Printf("[QuotaRepository] Выполняем расчет используемого пространства для пользователя %s", ownerID)

	result, err := r.db.ExecContext(ctx, query, ownerID)
	if err != nil {
		log.Printf("[QuotaRepository] Ошибка при обновлении используемого пространства: %v", err)
		return fmt.Errorf("failed to update used space: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	log.Printf("[QuotaRepository] Обновлено квот: %d", rows)

	if rows == 0 {
		// Если нет записи квоты, создаём новую
		quota, err := r.GetQuota(ctx, ownerID)
		if err != nil {
			return fmt.Errorf("failed to get or create quota: %w", err)
		}

		// Пересчитываем использованное пространство для новой квоты
		var usedBytes int64
		err = r.db.QueryRowContext(ctx, `
            WITH all_file_sizes AS (
                SELECT 
                    COALESCE(SUM(
                        CASE 
                            WHEN r.file_uuid IS NOT NULL THEN f.size_bytes -- Учитываем размер для файлов записей
                            ELSE 
                                (SELECT fv.size_bytes 
                                 FROM file_versions fv 
                                 WHERE fv.file_uuid = f.uuid 
                                 AND fv.version_number = f.current_version 
                                 AND fv.deleted_at IS NULL
                                 LIMIT 1)
                        END
                    ), 0) as total_size
                FROM files f
                LEFT JOIN recordings r ON f.uuid = r.file_uuid -- Добавляем записи
                WHERE f.deleted_at IS NULL 
                AND f.owner_id = $1
            )
            SELECT total_size FROM all_file_sizes
        `, ownerID).Scan(&usedBytes)

		if err != nil {
			log.Printf("[QuotaRepository] Ошибка при расчете пространства: %v", err)
			return fmt.Errorf("failed to calculate used space: %w", err)
		}

		log.Printf("[QuotaRepository] Рассчитано использованное пространство: %d байт", usedBytes)

		// Обновляем использованное пространство
		quota.UsedBytes = usedBytes
		err = r.Update(ctx, quota)
		if err != nil {
			return fmt.Errorf("failed to update quota: %w", err)
		}

		log.Printf("[QuotaRepository] Обновлена квота для пользователя %s: используется %d из %d байт",
			ownerID, quota.UsedBytes, quota.TotalBytesLimit)
	}

	return nil
}

func (r *StorageQuotaRepository) Update(ctx context.Context, quota *domain.StorageQuota) error {
	query := `
        UPDATE storage_quotas
        SET used_bytes = $1,
            total_bytes_limit = $2,
            updated_at = CURRENT_TIMESTAMP
        WHERE owner_id = $3
        RETURNING id, created_at, updated_at`

	return r.db.QueryRowContext(ctx, query,
		quota.UsedBytes,
		quota.TotalBytesLimit,
		quota.OwnerID,
	).Scan(&quota.ID, &quota.CreatedAt, &quota.UpdatedAt)
}
