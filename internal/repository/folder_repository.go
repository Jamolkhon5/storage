package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/jmoiron/sqlx"
	"log"
	"strconv"
	"strings"
	"synxrondrive/internal/auth"
	"synxrondrive/internal/domain"
	"time"
)

type FolderRepository struct {
	db *sqlx.DB
}

func NewFolderRepository(db *sqlx.DB) *FolderRepository {
	return &FolderRepository{db: db}
}

// Исправленная версия
func (r *FolderRepository) Create(ctx context.Context, folder *domain.Folder) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var path string
	var level int

	if folder.ParentID == nil {
		path = "/"
		level = 0
	} else {
		// Сначала получаем данные родительской папки
		err := tx.QueryRowContext(ctx,
			"SELECT path, level FROM folders WHERE id = $1",
			folder.ParentID,
		).Scan(&path, &level)
		if err != nil {
			return fmt.Errorf("failed to get parent folder: %w", err)
		}

		// Исправление: проверяем, является ли родительская папка корневой
		if path == "/" {
			// Для корневой папки просто добавляем имя без дополнительного слеша
			path = fmt.Sprintf("/%s", folder.Name)
		} else {
			// Для остальных папок добавляем слеш между путем и именем
			path = fmt.Sprintf("%s/%s", path, folder.Name)
		}
		level = level + 1
	}

	// Теперь выполняем вставку с уже подготовленными значениями
	query := `
        INSERT INTO folders (name, owner_id, parent_id, path, level)
        VALUES ($1, $2, $3, $4, $5)
        RETURNING id, created_at, updated_at`

	err = tx.QueryRowContext(
		ctx,
		query,
		folder.Name,
		folder.OwnerID,
		folder.ParentID,
		path,
		level,
	).Scan(&folder.ID, &folder.CreatedAt, &folder.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create folder: %w", err)
	}

	folder.Path = path
	folder.Level = level

	return tx.Commit()
}

func (r *FolderRepository) GetByID(ctx context.Context, id int64) (*domain.Folder, error) {
	query := `
        SELECT 
            id, name, owner_id, parent_id, path, level, 
            size_bytes, files_count, created_at, updated_at, 
            deleted_at, restore_path, restore_parent_id,
            COALESCE(metadata, '{}'::jsonb) as metadata
        FROM folders 
        WHERE id = $1 AND deleted_at IS NULL`

	var folder domain.Folder
	err := r.db.GetContext(ctx, &folder, query, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}

	return &folder, nil
}

func (r *FolderRepository) Update(ctx context.Context, folder *domain.Folder) error {
	query := `
        UPDATE folders 
        SET name = $1, updated_at = CURRENT_TIMESTAMP
        WHERE id = $2 AND owner_id = $3
        RETURNING updated_at`

	return r.db.QueryRowContext(
		ctx,
		query,
		folder.Name,
		folder.ID,
		folder.OwnerID,
	).Scan(&folder.UpdatedAt)
}

func (r *FolderRepository) Delete(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Сначала получаем информацию о папке и её подпапках
	var folders []struct {
		ID   int64  `db:"id"`
		Path string `db:"path"`
	}
	err = tx.SelectContext(ctx, &folders, `
        WITH RECURSIVE subfolder AS (
            SELECT id, path, parent_id
            FROM folders
            WHERE id = $1 AND deleted_at IS NULL
            
            UNION ALL
            
            SELECT f.id, f.path, f.parent_id
            FROM folders f
            INNER JOIN subfolder s ON f.parent_id = s.id
            WHERE f.deleted_at IS NULL
        )
        SELECT id, path FROM subfolder
    `, id)
	if err != nil {
		return fmt.Errorf("failed to get folders: %w", err)
	}

	now := time.Now()

	// Помечаем папки как удаленные и сохраняем их пути для восстановления
	for _, folder := range folders {
		_, err = tx.ExecContext(ctx, `
            UPDATE folders
            SET deleted_at = $1,
                restore_path = path,
                restore_parent_id = parent_id
            WHERE id = $2
        `, now, folder.ID)
		if err != nil {
			return fmt.Errorf("failed to mark folder as deleted: %w", err)
		}
	}

	// Помечаем файлы в этих папках как удаленные
	_, err = tx.ExecContext(ctx, `
        UPDATE files
        SET deleted_at = $1,
            restore_folder_id = folder_id,
            restore_path = (
                SELECT path 
                FROM folders 
                WHERE id = files.folder_id
            )
        WHERE folder_id IN (
            SELECT id 
            FROM folders 
            WHERE path LIKE (
                SELECT path || '%' 
                FROM folders 
                WHERE id = $2
            )
        ) AND deleted_at IS NULL
    `, now, id)
	if err != nil {
		return fmt.Errorf("failed to mark files as deleted: %w", err)
	}

	return tx.Commit()
}

