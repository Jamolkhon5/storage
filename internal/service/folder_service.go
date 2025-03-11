package service

import (
	"context"
	"fmt"
	"github.com/jmoiron/sqlx/types"
	"log"
	"strconv"
	"synxrondrive/internal/domain"
	"synxrondrive/internal/repository"
)

type FolderService struct {
	folderRepo        *repository.FolderRepository
	fileRepo          *repository.FileRepository
	permissionService *PermissionService
}

func NewFolderService(
	folderRepo *repository.FolderRepository,
	fileRepo *repository.FileRepository,
	permissionService *PermissionService,
) *FolderService {
	return &FolderService{
		folderRepo:        folderRepo,
		fileRepo:          fileRepo,
		permissionService: permissionService,
	}
}

// createRootFolder создает корневую папку для пользователя
func (s *FolderService) createRootFolder(ctx context.Context, name string, userID string) (*domain.Folder, error) {
	rootFolder := &domain.Folder{
		Name:     name,
		OwnerID:  userID,
		ParentID: nil,
		Path:     "/",
		Level:    0,
	}

	err := s.folderRepo.Create(ctx, rootFolder)
	if err != nil {
		return nil, fmt.Errorf("failed to create root folder: %w", err)
	}

	return rootFolder, nil
}

func (s *FolderService) CreateFolder(ctx context.Context, name string, parentID *int64, userID string) (*domain.Folder, error) {
	// Если это корневая папка
	if parentID == nil {
		return s.createRootFolder(ctx, name, userID)
	}

	// Проверяем права на создание в родительской папке
	hasPermission, err := s.permissionService.CheckSharedFolderPermission(
		ctx,
		userID,
		*parentID,
		OperationCreate,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to check permissions: %w", err)
	}

	// Получаем информацию о родительской папке
	folder, err := s.folderRepo.GetByID(ctx, *parentID)
	if err != nil {
		return nil, err
	}

	// Проверяем права доступа
	if !hasPermission && folder.OwnerID != userID {
		return nil, fmt.Errorf("access denied")
	}

	// ВАЖНОЕ ИЗМЕНЕНИЕ: Всегда устанавливаем владельцем родительской папки,
	// независимо от того, кто создает папку
	newFolder := &domain.Folder{
		Name:     name,
		OwnerID:  folder.OwnerID, // Владельцем всегда будет владелец родительской папки
		ParentID: parentID,
	}

	err = s.folderRepo.Create(ctx, newFolder)
	if err != nil {
		return nil, fmt.Errorf("failed to create folder: %w", err)
	}

	return newFolder, nil
}

func (s *FolderService) GetFolderContent(ctx context.Context, folderID int64, userID string) (*domain.FolderContent, error) {
	log.Printf("GetFolderContent called for folder: %d, user: %s", folderID, userID)

	if userID == "" {
		log.Printf("Error: userID is empty")
		return nil, fmt.Errorf("user ID is required")
	}

	content, err := s.folderRepo.GetContent(ctx, folderID, userID)
	if err != nil {
		log.Printf("Error getting folder content: %v", err)
		return nil, fmt.Errorf("failed to get folder content: %w", err)
	}

	log.Printf("Successfully got folder content. Files: %d, Subfolders: %d",
		len(content.Files), len(content.Folders))
	return content, nil
}

func (s *FolderService) DeleteFolder(ctx context.Context, folderID int64, userID string) error {
	folder, err := s.folderRepo.GetByID(ctx, folderID)
	if err != nil {
		return err
	}

	// Проверяем права на удаление
	if folder.OwnerID != userID {
		hasPermission, err := s.permissionService.CheckSharedFolderPermission(
			ctx,
			userID,
			folderID,
			OperationDelete, // Используем OperationType напрямую из того же пакета
		)
		if err != nil {
			return fmt.Errorf("failed to check permissions: %w", err)
		}
		if !hasPermission {
			return fmt.Errorf("access denied")
		}
	}

	return s.folderRepo.Delete(ctx, folderID)
}

func (s *FolderService) GetOrCreateRootFolder(ctx context.Context, userID string) (*domain.Folder, error) {
	// Пытаемся найти корневую папку пользователя
	folder, err := s.folderRepo.GetRootFolder(ctx, userID)
	if err == nil {
		return folder, nil
	}

	// Если корневой папки нет, создаем её
	rootFolder := &domain.Folder{
		Name:     "Root",
		OwnerID:  userID,
		ParentID: nil,
		Path:     "/",
		Level:    0,
		Metadata: types.JSONText(`{}`), // Добавляем пустой JSON для метаданных
	}

	err = s.folderRepo.Create(ctx, rootFolder)
	if err != nil {
		return nil, fmt.Errorf("failed to create root folder: %w", err)
	}

	return rootFolder, nil
}

