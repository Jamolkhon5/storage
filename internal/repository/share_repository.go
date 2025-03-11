package repository

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/jmoiron/sqlx"
	"strconv"
	"strings"
	"synxrondrive/internal/domain"
	"time"
)

type ShareRepository struct {
	db *sqlx.DB
}

func NewShareRepository(db *sqlx.DB) *ShareRepository {
	return &ShareRepository{db: db}
}

func (r *ShareRepository) Create(ctx context.Context, share *domain.Share) error {
	query := `
        INSERT INTO shares (
            id, resource_id, resource_type, owner_id, 
            access_type, token, expires_at, created_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, CURRENT_TIMESTAMP
        ) RETURNING created_at`

	return r.db.QueryRowContext(
		ctx,
		query,
		share.ID,
		share.ResourceID,
		share.ResourceType,
		share.OwnerID,
		share.AccessType,
		share.Token,
		share.ExpiresAt,
	).Scan(&share.CreatedAt)
}

func (r *ShareRepository) GetByToken(ctx context.Context, token string) (*domain.Share, error) {
	query := `
        SELECT * FROM shares 
        WHERE token = $1 
        AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)`

	var share domain.Share
	if err := r.db.GetContext(ctx, &share, query, token); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("share not found or expired")
		}
		return nil, err
	}

	return &share, nil
}

func (r *ShareRepository) DeleteExpired(ctx context.Context) error {
	query := `DELETE FROM shares WHERE expires_at < CURRENT_TIMESTAMP`
	_, err := r.db.ExecContext(ctx, query)
	return err
}

func (r *ShareRepository) UpdateExpiration(ctx context.Context, shareID string, expiresAt *time.Time) error {
	query := `
        UPDATE shares 
        SET expires_at = $1, updated_at = CURRENT_TIMESTAMP
        WHERE id = $2`

	result, err := r.db.ExecContext(ctx, query, expiresAt, shareID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return fmt.Errorf("share not found")
	}

	return nil
}

func (r *ShareRepository) GetSharesByResource(ctx context.Context, resourceID string, resourceType domain.ResourceType) ([]domain.Share, error) {
	query := `
        SELECT * FROM shares 
        WHERE resource_id = $1 
        AND resource_type = $2
        AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)`

	var shares []domain.Share
	err := r.db.SelectContext(ctx, &shares, query, resourceID, resourceType)
	if err != nil {
		return nil, err
	}

	return shares, nil
}

func (r *ShareRepository) DeleteShare(ctx context.Context, shareID string, ownerID string) error {
	query := `DELETE FROM shares WHERE id = $1 AND owner_id = $2`

	result, err := r.db.ExecContext(ctx, query, shareID, ownerID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return fmt.Errorf("share not found or access denied")
	}

	return nil
}