func (r *FolderRepository) GetContent(ctx context.Context, folderID int64, userID string) (*domain.FolderContent, error) {
	log.Printf("[GetContent] Started. FolderID: %d, UserID: %s", folderID, userID)

	// Сначала получаем информацию о папке
	folder, err := r.GetByID(ctx, folderID)
	if err != nil {
		log.Printf("[GetContent] Error getting folder by ID %d: %v", folderID, err)
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}
	log.Printf("[GetContent] Successfully got folder. Name: %s, OwnerID: %s", folder.Name, folder.OwnerID)

	// Проверяем, является ли пользователь владельцем
	if folder.OwnerID == userID {
		log.Printf("[GetContent] User %s is the owner of folder %d, getting content directly", userID, folderID)
		return r.getContentInternal(ctx, folder)
	}

	log.Printf("[GetContent] User %s is not the owner of folder %d, checking shared access", userID, folderID)

	// Модифицированный запрос для проверки доступа
	query := `
        WITH RECURSIVE folder_hierarchy AS (
            -- Начальная папка и все её родители
            WITH RECURSIVE parent_folders AS (
                -- Начальная папка
                SELECT id, parent_id, path, owner_id
                FROM folders 
                WHERE id = $1
                UNION ALL
                -- Все родительские папки
                SELECT f.id, f.parent_id, f.path, f.owner_id
                FROM folders f
                INNER JOIN parent_folders pf ON f.id = pf.parent_id
            )
            SELECT id, parent_id, path, owner_id FROM parent_folders
        )
         SELECT s.access_type, s.resource_id, s.user_ids, fh.owner_id
        FROM folder_hierarchy fh
        JOIN shares s ON (
            fh.id::text = s.resource_id 
            AND s.resource_type = 'folder'
            AND (s.expires_at IS NULL OR s.expires_at > CURRENT_TIMESTAMP)
            AND (
                s.owner_id = fh.owner_id
                OR s.user_ids LIKE '%' || $2 || '%'
            )
        )
        LIMIT 1;`

	log.Printf("[GetContent] Executing modified share check query for folder %d", folderID)

	var (
		accessType string
		resourceID string
		userIDs    string
		ownerID    string
	)

	err = r.db.QueryRowContext(ctx, query, folderID, userID).Scan(&accessType, &resourceID, &userIDs, &ownerID)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("[GetContent] No share access found for user %s to folder %d", userID, folderID)
			return nil, fmt.Errorf("access denied")
		}
		log.Printf("[GetContent] Error checking share access: %v", err)
		return nil, fmt.Errorf("failed to check access: %w", err)
	}

	log.Printf("[GetContent] Found share access. Type: %s, ResourceID: %s, OwnerID: %s",
		accessType, resourceID, ownerID)

	// Добавляем информацию о шаринге к папке
	if userIDs != "" {
		userIDsList := strings.Split(userIDs, ",")
		users, err := auth.GetUsersByIds(ctx, userIDsList)
		if err != nil {
			log.Printf("[GetContent] Error getting users info: %v", err)
			return nil, fmt.Errorf("failed to get users info: %w", err)
		}

		sharedUsers := make([]domain.SharedUser, 0, len(users))
		for _, user := range users {
			sharedUsers = append(sharedUsers, domain.SharedUser{
				ID:       user.ID,
				Email:    user.Email,
				Name:     user.Name,
				Lastname: user.Lastname,
				Photo:    user.Photo,
			})
		}

		folder.ShareInfo = &domain.ShareInfo{
			IsShared:    true,
			SharedUsers: sharedUsers,
		}

		log.Printf("[GetContent] Added share info with %d users", len(sharedUsers))
	}

	// Получаем содержимое папки
	content, err := r.getContentInternal(ctx, folder)
	if err != nil {
		log.Printf("[GetContent] Error getting folder content: %v", err)
		return nil, fmt.Errorf("failed to get folder content: %w", err)
	}

	log.Printf("[GetContent] Successfully got folder content. Files: %d, Subfolders: %d",
		len(content.Files), len(content.Folders))

	return content, nil
}