func (s *FolderService) GetFolderStructure(ctx context.Context, userID string) ([]domain.Folder, error) {
	return s.folderRepo.GetUserFolders(ctx, userID)
}

func (s *FolderService) RenameFolder(ctx context.Context, folderID int64, newName string, userID string) error {
	// Получаем информацию о папке
	folder, err := s.folderRepo.GetByID(ctx, folderID)
	if err != nil {
		return fmt.Errorf("failed to get folder: %w", err)
	}

	// Проверяем права на редактирование
	if folder.OwnerID != userID {
		// Проверяем права доступа через shares
		hasPermission, err := s.permissionService.CheckPermission(
			ctx,
			userID,
			strconv.FormatInt(folderID, 10),
			domain.ResourceTypeFolder,
			OperationEdit,
		)
		if err != nil {
			return fmt.Errorf("failed to check permissions: %w", err)
		}
		if !hasPermission {
			// Дополнительно проверяем права через shared родительские папки
			hasSharedPermission, err := s.permissionService.CheckSharedFolderPermission(
				ctx,
				userID,
				folderID,
				OperationEdit,
			)
			if err != nil {
				return fmt.Errorf("failed to check shared permissions: %w", err)
			}
			if !hasSharedPermission {
				return fmt.Errorf("access denied")
			}
		}
	}

	// Проверяем, нет ли папки с таким именем на том же уровне
	exists, err := s.folderRepo.CheckFolderExists(ctx, folder.ParentID, newName, folderID)
	if err != nil {
		return fmt.Errorf("failed to check folder existence: %w", err)
	}
	if exists {
		return fmt.Errorf("folder with name %s already exists", newName)
	}

	// Обновляем имя папки
	if err := s.folderRepo.UpdateFolderName(ctx, folderID, newName); err != nil {
		return fmt.Errorf("failed to update folder name: %w", err)
	}

	return nil
}

// MoveFolder перемещает папку
func (s *FolderService) MoveFolder(ctx context.Context, folderID int64, newParentID int64, userID string) error {
	// Получаем информацию о перемещаемой папке
	folder, err := s.folderRepo.GetByID(ctx, folderID)
	if err != nil {
		return fmt.Errorf("failed to get folder: %w", err)
	}

	// Получаем информацию о целевой папке
	newParent, err := s.folderRepo.GetByID(ctx, newParentID)
	if err != nil {
		return fmt.Errorf("failed to get target folder: %w", err)
	}

	// Проверяем права на исходную папку
	if folder.OwnerID != userID {
		// Проверяем прямые права через shares
		hasDirectPermission, err := s.permissionService.CheckPermission(
			ctx,
			userID,
			strconv.FormatInt(folderID, 10),
			domain.ResourceTypeFolder,
			OperationEdit,
		)
		if err != nil {
			return fmt.Errorf("failed to check direct permissions: %w", err)
		}

		if !hasDirectPermission {
			// Проверяем права через shared папки
			hasSourcePermission, err := s.permissionService.CheckSharedFolderPermission(
				ctx,
				userID,
				folderID,
				OperationEdit,
			)
			if err != nil {
				return fmt.Errorf("failed to check source folder permissions: %w", err)
			}
			if !hasSourcePermission {
				return fmt.Errorf("access denied for source folder")
			}
		}
	}

	// Проверяем права на целевую папку
	if newParent.OwnerID != userID {
		hasTargetPermission, err := s.permissionService.CheckSharedFolderPermission(
			ctx,
			userID,
			newParentID,
			OperationEdit,
		)
		if err != nil {
			return fmt.Errorf("failed to check target folder permissions: %w", err)
		}
		if !hasTargetPermission {
			return fmt.Errorf("access denied for target folder")
		}
	}

	// Проверяем, не пытаемся ли переместить папку в саму себя или в свою подпапку
	if folderID == newParentID {
		return fmt.Errorf("cannot move folder into itself")
	}

	isInHierarchy, err := s.folderRepo.IsInHierarchy(ctx, newParentID, strconv.FormatInt(folderID, 10))
	if err != nil {
		return fmt.Errorf("failed to check hierarchy: %w", err)
	}
	if isInHierarchy {
		return fmt.Errorf("cannot move folder into its own subfolder")
	}

	// Проверяем, нет ли папки с таким именем в целевой папке
	exists, err := s.folderRepo.CheckFolderExistsInParent(ctx, newParentID, folder.Name)
	if err != nil {
		return fmt.Errorf("failed to check folder existence: %w", err)
	}
	if exists {
		return fmt.Errorf("folder with name %s already exists in target folder", folder.Name)
	}

	// Обновляем родительскую папку
	if err := s.folderRepo.UpdateFolderParent(ctx, folderID, newParentID); err != nil {
		return fmt.Errorf("failed to move folder: %w", err)
	}

	return nil
}