// AddUserToShare добавляет пользователя к общему ресурсу
func (r *ShareRepository) AddUserToShare(ctx context.Context, shareID string, userID string) error {
	query := `
        UPDATE shares 
        SET user_ids = 
            CASE 
                WHEN user_ids = '' THEN $2
                WHEN position($2 IN user_ids) = 0 THEN user_ids || ',' || $2
                ELSE user_ids 
            END
        WHERE id = $1
    `
	result, err := r.db.ExecContext(ctx, query, shareID, userID)
	if err != nil {
		return fmt.Errorf("failed to add user to share: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return fmt.Errorf("share not found")
	}

	return nil
}

// GetUserShares возвращает все активные ссылки пользователя
func (r *ShareRepository) GetUserShares(ctx context.Context, userID string) ([]domain.Share, error) {
	query := `
        SELECT * FROM shares 
        WHERE (user_ids LIKE '%' || $1 || '%' AND owner_id != $1)  -- Добавляем проверку owner_id != userID
        AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
        ORDER BY created_at DESC
    `

	var shares []domain.Share
	err := r.db.SelectContext(ctx, &shares, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user shares: %w", err)
	}

	return shares, nil
}

// GetShareContent получает содержимое общего ресурса с учетом пути
func (r *ShareRepository) GetShareContent(ctx context.Context, shareID string, path string) (*domain.SharedContent, error) {
	// Получаем информацию о share
	share := &domain.Share{}
	err := r.db.GetContext(ctx, share, "SELECT * FROM shares WHERE id = $1", shareID)
	if err != nil {
		return nil, fmt.Errorf("failed to get share: %w", err)
	}

	content := &domain.SharedContent{
		Share:        *share,
		ResourceType: share.ResourceType,
		AccessType:   share.AccessType,
		Path:         path,
	}

	// В зависимости от типа ресурса получаем содержимое
	switch share.ResourceType {
	case domain.ResourceTypeFolder:
		return r.getFolderContent(ctx, share, path, content)
	case domain.ResourceTypeFile:
		return r.getFileContent(ctx, share, content)
	default:
		return nil, fmt.Errorf("unsupported resource type")
	}
}

// Вспомогательные методы для получения содержимого
func (r *ShareRepository) getFolderContent(ctx context.Context, share *domain.Share, path string, content *domain.SharedContent) (*domain.SharedContent, error) {
	// Извлекаем ID папки из пути
	var folderID int64
	if path == "/" {
		var err error
		folderID, err = strconv.ParseInt(share.ResourceID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid folder ID in share: %w", err)
		}
	} else {
		folderIDStr := strings.TrimPrefix(path, "/folders/")
		var err error
		folderID, err = strconv.ParseInt(folderIDStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid folder ID in path: %w", err)
		}
	}

	// Получаем корневую папку шаринга
	shareRootID, err := strconv.ParseInt(share.ResourceID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid share root ID: %w", err)
	}

	// Получаем текущую папку
	var currentFolder domain.Folder
	err = r.db.GetContext(ctx, &currentFolder,
		"SELECT * FROM folders WHERE id = $1 AND deleted_at IS NULL",
		folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}

	content.CurrentFolder = &currentFolder

	// Получаем родительские папки
	parentFolders, err := r.getParentFolders(ctx, folderID, shareRootID)
	if err != nil {
		return nil, fmt.Errorf("failed to get parent folders: %w", err)
	}
	content.ParentFolders = parentFolders

	// Получаем подпапки с рекурсивным подсчетом размера и количества файлов
	query := `
        WITH RECURSIVE folder_tree AS (
            -- Базовые папки
            SELECT 
                f.id, f.name, f.owner_id, f.parent_id, f.path, f.level,
                f.size_bytes, f.files_count, f.created_at, f.updated_at, f.deleted_at
            FROM folders f
            WHERE f.parent_id = $1 AND f.deleted_at IS NULL

            UNION ALL

            -- Рекурсивная часть для подпапок
            SELECT 
                f.id, f.name, f.owner_id, f.parent_id, f.path, f.level,
                f.size_bytes, f.files_count, f.created_at, f.updated_at, f.deleted_at
            FROM folders f
            JOIN folder_tree ft ON f.parent_id = ft.id
            WHERE f.deleted_at IS NULL
        )
        SELECT DISTINCT ON (id)
            id, name, owner_id, parent_id, path, level,
            (
                SELECT COALESCE(SUM(size_bytes), 0)
                FROM folder_tree ft2
                WHERE ft2.path LIKE folder_tree.path || '%'
            ) as size_bytes,
            (
                SELECT COALESCE(SUM(files_count), 0)
                FROM folder_tree ft2
                WHERE ft2.path LIKE folder_tree.path || '%'
            ) as files_count,
            created_at, updated_at, deleted_at
        FROM folder_tree
        WHERE parent_id = $1
        ORDER BY id, path;
    `

	err = r.db.SelectContext(ctx, &content.Subfolders, query, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get subfolders: %w", err)
	}

	// Получаем файлы
	err = r.db.SelectContext(ctx, &content.Files,
		`SELECT * FROM files 
         WHERE folder_id = $1
         AND deleted_at IS NULL 
         ORDER BY name`,
		folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get files: %w", err)
	}

	return content, nil
}

func (r *ShareRepository) getFileContent(ctx context.Context, share *domain.Share, content *domain.SharedContent) (*domain.SharedContent, error) {
	var file domain.File
	err := r.db.GetContext(ctx, &file,
		"SELECT * FROM files WHERE uuid = $1 AND deleted_at IS NULL",
		share.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	content.Files = []domain.File{file}
	return content, nil
}

func (r *ShareRepository) GetByID(ctx context.Context, shareID string) (*domain.Share, error) {
	var share domain.Share
	query := `SELECT * FROM shares WHERE id = $1`
	err := r.db.GetContext(ctx, &share, query, shareID)
	if err != nil {
		return nil, fmt.Errorf("failed to get share by id: %w", err)
	}
	return &share, nil
}

func (r *ShareRepository) getParentFolders(ctx context.Context, folderID int64, shareRootID int64) ([]domain.Folder, error) {
	query := `
        WITH RECURSIVE parent_folders AS (
            -- Начальная папка
            SELECT id, name, owner_id, parent_id, path, level 
            FROM folders 
            WHERE id = $1 AND deleted_at IS NULL
            
            UNION ALL
            
            -- Родительские папки до корня шаринга
            SELECT f.id, f.name, f.owner_id, f.parent_id, f.path, f.level
            FROM folders f
            INNER JOIN parent_folders pf ON f.id = pf.parent_id
            WHERE f.deleted_at IS NULL AND f.id >= $2  -- Останавливаемся на корне шаринга
        )
        SELECT * FROM parent_folders 
        WHERE id != $1 AND id >= $2  -- Исключаем текущую папку и папки выше корня шаринга
        ORDER BY level ASC
    `

	var folders []domain.Folder
	err := r.db.SelectContext(ctx, &folders, query, folderID, shareRootID)
	if err != nil {
		return nil, fmt.Errorf("failed to get parent folders: %w", err)
	}

	return folders, nil
}

func (r *ShareRepository) GetExistingShare(
	ctx context.Context,
	resourceID string,
	resourceType domain.ResourceType,
	ownerID string,
	accessType domain.AccessType,
	expiresAt *time.Time,
) (*domain.Share, error) {
	query := `
        SELECT * FROM shares 
        WHERE resource_id = $1 
        AND resource_type = $2 
        AND owner_id = $3 
        AND access_type = $4 
        AND (
            ($5 IS NULL AND expires_at IS NULL) OR 
            ($5 IS NOT NULL AND expires_at = $5)
        )
        AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
        ORDER BY created_at DESC 
        LIMIT 1
    `

	var share domain.Share
	err := r.db.GetContext(ctx, &share, query, resourceID, resourceType, ownerID, accessType, expiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &share, nil
}

func (r *ShareRepository) GetSharedFolderStructure(ctx context.Context, shareID string) ([]domain.Folder, error) {
	// Получаем информацию о share
	share, err := r.GetByID(ctx, shareID)
	if err != nil {
		return nil, fmt.Errorf("failed to get share: %w", err)
	}

	// Проверяем, что это папка
	if share.ResourceType != domain.ResourceTypeFolder {
		return nil, fmt.Errorf("resource is not a folder")
	}

	// Получаем ID корневой shared папки
	rootFolderID, err := strconv.ParseInt(share.ResourceID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid folder ID: %w", err)
	}

	// Получаем все подпапки рекурсивно
	query := `
        WITH RECURSIVE folder_tree AS (
            -- Базовая папка
            SELECT 
                f.id, f.name, f.owner_id, f.parent_id, f.path, f.level,
                f.size_bytes, f.files_count, f.created_at, f.updated_at
            FROM folders f
            WHERE f.id = $1 AND f.deleted_at IS NULL

            UNION ALL

            -- Рекурсивная часть
            SELECT 
                f.id, f.name, f.owner_id, f.parent_id, f.path, f.level,
                f.size_bytes, f.files_count, f.created_at, f.updated_at
            FROM folders f
            INNER JOIN folder_tree ft ON f.parent_id = ft.id
            WHERE f.deleted_at IS NULL
        )
        SELECT * FROM folder_tree
        ORDER BY path;
    `

	var folders []domain.Folder
	err = r.db.SelectContext(ctx, &folders, query, rootFolderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder structure: %w", err)
	}

	return folders, nil
}
