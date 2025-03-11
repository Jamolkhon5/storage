package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"github.com/google/uuid"
	"log"
	"strconv"
	"strings"
	"synxrondrive/internal/domain"
	"synxrondrive/internal/repository"
	"time"
)

type ShareService struct {
	shareRepo  *repository.ShareRepository
	fileRepo   *repository.FileRepository
	folderRepo *repository.FolderRepository
}

type SharedResource struct {
	ResourceType domain.ResourceType `json:"resource_type"`
	AccessType   domain.AccessType   `json:"access_type"`
	Data         interface{}         `json:"data"`
}

type SharedWithMeResource struct {
	Share    *domain.Share `json:"share"`
	Resource interface{}   `json:"resource"`
}

func NewShareService(
	shareRepo *repository.ShareRepository,
	fileRepo *repository.FileRepository,
	folderRepo *repository.FolderRepository,
) *ShareService {
	return &ShareService{
		shareRepo:  shareRepo,
		fileRepo:   fileRepo,
		folderRepo: folderRepo,
	}
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func (s *ShareService) CreateShare(
	ctx context.Context,
	resourceID string,
	resourceType domain.ResourceType,
	ownerID string,
	accessType domain.AccessType,
	expiresIn *time.Duration,
	userID string,
) (*domain.Share, error) {
	// Проверяем владельца ресурса
	switch resourceType {
	case domain.ResourceTypeFile:
		fileUUID, err := uuid.Parse(resourceID)
		if err != nil {
			return nil, fmt.Errorf("invalid file UUID: %w", err)
		}
		file, err := s.fileRepo.GetByUUID(ctx, fileUUID)
		if err != nil {
			return nil, err
		}
		if file.OwnerID != ownerID {
			return nil, fmt.Errorf("access denied")
		}
	case domain.ResourceTypeFolder:
		folderID, err := strconv.ParseInt(resourceID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid folder ID: %w", err)
		}
		folder, err := s.folderRepo.GetByID(ctx, folderID)
		if err != nil {
			return nil, err
		}
		if folder.OwnerID != ownerID {
			return nil, fmt.Errorf("access denied")
		}
	}

	// Проверяем существующие share с такими же параметрами
	var expiresAt *time.Time
	if expiresIn != nil {
		t := time.Now().Add(*expiresIn)
		expiresAt = &t
	}

	existingShare, err := s.shareRepo.GetExistingShare(ctx, resourceID, resourceType, ownerID, accessType, expiresAt)
	if err == nil && existingShare != nil {
		return existingShare, nil
	}

	// Если не нашли существующий share, создаем новый
	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	share := &domain.Share{
		ID:           uuid.New(),
		ResourceID:   resourceID,
		ResourceType: resourceType,
		OwnerID:      ownerID,
		AccessType:   accessType,
		Token:        token,
		ExpiresAt:    expiresAt,
	}

	err = s.shareRepo.Create(ctx, share)
	if err != nil {
		return nil, fmt.Errorf("failed to create share: %w", err)
	}

	return share, nil
}

func (s *ShareService) GetSharedResource(ctx context.Context, token string) (*SharedResource, error) {
	// Получаем информацию о шаре по токену
	share, err := s.shareRepo.GetByToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("share not found or expired: %w", err)
	}

	var resource SharedResource
	resource.ResourceType = share.ResourceType
	resource.AccessType = share.AccessType

	// В зависимости от типа ресурса получаем данные
	switch share.ResourceType {
	case domain.ResourceTypeFile:
		fileUUID, err := uuid.Parse(share.ResourceID)
		if err != nil {
			return nil, fmt.Errorf("invalid file UUID: %w", err)
		}
		file, err := s.fileRepo.GetByUUID(ctx, fileUUID)
		if err != nil {
			return nil, fmt.Errorf("file not found: %w", err)
		}
		resource.Data = file

	case domain.ResourceTypeFolder:
		folderID, err := strconv.ParseInt(share.ResourceID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid folder ID: %w", err)
		}

		// Получаем папку для определения владельца
		folder, err := s.folderRepo.GetByID(ctx, folderID)
		if err != nil {
			return nil, fmt.Errorf("folder not found: %w", err)
		}

		// Получаем содержимое папки, используя ID владельца
		content, err := s.folderRepo.GetContent(ctx, folderID, folder.OwnerID)
		if err != nil {
			return nil, fmt.Errorf("failed to get folder content: %w", err)
		}
		resource.Data = content
	}

	return &resource, nil
}

