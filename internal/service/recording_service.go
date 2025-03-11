package service

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"log"
	"strings"
	"synxrondrive/internal/domain"
	"synxrondrive/internal/repository"
	"synxrondrive/internal/service/s3"
	pb "synxrondrive/pkg/proto/recording_v1"
	"time"
)

type RecordingService struct {
	recordingRepo *repository.RecordingRepository
	fileRepo      *repository.FileRepository
	folderRepo    *repository.FolderRepository
	fileService   *FileService
	s3Client      s3.Storage
}

func NewRecordingService(
	recordingRepo *repository.RecordingRepository,
	fileRepo *repository.FileRepository,
	folderRepo *repository.FolderRepository,
	fileService *FileService,
	s3Client s3.Storage,
) *RecordingService {
	return &RecordingService{
		recordingRepo: recordingRepo,
		fileRepo:      fileRepo,
		folderRepo:    folderRepo,
		fileService:   fileService,
		s3Client:      s3Client,
	}
}

func (s *RecordingService) SaveRecording(ctx context.Context, req *pb.SaveRecordingRequest) (*pb.SaveRecordingResponse, error) {
	log.Printf("[RecordingService] Received request to save recording %s for user %s", req.RecordingId, req.UserId)

	// Получаем или создаем папку "Записи видеовстреч"
	folder, err := s.recordingRepo.GetOrCreateRecordingsFolder(ctx, req.UserId)
	if err != nil {
		return nil, fmt.Errorf("failed to get recordings folder: %w", err)
	}

	// Проверяем существование файла в S3
	s3Path := req.FilePath
	exists, fileSize, err := s.checkFileExistsInS3(ctx, s3Path)
	if err != nil {
		log.Printf("[RecordingService] Warning: Error checking file existence: %v", err)
		// Продолжаем выполнение даже при ошибке
	}

	if exists {
		log.Printf("[RecordingService] File already exists in S3: %s, size: %d bytes", s3Path, fileSize)
		// Если получили размер от S3, используем его
		if fileSize > 0 {
			req.SizeBytes = fileSize
		}
	} else {
		log.Printf("[RecordingService] File not found in S3 yet: %s, will check periodically", s3Path)
	}

	// Генерируем UUID для файла
	fileUUID := uuid.New()

	// Создаем запись о файле в БД
	file := &domain.File{
		UUID:           fileUUID,
		Name:           req.FileName,
		MIMEType:       req.MimeType,
		SizeBytes:      req.SizeBytes,
		FolderID:       folder.ID,
		OwnerID:        req.UserId,
		CurrentVersion: 1,
		Metadata: map[string]interface{}{
			"isRecording": true,
			"egressId":    req.RecordingId,
			"roomId":      req.RoomId,
			"s3Path":      s3Path,
			"verified":    exists,
		},
	}

	// Начинаем транзакцию
	tx, err := s.fileRepo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Сохраняем информацию о файле в БД
	if err := s.fileRepo.Create(ctx, file); err != nil {
		return nil, fmt.Errorf("failed to create file record: %w", err)
	}

	// Создаем запись о версии файла
	version := &domain.FileVersion{
		FileUUID:      fileUUID,
		VersionNumber: 1,
		S3Key:         s3Path,
		SizeBytes:     req.SizeBytes,
	}

	if err := s.fileRepo.CreateFileVersion(ctx, tx, version); err != nil {
		return nil, fmt.Errorf("failed to create file version: %w", err)
	}

	// Сохраняем информацию о записи
	recording := &domain.Recording{
		FileUUID:  fileUUID,
		RoomID:    req.RoomId,
		EgressID:  req.RecordingId,
		UserID:    req.UserId,
		FolderID:  folder.ID,
		S3Path:    s3Path,
		Verified:  exists,
		CreatedAt: time.Now(),
	}

	if err := s.recordingRepo.SaveRecording(ctx, recording); err != nil {
		return nil, fmt.Errorf("failed to save recording info: %w", err)
	}

	// Обновляем метаданные папки после создания файла
	// Только если файл уже существует в S3, иначе это будет сделано позже
	if exists {
		// Обновляем метаданные папки
		if err := s.recordingRepo.UpdateFolderMetadata(ctx, folder.ID, req.SizeBytes, 1); err != nil {
			log.Printf("[RecordingService] Warning: Failed to update folder metadata: %v", err)
			// Продолжаем выполнение даже при ошибке
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// ДОБАВЛЕНО: Обновляем использованное пространство пользователя
	if s.fileService != nil && s.fileService.quotaService != nil {
		go func() {
			updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := s.fileService.quotaService.UpdateUsedSpace(updateCtx, req.UserId); err != nil {
				log.Printf("[RecordingService] Failed to update user quota: %v", err)
			} else {
				log.Printf("[RecordingService] Successfully updated user quota for %s", req.UserId)
			}
		}()
	}

	// Если файл еще не существует, запускаем процесс проверки его появления
	if !exists {
		go s.waitForRecordingFile(context.Background(), fileUUID, s3Path, req.RecordingId, folder.ID)
	}

	log.Printf("[RecordingService] Successfully registered recording %s for user %s", req.RecordingId, req.UserId)

	log.Printf("[RecordingService] Используем папку для записей: %s (ID: %d)",
		folder.Name, folder.ID)

	return &pb.SaveRecordingResponse{
		FileId:   fileUUID.String(),
		FolderId: fmt.Sprintf("%d", folder.ID),
	}, nil
}

// Проверяет наличие файла в S3 хранилище
func (s *RecordingService) checkFileExistsInS3(ctx context.Context, path string) (bool, int64, error) {
	log.Printf("[RecordingService] Checking if file exists in S3: %s", path)

	// Здесь используем существующий метод из s3Client
	// Пример:
	object, err := s.s3Client.GetObject(ctx, path)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "NoSuchKey") {
			return false, 0, nil
		}
		return false, 0, err
	}
	defer object.Close()

	return true, object.ContentLength(), nil
}

