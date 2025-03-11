package service

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"strconv"
	"strings"
	"synxrondrive/internal/domain"
	"synxrondrive/internal/repository"
	"time"
)

// PermissionService представляет сервис для проверки прав доступа
type PermissionService struct {
	shareRepo  *repository.ShareRepository
	fileRepo   *repository.FileRepository
	folderRepo *repository.FolderRepository
}

// NewPermissionService создает новый экземпляр PermissionService
func NewPermissionService(
	shareRepo *repository.ShareRepository,
	fileRepo *repository.FileRepository,
	folderRepo *repository.FolderRepository,
) *PermissionService {
	return &PermissionService{
		shareRepo:  shareRepo,
		fileRepo:   fileRepo,
		folderRepo: folderRepo,
	}
}

// OperationType определяет тип операции
type OperationType string

const (
	OperationView     OperationType = "view"
	OperationEdit     OperationType = "edit"
	OperationDelete   OperationType = "delete"
	OperationCreate   OperationType = "create"
	OperationShare    OperationType = "share"
	OperationDownload OperationType = "download"
	OperationUpload   OperationType = "upload"
	OperationRename   OperationType = "rename"
)

// checkAccessLevel проверяет, достаточен ли уровень доступа для операции
func (s *PermissionService) checkAccessLevel(accessType domain.AccessType, operation OperationType) bool {
	switch accessType {
	case "view":
		// Только просмотр и скачивание
		return operation == OperationView || operation == OperationDownload

	case "edit":
		// Редактирование позволяет создавать, загружать, удалять и переименовывать
		switch operation {
		case OperationView, OperationDownload, OperationUpload,
			OperationCreate, OperationEdit, OperationRename:
			return true
		default:
			return false
		}

	case "full":
		// Полный доступ разрешает все операции
		return true

	default:
		return false
	}
}

// GetResourceOwner получает ID владельца ресурса
func (s *PermissionService) GetResourceOwner(
	ctx context.Context,
	resourceID string,
	resourceType domain.ResourceType,
) (string, error) {
	switch resourceType {
	case domain.ResourceTypeFile:
		fileUUID, err := uuid.Parse(resourceID)
		if err != nil {
			return "", fmt.Errorf("invalid file UUID: %w", err)
		}
		file, err := s.fileRepo.GetByUUID(ctx, fileUUID)
		if err != nil {
			return "", err
		}
		return file.OwnerID, nil

	case domain.ResourceTypeFolder:
		folderID, err := strconv.ParseInt(resourceID, 10, 64)
		if err != nil {
			return "", fmt.Errorf("invalid folder ID: %w", err)
		}
		folder, err := s.folderRepo.GetByID(ctx, folderID)
		if err != nil {
			return "", err
		}
		return folder.OwnerID, nil

	default:
		return "", fmt.Errorf("unsupported resource type: %s", resourceType)
	}
}

// CheckPermission проверяет права доступа для конкретной операции
func (s *PermissionService) CheckPermission(
	ctx context.Context,
	userID string,
	resourceID string,
	resourceType domain.ResourceType,
	operation OperationType,
) (bool, error) {
	// Проверяем является ли пользователь владельцем
	ownerID, err := s.GetResourceOwner(ctx, resourceID, resourceType)
	if err != nil {
		return false, fmt.Errorf("failed to get resource owner: %w", err)
	}

	// Владелец имеет полные права
	if ownerID == userID {
		return true, nil
	}

	// Получаем все активные shares для ресурса
	shares, err := s.shareRepo.GetSharesByResource(ctx, resourceID, resourceType)
	if err != nil {
		return false, fmt.Errorf("failed to get shares: %w", err)
	}

	// Проверяем каждый share
	for _, share := range shares {
		// Проверяем срок действия
		if share.ExpiresAt != nil && share.ExpiresAt.Before(time.Now()) {
			continue
		}

		// Проверяем доступ пользователя к share
		if share.Token != "" || strings.Contains(share.UserIDs, userID) {
			// Проверяем уровень доступа для операции
			if s.checkAccessLevel(share.AccessType, operation) {
				return true, nil
			}
		}
	}

	return false, nil
}

// CheckFolderTreePermission проверяет права доступа ко всей иерархии папок
func (s *PermissionService) CheckFolderTreePermission(
	ctx context.Context,
	userID string,
	folderID string,
	operation OperationType,
) (bool, error) {
	// Получаем папку
	id, err := strconv.ParseInt(folderID, 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid folder ID: %w", err)
	}

	folder, err := s.folderRepo.GetByID(ctx, id)
	if err != nil {
		return false, fmt.Errorf("failed to get folder: %w", err)
	}

	currentFolderID := folder.ID
	for currentFolderID != 0 {
		// Проверяем права на текущую папку
		hasPermission, err := s.CheckPermission(
			ctx,
			userID,
			strconv.FormatInt(currentFolderID, 10),
			domain.ResourceTypeFolder,
			operation,
		)
		if err != nil {
			return false, fmt.Errorf("failed to check permissions: %w", err)
		}
		if !hasPermission {
			return false, nil
		}

		// Переходим к родительской папке
		if folder.ParentID == nil {
			break
		}
		currentFolderID = *folder.ParentID

		// Получаем родительскую папку
		folder, err = s.folderRepo.GetByID(ctx, currentFolderID)
		if err != nil {
			return false, fmt.Errorf("failed to get parent folder: %w", err)
		}
	}

	return true, nil
}

func (s *PermissionService) CheckSharedFolderPermission(
	ctx context.Context,
	userID string,
	folderID int64,
	operation OperationType,
) (bool, error) {
	// Получаем иерархию папок до корневой shared папки
	folder, err := s.folderRepo.GetByID(ctx, folderID)
	if err != nil {
		return false, fmt.Errorf("failed to get folder: %w", err)
	}

	currentFolderID := folder.ID
	for currentFolderID != 0 {
		// Получаем все shares для текущей папки
		shares, err := s.shareRepo.GetSharesByResource(
			ctx,
			strconv.FormatInt(currentFolderID, 10),
			domain.ResourceTypeFolder,
		)
		if err != nil {
			return false, fmt.Errorf("failed to get shares: %w", err)
		}

		// Проверяем права доступа для каждого share
		for _, share := range shares {
			// Пропускаем просроченные shares
			if share.ExpiresAt != nil && share.ExpiresAt.Before(time.Now()) {
				continue
			}

			// Проверяем доступ пользователя к share
			if share.Token != "" || strings.Contains(share.UserIDs, userID) {
				// Проверяем уровень доступа для операции
				if s.checkAccessLevel(share.AccessType, operation) {
					return true, nil
				}
			}
		}

		// Переходим к родительской папке
		if folder.ParentID == nil {
			break
		}
		currentFolderID = *folder.ParentID
		folder, err = s.folderRepo.GetByID(ctx, currentFolderID)
		if err != nil {
			return false, fmt.Errorf("failed to get parent folder: %w", err)
		}
	}

	return false, nil
}