// Добавляем новый метод в ShareService
func (s *ShareService) GetSharedWithUser(ctx context.Context, userID string) ([]SharedWithMeResource, error) {
	// Получаем все активные шары для пользователя
	shares, err := s.shareRepo.GetUserShares(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user shares: %w", err)
	}

	var resources []SharedWithMeResource

	// Для каждого шара получаем информацию о ресурсе
	for _, share := range shares {
		var resource interface{}

		switch share.ResourceType {
		case domain.ResourceTypeFile:
			fileUUID, err := uuid.Parse(share.ResourceID)
			if err != nil {
				continue
			}
			file, err := s.fileRepo.GetByUUID(ctx, fileUUID)
			if err != nil {
				continue
			}
			resource = file

		case domain.ResourceTypeFolder:
			folderID, err := strconv.ParseInt(share.ResourceID, 10, 64)
			if err != nil {
				continue
			}
			folder, err := s.folderRepo.GetByID(ctx, folderID)
			if err != nil {
				continue
			}

			// Получаем содержимое папки
			content, err := s.folderRepo.GetContent(ctx, folderID, folder.OwnerID)
			if err != nil {
				continue
			}
			resource = content
		}

		// Добавляем ресурс в список только если он успешно получен
		if resource != nil {
			resources = append(resources, SharedWithMeResource{
				Share:    &share,
				Resource: resource,
			})
		}
	}

	return resources, nil
}

// GetSharedContent получает содержимое общего ресурса с учетом путей
func (s *ShareService) GetSharedContent(ctx context.Context, shareID string, path string, userID string) (*domain.SharedContent, error) {
	log.Printf("[GetSharedContent] Starting with path: %s", path)

	// Если путь указывает на папку, проверяем доступ к ней
	if strings.HasPrefix(path, "/folders/") {
		folderIDStr := strings.TrimPrefix(path, "/folders/")
		folderID, err := strconv.ParseInt(folderIDStr, 10, 64)
		if err != nil {
			log.Printf("[GetSharedContent] Error parsing folder ID: %v", err)
			return nil, fmt.Errorf("invalid folder ID in path: %w", err)
		}

		log.Printf("[GetSharedContent] Validating access to folder: %d", folderID)
		if err := s.validateFolderAccess(ctx, shareID, folderID, userID); err != nil {
			log.Printf("[GetSharedContent] Validation failed: %v", err)
			return nil, fmt.Errorf("access validation failed: %w", err)
		}
	}

	// Получаем содержимое
	content, err := s.shareRepo.GetShareContent(ctx, shareID, path)
	if err != nil {
		log.Printf("[GetSharedContent] Error getting content: %v", err)
		return nil, fmt.Errorf("failed to get content: %w", err)
	}

	log.Printf("[GetSharedContent] Successfully retrieved content")
	return content, nil
}

// AddUserToShare добавляет пользователя к общему ресурсу
func (s *ShareService) AddUserToShare(ctx context.Context, shareID string, userID string) error {
	share, err := s.shareRepo.GetByID(ctx, shareID)
	if err != nil {
		return fmt.Errorf("share not found: %w", err)
	}

	// Проверяем срок действия
	if share.ExpiresAt != nil && share.ExpiresAt.Before(time.Now()) {
		return fmt.Errorf("share has expired")
	}

	return s.shareRepo.AddUserToShare(ctx, shareID, userID)
}