// getContentInternal получает содержимое папки без проверки прав доступа
func (r *FolderRepository) getContentInternal(ctx context.Context, folder *domain.Folder) (*domain.FolderContent, error) {
	log.Printf("[getContentInternal] Getting content for folder %d", folder.ID)

	// 1. Получаем подпапки с информацией о шаринге
	var subfolders []domain.Folder
	subfoldersQuery := `
        WITH folder_shares AS (
            SELECT 
                s.resource_id,
                array_to_json(array_agg(json_build_object(
                    'access_type', s.access_type,
                    'user_ids', s.user_ids
                ))) as shares_info
            FROM shares s
            WHERE s.resource_type = 'folder'
            AND (s.expires_at IS NULL OR s.expires_at > CURRENT_TIMESTAMP)
            GROUP BY s.resource_id
        )
        SELECT 
            f.*,
            fs.shares_info
        FROM folders f
        LEFT JOIN folder_shares fs ON f.id::text = fs.resource_id
        WHERE f.parent_id = $1 
        AND f.deleted_at IS NULL 
        ORDER BY f.name
    `

	// Структура для сканирования результатов
	type shareInfoJSON struct {
		AccessType string `json:"access_type"`
		UserIds    string `json:"user_ids"`
	}

	type folderWithShares struct {
		domain.Folder
		SharesInfo *string `db:"shares_info"`
	}

	var foldersWithShares []folderWithShares
	err := r.db.SelectContext(ctx, &foldersWithShares, subfoldersQuery, folder.ID)
	if err != nil {
		log.Printf("[getContentInternal] Error getting subfolders: %v", err)
		return nil, fmt.Errorf("failed to get subfolders: %w", err)
	}

	// 2. Обрабатываем результаты и добавляем информацию о шаринге
	subfolders = make([]domain.Folder, len(foldersWithShares))
	for i, f := range foldersWithShares {
		subfolders[i] = f.Folder

		if f.SharesInfo != nil {
			var sharesInfo []shareInfoJSON
			if err := json.Unmarshal([]byte(*f.SharesInfo), &sharesInfo); err != nil {
				log.Printf("[getContentInternal] Error unmarshal shares info for folder %d: %v", f.ID, err)
				continue
			}

			// Собираем информацию о правах доступа
			userAccessMap := make(map[string]string)
			allUserIDs := make([]string, 0)

			for _, shareInfo := range sharesInfo {
				userIDs := strings.Split(shareInfo.UserIds, ",")
				for _, userID := range userIDs {
					userID = strings.TrimSpace(userID)
					if userID != "" {
						userAccessMap[userID] = shareInfo.AccessType
						allUserIDs = append(allUserIDs, userID)
					}
				}
			}

			if len(allUserIDs) > 0 {
				users, err := auth.GetUsersByIds(ctx, allUserIDs)
				if err != nil {
					log.Printf("[getContentInternal] Error getting users for folder %d: %v", f.ID, err)
					continue
				}

				sharedUsers := make([]domain.SharedUser, 0, len(users))
				for _, user := range users {
					if accessType, ok := userAccessMap[user.ID]; ok {
						sharedUsers = append(sharedUsers, domain.SharedUser{
							ID:         user.ID,
							Email:      user.Email,
							Name:       user.Name,
							Lastname:   user.Lastname,
							Photo:      user.Photo,
							AccessType: accessType,
						})
					}
				}

				subfolders[i].ShareInfo = &domain.ShareInfo{
					IsShared:    true,
					SharedUsers: sharedUsers,
				}
			}
		}
	}

	log.Printf("[getContentInternal] Found %d subfolders", len(subfolders))

	// 3. Получаем файлы
	var files []domain.File
	filesQuery := `
        SELECT * FROM files 
        WHERE folder_id = $1 
        AND deleted_at IS NULL 
        ORDER BY name
    `

	err = r.db.SelectContext(ctx, &files, filesQuery, folder.ID)
	if err != nil {
		log.Printf("[getContentInternal] Error getting files: %v", err)
		return nil, fmt.Errorf("failed to get files: %w", err)
	}
	log.Printf("[getContentInternal] Found %d files", len(files))

	// 4. Получаем информацию о шаринге для текущей папки
	sharesQuery := `
        SELECT 
            array_to_json(array_agg(json_build_object(
                'access_type', s.access_type,
                'user_ids', s.user_ids
            ))) as shares_info
        FROM shares s
        WHERE s.resource_id = $1::text 
        AND s.resource_type = 'folder'
        AND (s.expires_at IS NULL OR s.expires_at > CURRENT_TIMESTAMP)
    `

	var sharesInfoJSON *string
	err = r.db.QueryRowContext(ctx, sharesQuery, folder.ID).Scan(&sharesInfoJSON)
	if err != nil && err != sql.ErrNoRows {
		log.Printf("[getContentInternal] Error getting shares for current folder: %v", err)
		return nil, fmt.Errorf("failed to get shares: %w", err)
	}

	if sharesInfoJSON != nil {
		var sharesInfo []shareInfoJSON
		if err := json.Unmarshal([]byte(*sharesInfoJSON), &sharesInfo); err != nil {
			log.Printf("[getContentInternal] Error unmarshal shares info for current folder: %v", err)
		} else {
			userAccessMap := make(map[string]string)
			allUserIDs := make([]string, 0)

			for _, shareInfo := range sharesInfo {
				userIDs := strings.Split(shareInfo.UserIds, ",")
				for _, userID := range userIDs {
					userID = strings.TrimSpace(userID)
					if userID != "" {
						userAccessMap[userID] = shareInfo.AccessType
						allUserIDs = append(allUserIDs, userID)
					}
				}
			}

			if len(allUserIDs) > 0 {
				users, err := auth.GetUsersByIds(ctx, allUserIDs)
				if err == nil {
					sharedUsers := make([]domain.SharedUser, 0, len(users))
					for _, user := range users {
						if accessType, ok := userAccessMap[user.ID]; ok {
							sharedUsers = append(sharedUsers, domain.SharedUser{
								ID:         user.ID,
								Email:      user.Email,
								Name:       user.Name,
								Lastname:   user.Lastname,
								Photo:      user.Photo,
								AccessType: accessType,
							})
						}
					}

					folder.ShareInfo = &domain.ShareInfo{
						IsShared:    true,
						SharedUsers: sharedUsers,
					}
				}
			}
		}
	}

	// 5. Возвращаем результат
	return &domain.FolderContent{
		Folder:  *folder,
		Files:   files,
		Folders: subfolders,
	}, nil
}

