package service

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"log"
	"synxrondrive/internal/domain"
	"synxrondrive/internal/repository"
	"synxrondrive/internal/service/s3"
	"time"
)

type TrashService struct {
	trashRepo    *repository.TrashRepository
	fileRepo     *repository.FileRepository
	folderRepo   *repository.FolderRepository
	s3Client     s3.Storage
	quotaService *StorageQuotaService // Добавляем quotaService
}

func NewTrashService(
	trashRepo *repository.TrashRepository,
	fileRepo *repository.FileRepository,
	folderRepo *repository.FolderRepository,
	s3Client s3.Storage,
	quotaService *StorageQuotaService, // Добавляем параметр
) *TrashService {
	return &TrashService{
		trashRepo:    trashRepo,
		fileRepo:     fileRepo,
		folderRepo:   folderRepo,
		s3Client:     s3Client,
		quotaService: quotaService,
	}
}

// GetTrashItems получает список элементов в корзине
func (s *TrashService) GetTrashItems(ctx context.Context, ownerID string) ([]domain.TrashItem, error) {
	if ownerID == "" {
		return nil, fmt.Errorf("owner id is required")
	}

	return s.trashRepo.GetTrashItems(ctx, ownerID)
}

// UpdateRetentionPeriod обновляет период хранения файлов в корзине
func (s *TrashService) UpdateRetentionPeriod(ctx context.Context, ownerID string, period string) error {
	if ownerID == "" {
		return fmt.Errorf("owner id is required")
	}

	// Проверяем корректность периода
	_, err := time.ParseDuration(period)
	if err != nil {
		return fmt.Errorf("invalid retention period format: %w", err)
	}

	settings := &domain.TrashSettings{
		OwnerID:         ownerID,
		RetentionPeriod: period,
	}

	return s.trashRepo.UpdateSettings(ctx, settings)
}

// MoveToTrash перемещает элемент в корзину
func (s *TrashService) MoveToTrash(ctx context.Context, itemID string, itemType string, ownerID string) error {
	if itemID == "" || itemType == "" || ownerID == "" {
		return fmt.Errorf("all parameters are required")
	}

	if itemType != "file" && itemType != "folder" {
		return fmt.Errorf("invalid item type: must be 'file' or 'folder'")
	}

	return s.trashRepo.MoveToTrash(ctx, itemID, itemType, ownerID)
}

// RestoreFromTrash восстанавливает элемент из корзины
func (s *TrashService) RestoreFromTrash(ctx context.Context, itemID string, itemType string, ownerID string) error {
	if itemID == "" || itemType == "" || ownerID == "" {
		return fmt.Errorf("all parameters are required")
	}

	if itemType != "file" && itemType != "folder" {
		return fmt.Errorf("invalid item type: must be 'file' or 'folder'")
	}

	return s.trashRepo.RestoreItem(ctx, itemID, itemType, ownerID)
}