// GetUserSharedContent получает все доступные пользователю ресурсы
func (s *ShareService) GetUserSharedContent(ctx context.Context, userID string) ([]domain.SharedContent, error) {
	// Получаем только те шары, где пользователь НЕ является владельцем
	shares, err := s.shareRepo.GetUserShares(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user shares: %w", err)
	}

	var contents []domain.SharedContent
	for _, share := range shares {
		// Дополнительная проверка, что это не собственный ресурс
		if share.OwnerID != userID {
			content, err := s.shareRepo.GetShareContent(ctx, share.ID.String(), "/")
			if err != nil {
				continue // Пропускаем недоступные ресурсы
			}
			contents = append(contents, *content)
		}
	}

	return contents, nil
}

// hasAccess проверяет, есть ли у пользователя доступ к ресурсу
func (s *ShareService) hasAccess(share *domain.Share, userID string) bool {
	// 1. Владелец всегда имеет доступ
	if share.OwnerID == userID {
		return true
	}

	// 2. Проверяем наличие token - если есть token, то это публичная ссылка
	if share.Token != "" {
		return true // Для публичных ссылок разрешаем доступ всем
	}

	// 3. Для непубличных ресурсов проверяем список пользователей
	if share.UserIDs != "" {
		userIDs := strings.Split(share.UserIDs, ",")
		for _, id := range userIDs {
			if id == userID {
				return true
			}
		}
	}

	return false
}

// generateSharedBreadcrumbs генерирует хлебные крошки для shared-ресурса
func (s *ShareService) generateSharedBreadcrumbs(content *domain.SharedContent) []domain.SharedBreadcrumb {
	breadcrumbs := []domain.SharedBreadcrumb{
		{
			ID:      "shared",
			Name:    "Доступные мне",
			IsRoot:  true,
			ShareID: content.Share.ID.String(),
		},
	}

	if content.ParentFolders != nil {
		for _, folder := range content.ParentFolders {
			breadcrumbs = append(breadcrumbs, domain.SharedBreadcrumb{
				ID:      strconv.FormatInt(folder.ID, 10),
				Name:    folder.Name,
				ShareID: content.Share.ID.String(),
			})
		}
	}

	return breadcrumbs
}

// GrantAccess предоставляет доступ пользователю к ресурсу
func (s *ShareService) GrantAccess(ctx context.Context, token string, userID string) (*domain.Share, interface{}, error) {
	// Получаем share по токену
	share, err := s.shareRepo.GetByToken(ctx, token)
	if err != nil {
		return nil, nil, fmt.Errorf("share not found or expired: %w", err)
	}

	// Проверяем срок действия
	if share.ExpiresAt != nil && share.ExpiresAt.Before(time.Now()) {
		return nil, nil, fmt.Errorf("share has expired")
	}

	// Для публичных ссылок НЕ добавляем пользователя в UserIDs
	// Добавляем пользователя только если это не публичная ссылка
	if share.Token == "" {
		if err = s.shareRepo.AddUserToShare(ctx, share.ID.String(), userID); err != nil {
			return nil, nil, fmt.Errorf("failed to add user to share: %w", err)
		}
	}

	// Получаем ресурс в зависимости от типа
	var resource interface{}
	switch share.ResourceType {
	case domain.ResourceTypeFile:
		fileUUID, err := uuid.Parse(share.ResourceID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid file UUID: %w", err)
		}
		resource, err = s.fileRepo.GetByUUID(ctx, fileUUID)
		if err != nil {
			return nil, nil, fmt.Errorf("file not found: %w", err)
		}
	case domain.ResourceTypeFolder:
		folderID, err := strconv.ParseInt(share.ResourceID, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid folder ID: %w", err)
		}
		resource, err = s.folderRepo.GetByID(ctx, folderID)
		if err != nil {
			return nil, nil, fmt.Errorf("folder not found: %w", err)
		}
	}

	return share, resource, nil
}