func (r *FolderRepository) GetRootFolder(ctx context.Context, ownerID string) (*domain.Folder, error) {
	var folder domain.Folder
	query := `
        SELECT * FROM folders 
        WHERE owner_id = $1 AND parent_id IS NULL 
        LIMIT 1`

	err := r.db.GetContext(ctx, &folder, query, ownerID)
	if err != nil {
		return nil, err
	}

	return &folder, nil
}

func (r *FolderRepository) GetUserFolders(ctx context.Context, userID string) ([]domain.Folder, error) {
	log.Printf("[GetUserFolders] Получение структуры папок для пользователя %s", userID)

	var folders []domain.Folder
	query := `
        SELECT 
            id, name, owner_id, parent_id, path, level, 
            size_bytes, files_count, created_at, updated_at, 
            deleted_at, restore_path, restore_parent_id, metadata
        FROM folders 
        WHERE owner_id = $1 
        AND deleted_at IS NULL 
        ORDER BY path
    `

	err := r.db.SelectContext(ctx, &folders, query, userID)
	if err != nil {
		log.Printf("[GetUserFolders] Ошибка получения папок: %v", err)
		return nil, fmt.Errorf("failed to get user folders: %w", err)
	}

	log.Printf("[GetUserFolders] Получено %d папок для пользователя %s", len(folders), userID)
	return folders, nil
}