// EmptyTrash полностью очищает корзину пользователя
func (s *TrashService) EmptyTrash(ctx context.Context, ownerID string) error {
	if ownerID == "" {
		return fmt.Errorf("owner id is required")
	}

	// Получаем все файлы из корзины
	items, err := s.trashRepo.GetTrashItems(ctx, ownerID)
	if err != nil {
		return fmt.Errorf("failed to get trash items: %w", err)
	}

	// Начинаем транзакцию
	tx, err := s.trashRepo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Удаляем файлы из S3
	for _, item := range items {
		if item.Type == "file" {
			s3Key := fmt.Sprintf("personal_drive_files/%s/%s", ownerID, item.ID)
			if err := s.s3Client.DeleteObject(s3Key); err != nil {
				// Логируем ошибку, но продолжаем удаление
				log.Printf("warning: failed to delete file %s from S3: %v\n", item.ID, err)
			}
		}
	}

	// Очищаем корзину в базе данных
	if err := s.trashRepo.EmptyTrash(ctx, ownerID); err != nil {
		return err
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// После успешного удаления обновляем использованное пространство
	if err := s.quotaService.UpdateUsedSpace(ctx, ownerID); err != nil {
		// Логируем ошибку, но не прерываем выполнение
		log.Printf("warning: failed to update storage quota: %v", err)
	}

	return nil
}

// GetSettings получает настройки корзины пользователя
func (s *TrashService) GetSettings(ctx context.Context, ownerID string) (*domain.TrashSettings, error) {
	if ownerID == "" {
		return nil, fmt.Errorf("owner id is required")
	}

	return s.trashRepo.GetSettings(ctx, ownerID)
}

// DeletePermanently окончательно удаляет элемент из корзины
func (s *TrashService) DeletePermanently(ctx context.Context, itemID string, itemType string, ownerID string) error {
	// Если удаляем файл, пытаемся удалить его из S3
	if itemType == "file" {
		// Получаем полную информацию о файле перед удалением
		fileUUID, err := uuid.Parse(itemID)
		if err == nil {
			// Пытаемся получить информацию о файле из базы данных
			file, err := s.fileRepo.GetByUUID(ctx, fileUUID)
			if err == nil {
				// Определяем правильный путь к файлу в S3
				s3Key := fmt.Sprintf("personal_drive_files/%s/%s", ownerID, itemID)

				// Проверяем, является ли файл записью конференции
				if file.Metadata != nil {
					// Проверяем наличие признака записи в метаданных
					if isRecording, ok := file.Metadata["isRecording"]; ok && isRecording.(bool) {
						// Если в метаданных есть прямой путь, используем его
						if s3Path, ok := file.Metadata["s3Path"].(string); ok && s3Path != "" {
							s3Key = s3Path
							log.Printf("Используем путь из метаданных для удаления файла: %s", s3Key)
						} else {
							// Используем альтернативный путь для записей
							s3Key = fmt.Sprintf("recordings/personal_recordings/%s/%s", ownerID, file.Name)
							log.Printf("Используем альтернативный путь для удаления записи: %s", s3Key)
						}
					}
				}

				// Удаляем файл из S3
				if err := s.s3Client.DeleteObject(s3Key); err != nil {
					log.Printf("Warning: failed to delete file from S3 by primary path: %v", err)

					// Если не удалось удалить по основному пути, пробуем альтернативный
					if s3Key != fmt.Sprintf("recordings/personal_recordings/%s/%s", ownerID, file.Name) {
						altKey := fmt.Sprintf("recordings/personal_recordings/%s/%s", ownerID, file.Name)
						if altErr := s.s3Client.DeleteObject(altKey); altErr != nil {
							log.Printf("Warning: also failed to delete file from S3 by alternative path: %v", altErr)
						} else {
							log.Printf("Successfully deleted file by alternative path: %s", altKey)
						}
					}
				}
			} else {
				// Если не удалось получить информацию о файле, пытаемся удалить по стандартному пути
				s3Key := fmt.Sprintf("personal_drive_files/%s/%s", ownerID, itemID)
				if err := s.s3Client.DeleteObject(s3Key); err != nil {
					log.Printf("Warning: failed to delete file from S3: %v", err)
				}
			}
		} else {
			// Если не удалось распарсить UUID, пытаемся удалить по стандартному пути
			s3Key := fmt.Sprintf("personal_drive_files/%s/%s", ownerID, itemID)
			if err := s.s3Client.DeleteObject(s3Key); err != nil {
				log.Printf("Warning: failed to delete file from S3: %v", err)
			}
		}
	}

	return s.trashRepo.DeleteItemPermanently(ctx, itemID, itemType, ownerID)
}

// AutoCleanup запускает автоматическую очистку корзины
func (s *TrashService) AutoCleanup(ctx context.Context) error {
	deletedFiles, err := s.trashRepo.RunCleanup(ctx)
	if err != nil {
		return fmt.Errorf("failed to run database cleanup: %w", err)
	}

	// Удаляем файлы из S3
	for _, file := range deletedFiles {
		fileUUID, err := uuid.Parse(file.UUID)
		if err != nil {
			log.Printf("Warning: invalid UUID for file: %s", file.UUID)
			continue
		}

		// Получаем полную информацию о файле
		fileInfo, err := s.fileRepo.GetByUUID(ctx, fileUUID)
		if err != nil {
			// Если не удалось получить информацию, удаляем по стандартному пути
			s3Key := fmt.Sprintf("personal_drive_files/%s/%s", file.OwnerID, file.UUID)
			if err := s.s3Client.DeleteObject(s3Key); err != nil {
				log.Printf("Warning: failed to delete file %s from S3: %v", file.UUID, err)
			}
			continue
		}

		// Определяем правильный путь к файлу
		s3Key := fmt.Sprintf("personal_drive_files/%s/%s", file.OwnerID, file.UUID)

		// Проверяем, является ли файл записью конференции
		if fileInfo.Metadata != nil {
			if isRecording, ok := fileInfo.Metadata["isRecording"]; ok && isRecording.(bool) {
				if s3Path, ok := fileInfo.Metadata["s3Path"].(string); ok && s3Path != "" {
					s3Key = s3Path
					log.Printf("Using path from metadata for cleanup: %s", s3Key)
				} else {
					s3Key = fmt.Sprintf("recordings/personal_recordings/%s/%s", file.OwnerID, fileInfo.Name)
					log.Printf("Using alternative path for recording cleanup: %s", s3Key)
				}
			}
		}

		// Удаляем файл из S3
		if err := s.s3Client.DeleteObject(s3Key); err != nil {
			log.Printf("Warning: failed to delete file %s from S3 by primary path: %v", file.UUID, err)

			// Если не удалось удалить по основному пути, пробуем альтернативный
			if s3Key != fmt.Sprintf("recordings/personal_recordings/%s/%s", file.OwnerID, fileInfo.Name) {
				altKey := fmt.Sprintf("recordings/personal_recordings/%s/%s", file.OwnerID, fileInfo.Name)
				if altErr := s.s3Client.DeleteObject(altKey); altErr != nil {
					log.Printf("Warning: also failed to delete file from S3 by alternative path: %v", altErr)
				} else {
					log.Printf("Successfully deleted file by alternative path: %s", altKey)
				}
			}
		}
	}

	return nil
}