func (s *ShareService) GetSharedFolderContent(ctx context.Context, token string, folderID string, userID string) (*domain.SharedContent, error) {
	// Получаем share по токену
	share, err := s.shareRepo.GetByToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("share not found or expired: %w", err)
	}

	// Проверяем доступ пользователя
	if !s.hasAccess(share, userID) {
		return nil, fmt.Errorf("access denied")
	}

	// Конвертируем folderID в int64
	folderIDInt, err := strconv.ParseInt(folderID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid folder ID: %w", err)
	}

	// Если это корневая папка share
	rootFolderID := share.ResourceID
	if rootFolderID == folderID {
		return s.shareRepo.GetShareContent(ctx, share.ID.String(), "/")
	}

	// Проверяем иерархию папок
	inHierarchy, err := s.folderRepo.IsInHierarchy(ctx, folderIDInt, rootFolderID)
	if err != nil {
		return nil, fmt.Errorf("failed to check hierarchy: %w", err)
	}

	if !inHierarchy {
		return nil, fmt.Errorf("folder is not in shared resource hierarchy")
	}

	// Получаем содержимое папки
	return s.shareRepo.GetShareContent(ctx, share.ID.String(), fmt.Sprintf("/folders/%d", folderIDInt))
}

func (s *ShareService) validateFolderAccess(ctx context.Context, shareID string, folderID int64, userID string) error {
	log.Printf("[validateFolderAccess] Starting validation for shareID: %s, folderID: %d", shareID, folderID)

	// Получаем информацию о share
	share, err := s.shareRepo.GetByID(ctx, shareID)
	if err != nil {
		log.Printf("[validateFolderAccess] Error getting share: %v", err)
		return fmt.Errorf("failed to get share: %w", err)
	}

	// Проверяем доступ пользователя к share
	if !s.hasAccess(share, userID) {
		log.Printf("[validateFolderAccess] Access denied for user: %s", userID)
		return fmt.Errorf("access denied to share")
	}

	// Получаем root folder ID
	rootFolderID, err := strconv.ParseInt(share.ResourceID, 10, 64)
	if err != nil {
		log.Printf("[validateFolderAccess] Error parsing root folder ID: %v", err)
		return fmt.Errorf("invalid root folder ID: %w", err)
	}

	log.Printf("[validateFolderAccess] Root folder ID: %d, Requested folder ID: %d", rootFolderID, folderID)

	// Получаем запрашиваемую папку
	requestedFolder, err := s.folderRepo.GetByID(ctx, folderID)
	if err != nil {
		log.Printf("[validateFolderAccess] Error getting requested folder: %v", err)
		return fmt.Errorf("failed to get requested folder: %w", err)
	}

	// Получаем корневую shared папку
	rootFolder, err := s.folderRepo.GetByID(ctx, rootFolderID)
	if err != nil {
		log.Printf("[validateFolderAccess] Error getting root folder: %v", err)
		return fmt.Errorf("failed to get root folder: %w", err)
	}

	log.Printf("[validateFolderAccess] Checking if path %s contains %s", requestedFolder.Path, rootFolder.Path)

	// Проверяем, находится ли запрашиваемая папка в иерархии shared папки
	// путем проверки, начинается ли путь запрашиваемой папки с пути корневой shared папки
	if !strings.HasPrefix(requestedFolder.Path, rootFolder.Path) {
		log.Printf("[validateFolderAccess] Folder %d is not in hierarchy of folder %d", folderID, rootFolderID)
		return fmt.Errorf("requested folder is not in shared hierarchy")
	}

	log.Printf("[validateFolderAccess] Access validation successful")
	return nil
}

func (s *ShareService) GetSharedFolderStructure(ctx context.Context, shareID string, userID string) ([]domain.Folder, error) {
	// Получаем share
	share, err := s.shareRepo.GetByID(ctx, shareID)
	if err != nil {
		return nil, fmt.Errorf("failed to get share: %w", err)
	}

	// Проверяем доступ пользователя
	if !s.hasAccess(share, userID) {
		return nil, fmt.Errorf("access denied")
	}

	// Получаем структуру папок
	folders, err := s.shareRepo.GetSharedFolderStructure(ctx, shareID)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder structure: %w", err)
	}

	return folders, nil
}