// Добавляем в FolderRepository новый метод для проверки иерархии
func (r *FolderRepository) IsInHierarchy(ctx context.Context, folderID int64, rootFolderID string) (bool, error) {
	rootID, err := strconv.ParseInt(rootFolderID, 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid root folder ID: %w", err)
	}

	log.Printf("[IsInHierarchy] Checking if folder %d is in hierarchy of %d", folderID, rootID)

	// Сначала получаем пути обеих папок
	var rootPath, folderPath string
	err = r.db.QueryRowContext(ctx,
		"SELECT path FROM folders WHERE id = $1",
		rootID,
	).Scan(&rootPath)
	if err != nil {
		log.Printf("[IsInHierarchy] Error getting root folder path: %v", err)
		return false, fmt.Errorf("failed to get root folder path: %w", err)
	}

	err = r.db.QueryRowContext(ctx,
		"SELECT path FROM folders WHERE id = $1",
		folderID,
	).Scan(&folderPath)
	if err != nil {
		log.Printf("[IsInHierarchy] Error getting folder path: %v", err)
		return false, fmt.Errorf("failed to get folder path: %w", err)
	}

	log.Printf("[IsInHierarchy] Root path: %s, Folder path: %s", rootPath, folderPath)

	// Проверяем, является ли путь подпапки подстрокой пути корневой папки
	isInHierarchy := strings.HasPrefix(folderPath, rootPath)
	log.Printf("[IsInHierarchy] Is in hierarchy: %v", isInHierarchy)

	return isInHierarchy, nil
}

func (r *FolderRepository) addShareInfo(ctx context.Context, folder *domain.Folder) error {
	query := `
        SELECT s.access_type, s.user_ids
        FROM shares s
        WHERE s.resource_id = $1::text 
        AND s.resource_type = 'folder'
        AND (s.expires_at IS NULL OR s.expires_at > CURRENT_TIMESTAMP)
    `

	var accessType string
	var userIDs string
	err := r.db.QueryRowContext(ctx, query, folder.ID).Scan(&accessType, &userIDs)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	if userIDs != "" {
		userIDsList := strings.Split(userIDs, ",")
		users, err := auth.GetUsersByIds(ctx, userIDsList)
		if err != nil {
			return err
		}

		sharedUsers := make([]domain.SharedUser, 0, len(users))
		for _, user := range users {
			sharedUsers = append(sharedUsers, domain.SharedUser{
				ID:       user.ID,
				Email:    user.Email,
				Name:     user.Name,
				Lastname: user.Lastname,
				Photo:    user.Photo,
			})
		}

		folder.ShareInfo = &domain.ShareInfo{
			IsShared:    true,
			SharedUsers: sharedUsers,
		}
	}

	return nil
}