// Ожидает появления файла в S3 и обновляет информацию о нем
func (s *RecordingService) waitForRecordingFile(ctx context.Context, fileUUID uuid.UUID, s3Path string, egressID string, folderID int64) {
	log.Printf("[RecordingService] Starting to wait for recording file: %s", s3Path)

	// Настройки ожидания
	maxAttempts := 60       // Максимальное количество попыток (60 минут)
	interval := time.Minute // Интервал между попытками (1 минута)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Проверяем, есть ли файл в S3
		exists, size, err := s.checkFileExistsInS3(ctx, s3Path)
		if err != nil {
			log.Printf("[RecordingService] Error checking file: %v", err)
			time.Sleep(interval)
			continue
		}

		if exists {
			log.Printf("[RecordingService] File found after %d attempts: %s, size: %d", attempt+1, s3Path, size)

			// Обновляем информацию о файле
			tx, err := s.fileRepo.BeginTx(ctx)
			if err != nil {
				log.Printf("[RecordingService] Error beginning transaction: %v", err)
				return
			}

			// Получаем текущий размер файла
			var currentSize int64
			err = tx.QueryRowContext(ctx,
				"SELECT size_bytes FROM files WHERE uuid = $1",
				fileUUID).Scan(&currentSize)
			if err != nil {
				tx.Rollback()
				log.Printf("[RecordingService] Error getting current file size: %v", err)
				return
			}

			// Обновляем размер файла
			if err := s.fileRepo.UpdateFileSize(ctx, tx, fileUUID, size); err != nil {
				tx.Rollback()
				log.Printf("[RecordingService] Error updating file size: %v", err)
				return
			}

			// Обновляем статус записи
			if err := s.recordingRepo.UpdateRecordingStatus(ctx, fileUUID, true); err != nil {
				tx.Rollback()
				log.Printf("[RecordingService] Error updating recording status: %v", err)
				return
			}

			// Обновляем метаданные папки с учетом нового размера файла
			deltaSize := size - currentSize
			if deltaSize != 0 {
				if err := s.recordingRepo.UpdateFolderMetadata(ctx, folderID, deltaSize, 0); err != nil {
					tx.Rollback()
					log.Printf("[RecordingService] Error updating folder metadata: %v", err)
					return
				}
			}

			if err := tx.Commit(); err != nil {
				log.Printf("[RecordingService] Error committing transaction: %v", err)
				return
			}

			// ДОБАВЛЕНО: Получаем userId файла и обновляем квоту
			var ownerID string
			err = s.fileRepo.GetDB().QueryRowContext(ctx,
				"SELECT owner_id FROM files WHERE uuid = $1",
				fileUUID).Scan(&ownerID)

			if err != nil {
				log.Printf("[RecordingService] Error getting file owner: %v", err)
			} else if s.fileService != nil && s.fileService.quotaService != nil {
				// Обновляем квоту пользователя
				go func() {
					updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()

					if err := s.fileService.quotaService.UpdateUsedSpace(updateCtx, ownerID); err != nil {
						log.Printf("[RecordingService] Failed to update user quota after size change: %v", err)
					} else {
						log.Printf("[RecordingService] Successfully updated user quota for %s after size change", ownerID)
					}
				}()
			}

			log.Printf("[RecordingService] Successfully updated recording file information for %s", egressID)
			return
		}

		log.Printf("[RecordingService] File not found yet, attempt %d/%d: %s", attempt+1, maxAttempts, s3Path)
		time.Sleep(interval)
	}

	log.Printf("[RecordingService] Failed to find file after %d attempts: %s", maxAttempts, s3Path)
}