// UpdateFolderName обновляет имя папки
func (r *FolderRepository) UpdateFolderName(ctx context.Context, folderID int64, newName string) error {
	// Получаем текущий путь папки
	var currentPath string
	var parentID *int64
	err := r.db.QueryRowContext(ctx,
		"SELECT path, parent_id FROM folders WHERE id = $1",
		folderID,
	).Scan(&currentPath, &parentID)
	if err != nil {
		return fmt.Errorf("failed to get current path: %w", err)
	}

	// Исправление: формируем новый путь с учетом расположения папки
	var newPath string
	if parentID == nil || (currentPath == "/"+currentPath[1:]) {
		// Это корневая папка или папка в корне
		newPath = fmt.Sprintf("/%s", newName)
	} else {
		// Это папка в подпапке
		lastSlashIndex := strings.LastIndex(currentPath, "/")
		if lastSlashIndex >= 0 {
			newPath = currentPath[:lastSlashIndex+1] + newName
		} else {
			// Защита от непредвиденных случаев
			newPath = fmt.Sprintf("/%s", newName)
		}
	}

	// Обновляем имя и путь папки
	query := `
        UPDATE folders 
        SET name = $1,
            path = $2,
            updated_at = CURRENT_TIMESTAMP
        WHERE id = $3
    `
	result, err := r.db.ExecContext(ctx, query, newName, newPath, folderID)
	if err != nil {
		return fmt.Errorf("failed to update folder name: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("folder not found")
	}

	// Обновляем пути всех подпапок
	updateSubfoldersQuery := `
        WITH RECURSIVE subfolder AS (
            SELECT id, path FROM folders WHERE parent_id = $1
            UNION ALL
            SELECT f.id, f.path
            FROM folders f
            INNER JOIN subfolder s ON f.parent_id = s.id
        )
        UPDATE folders f
        SET path = replace(f.path, $2, $3)
        WHERE f.id IN (SELECT id FROM subfolder)
    `
	_, err = r.db.ExecContext(ctx, updateSubfoldersQuery, folderID, currentPath, newPath)
	if err != nil {
		return fmt.Errorf("failed to update subfolders paths: %w", err)
	}

	return nil
}

// UpdateFolderParent обновляет родительскую папку
func (r *FolderRepository) UpdateFolderParent(ctx context.Context, folderID int64, newParentID int64) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Получаем информацию о перемещаемой папке
	var folder domain.Folder
	err = tx.QueryRowContext(ctx,
		"SELECT * FROM folders WHERE id = $1",
		folderID,
	).Scan(&folder.ID, &folder.Name, &folder.OwnerID, &folder.ParentID, &folder.Path,
		&folder.Level, &folder.SizeBytes, &folder.FilesCount, &folder.CreatedAt,
		&folder.UpdatedAt, &folder.DeletedAt, &folder.RestorePath, &folder.RestoreParentID, &folder.Metadata)
	if err != nil {
		return fmt.Errorf("failed to get folder info: %w", err)
	}

	// Получаем путь новой родительской папки
	var newParentPath string
	err = tx.QueryRowContext(ctx,
		"SELECT path FROM folders WHERE id = $1",
		newParentID,
	).Scan(&newParentPath)
	if err != nil {
		return fmt.Errorf("failed to get new parent path: %w", err)
	}

	// Исправление: формируем новый путь с учетом корневой папки
	var newPath string
	if newParentPath == "/" {
		// Если новый родитель - корневая папка
		newPath = fmt.Sprintf("/%s", folder.Name)
	} else {
		// Для остальных случаев
		newPath = fmt.Sprintf("%s/%s", newParentPath, folder.Name)
	}

	// Обновляем родителя и путь папки
	updateQuery := `
        UPDATE folders 
        SET parent_id = $1,
            path = $2,
            updated_at = CURRENT_TIMESTAMP
        WHERE id = $3
    `
	_, err = tx.ExecContext(ctx, updateQuery, newParentID, newPath, folderID)
	if err != nil {
		return fmt.Errorf("failed to update folder parent: %w", err)
	}

	// Обновляем пути всех подпапок
	updateSubfoldersQuery := `
        WITH RECURSIVE subfolder AS (
            SELECT id, path FROM folders WHERE parent_id = $1
            UNION ALL
            SELECT f.id, f.path
            FROM folders f
            INNER JOIN subfolder s ON f.parent_id = s.id
        )
        UPDATE folders f
        SET path = replace(f.path, $2, $3)
        WHERE f.id IN (SELECT id FROM subfolder)
    `
	_, err = tx.ExecContext(ctx, updateSubfoldersQuery, folderID, folder.Path, newPath)
	if err != nil {
		return fmt.Errorf("failed to update subfolders paths: %w", err)
	}

	return tx.Commit()
}

// CheckFolderExists проверяет существование папки с таким именем на том же уровне
func (r *FolderRepository) CheckFolderExists(ctx context.Context, parentID *int64, name string, excludeID int64) (bool, error) {
	var exists bool
	query := `
        SELECT EXISTS(
            SELECT 1 FROM folders 
            WHERE parent_id = $1 AND name = $2 AND id != $3 AND deleted_at IS NULL
        )`

	err := r.db.GetContext(ctx, &exists, query, parentID, name, excludeID)
	if err != nil {
		return false, fmt.Errorf("failed to check folder existence: %w", err)
	}

	return exists, nil
}

// CheckFolderExistsInParent проверяет существование папки с таким именем в родительской папке
func (r *FolderRepository) CheckFolderExistsInParent(ctx context.Context, parentID int64, name string) (bool, error) {
	var exists bool
	query := `
        SELECT EXISTS(
            SELECT 1 FROM folders 
            WHERE parent_id = $1 AND name = $2 AND deleted_at IS NULL
        )`

	err := r.db.GetContext(ctx, &exists, query, parentID, name)
	if err != nil {
		return false, fmt.Errorf("failed to check folder existence: %w", err)
	}

	return exists, nil
}
