package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"log"
	"mime/multipart"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"synxrondrive/internal/domain"
	"synxrondrive/internal/repository"
	"synxrondrive/internal/service/s3"
	"time"
)

// Определение констант для работы с файлами
const (
	maxFileSize    = 100 * 1024 * 1024 // 100MB максимальный размер файла
	downloadBuffer = 1 * 1024 * 1024   // 1MB размер буфера для скачивания
)

const (
	maxConcurrentUploads = 5                // Максимальное количество параллельных загрузок
	chunkSize            = 10 * 1024 * 1024 // 10MB для чанков при загрузке
)

// Определение пользовательских ошибок
var (
	errFileTooLarge  = errors.New("file size exceeds maximum allowed size")
	errInvalidFile   = errors.New("invalid file")
	errAccessDenied  = errors.New("access denied")
	errFileNotFound  = errors.New("file not found")
	errS3Operation   = errors.New("s3 operation failed")
	errDatabaseError = errors.New("database operation failed")
)

// FileService представляет сервис для работы с файлами
type FileService struct {
	fileRepo          *repository.FileRepository
	folderRepo        *repository.FolderRepository
	shareRepo         *repository.ShareRepository
	s3Client          s3.Storage
	permissionService *PermissionService
	quotaService      *StorageQuotaService
}

func NewFileService(
	fileRepo *repository.FileRepository,
	folderRepo *repository.FolderRepository,
	shareRepo *repository.ShareRepository,
	s3Client s3.Storage,
	permissionService *PermissionService,
	quotaService *StorageQuotaService,
) *FileService {
	return &FileService{
		fileRepo:          fileRepo,
		folderRepo:        folderRepo,
		shareRepo:         shareRepo,
		s3Client:          s3Client,
		permissionService: permissionService,
		quotaService:      quotaService,
	}
}

// UploadFile загружает файл в хранилище
func (s *FileService) UploadFile(
	ctx context.Context,
	header *multipart.FileHeader,
	file multipart.File,
	folderID int64,
	userID string,
) (*domain.File, error) {

	// Проверяем наличие свободного места
	spaceAvailable, err := s.quotaService.CheckSpaceAvailable(ctx, userID, header.Size)
	if err != nil {
		return nil, fmt.Errorf("failed to check available space: %w", err)
	}

	if !spaceAvailable {
		return nil, fmt.Errorf("not enough storage space available")
	}

	// Проверяем входные параметры
	if header == nil || file == nil || userID == "" {
		return nil, fmt.Errorf("%w: missing required parameters", errInvalidFile)
	}

	// Проверяем размер файла
	if header.Size > maxFileSize {
		return nil, fmt.Errorf("%w: max size is %d bytes", errFileTooLarge, maxFileSize)
	}

	// Если folderID = 0, получаем корневую папку пользователя
	if folderID == 0 {
		rootFolder, err := s.getRootFolder(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to get root folder: %w", err)
		}
		folderID = rootFolder.ID
	}

	// Получаем информацию о папке
	folder, err := s.folderRepo.GetByID(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}

	// Проверяем права на загрузку в папку
	if folder.OwnerID != userID {
		hasPermission, err := s.permissionService.CheckSharedFolderPermission(
			ctx,
			userID,
			folderID,
			OperationUpload,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to check permissions: %w", err)
		}
		if !hasPermission {
			return nil, errAccessDenied
		}
	}

	// Проверяем, существует ли файл с таким именем
	existingFile, err := s.fileRepo.CheckFileExists(ctx, folderID, header.Filename)
	if err != nil {
		return nil, fmt.Errorf("failed to check file existence: %w", err)
	}

	// Если файл существует и у пользователя есть права на редактирование,
	// создаем новую версию
	if existingFile != nil {
		hasPermission, err := s.permissionService.CheckSharedFolderPermission(
			ctx,
			userID,
			folderID,
			OperationEdit,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to check edit permissions: %w", err)
		}
		if !hasPermission && existingFile.OwnerID != userID {
			return nil, errAccessDenied
		}
		return s.createFileVersion(ctx, file, header, existingFile, folder.OwnerID)
	}

	// Создаем новый файл
	fileUUID := uuid.New()
	s3Key := fmt.Sprintf("personal_drive_files/%s/%s", folder.OwnerID, fileUUID.String())

	// Определяем тип контента
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Создаем новую запись о файле
	newFile := &domain.File{
		UUID:           fileUUID,
		Name:           filepath.Clean(header.Filename),
		MIMEType:       contentType,
		SizeBytes:      header.Size,
		FolderID:       folderID,
		OwnerID:        folder.OwnerID, // Владельцем будет владелец папки
		CurrentVersion: 1,
	}

	// Начинаем транзакцию
	tx, err := s.fileRepo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Загружаем файл в S3
	filePtr := &file
	if err := s.s3Client.UploadFile(s3Key, filePtr); err != nil {
		return nil, fmt.Errorf("%w: %v", errS3Operation, err)
	}

	// Создаем запись в БД
	if err := s.fileRepo.Create(ctx, newFile); err != nil {
		// При ошибке удаляем файл из S3
		if deleteErr := s.s3Client.DeleteObject(s3Key); deleteErr != nil {
			log.Printf("failed to delete file from s3 after db error: %v", deleteErr)
		}
		return nil, fmt.Errorf("%w: %v", errDatabaseError, err)
	}

	// Создаем версию файла
	version := &domain.FileVersion{
		FileUUID:      fileUUID,
		VersionNumber: 1,
		S3Key:         s3Key,
		SizeBytes:     header.Size,
	}

	if err := s.fileRepo.CreateFileVersion(ctx, tx, version); err != nil {
		return nil, fmt.Errorf("failed to create file version: %w", err)
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// После успешной загрузки обновляем использованное пространство
	if err := s.quotaService.UpdateUsedSpace(ctx, userID); err != nil {
		log.Printf("Failed to update used space: %v", err)
	}

	return newFile, nil
}

// DownloadFile скачивает файл из хранилища
func (s *FileService) DownloadFile(ctx context.Context, fileUUID uuid.UUID, userID string) (*domain.FileDownload, error) {
	// Получаем информацию о файле с проверкой shared доступа
	file, err := s.GetFileInfo(ctx, fileUUID, userID)
	if err != nil {
		return nil, err
	}

	// Теперь, когда доступ проверен, скачиваем файл
	s3Key := fmt.Sprintf("personal_drive_files/%s/%s", file.OwnerID, fileUUID.String())
	body, err := s.s3Client.GetObject(ctx, s3Key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errS3Operation, err)
	}
	defer body.Close()

	data := make([]byte, 0, file.SizeBytes)
	buffer := bytes.NewBuffer(data)

	_, err = io.Copy(buffer, body)
	if err != nil {
		return nil, fmt.Errorf("error reading from s3: %w", err)
	}

	return &domain.FileDownload{
		File: file,
		Data: buffer.Bytes(),
	}, nil
}

// DeleteFile удаляет файл из хранилища
func (s *FileService) DeleteFile(ctx context.Context, fileUUID uuid.UUID, userID string) error {
	// Получаем информацию о файле
	file, err := s.fileRepo.GetByUUID(ctx, fileUUID)
	if err != nil {
		return fmt.Errorf("%w: %v", errFileNotFound, err)
	}

	// Проверяем права на удаление
	if file.OwnerID != userID {
		// Проверяем права в родительской папке
		hasPermission, err := s.permissionService.CheckSharedFolderPermission(
			ctx,
			userID,
			file.FolderID,
			OperationDelete,
		)
		if err != nil {
			return fmt.Errorf("failed to check permissions: %w", err)
		}
		if !hasPermission {
			return errAccessDenied
		}
	}

	// Начинаем транзакцию
	tx, err := s.fileRepo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Получаем все версии файла для удаления из S3
	versions, err := s.fileRepo.GetFileVersions(ctx, fileUUID)
	if err != nil {
		return fmt.Errorf("failed to get file versions: %w", err)
	}

	// Удаляем все версии из S3
	for _, version := range versions {
		if err := s.s3Client.DeleteObject(version.S3Key); err != nil {
			log.Printf("warning: failed to delete version %d from S3: %v", version.VersionNumber, err)
		}
	}

	// Удаляем файл из БД
	if err := s.fileRepo.Delete(ctx, fileUUID); err != nil {
		return fmt.Errorf("failed to delete file from database: %w", err)
	}

	// Удаляем превью, если оно есть
	previewKey := fmt.Sprintf("previews/%s", fileUUID.String())
	if err := s.s3Client.DeleteObject(previewKey); err != nil {
		log.Printf("warning: failed to delete preview from S3: %v", err)
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// createFileVersion создает новую версию существующего файла
func (s *FileService) createFileVersion(
	ctx context.Context,
	file multipart.File,
	header *multipart.FileHeader,
	existingFile *domain.File,
	ownerID string,
) (*domain.File, error) {
	// Начинаем транзакцию
	tx, err := s.fileRepo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Формируем ключ для S3
	s3Key := fmt.Sprintf("personal_drive_files/%s/%s", ownerID, existingFile.UUID)

	// Создаем новую версию в БД
	newVersion := &domain.FileVersion{
		FileUUID:      existingFile.UUID,
		VersionNumber: existingFile.CurrentVersion + 1,
		S3Key:         s3Key,
		SizeBytes:     header.Size,
	}

	// Загружаем файл в S3
	filePtr := &file
	if err := s.s3Client.UploadFile(s3Key, filePtr); err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	// Создаем запись о версии
	if err := s.fileRepo.CreateFileVersion(ctx, tx, newVersion); err != nil {
		// При ошибке удаляем файл из S3
		if deleteErr := s.s3Client.DeleteObject(s3Key); deleteErr != nil {
			log.Printf("failed to delete file from s3 after version creation error: %v", deleteErr)
		}
		return nil, fmt.Errorf("failed to create file version: %w", err)
	}

	// Обновляем информацию о файле
	existingFile.CurrentVersion++
	existingFile.SizeBytes = header.Size
	if err := s.fileRepo.Update(ctx, existingFile); err != nil {
		return nil, fmt.Errorf("failed to update file: %w", err)
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return existingFile, nil
}

// getRootFolder получает или создает корневую папку пользователя
func (s *FileService) getRootFolder(ctx context.Context, ownerID string) (*domain.Folder, error) {
	rootFolder, err := s.folderRepo.GetRootFolder(ctx, ownerID)
	if err == nil {
		return rootFolder, nil
	}

	rootFolder = &domain.Folder{
		Name:    "Root",
		OwnerID: ownerID,
	}

	if err := s.folderRepo.Create(ctx, rootFolder); err != nil {
		return nil, fmt.Errorf("failed to create root folder: %w", err)
	}

	return rootFolder, nil
}

// GetFilesByFolder получает список файлов в папке
func (s *FileService) GetFilesByFolder(ctx context.Context, folderID int64, ownerID string) ([]domain.File, error) {
	folder, err := s.folderRepo.GetByID(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}

	if folder.OwnerID != ownerID {
		return nil, errAccessDenied
	}

	files, err := s.fileRepo.GetByFolder(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get files: %w", err)
	}

	return files, nil
}

// UpdateFileName обновляет имя файла
func (s *FileService) UpdateFileName(ctx context.Context, fileUUID uuid.UUID, newName string, ownerID string) error {
	if newName == "" || ownerID == "" {
		return fmt.Errorf("%w: missing required parameters", errInvalidFile)
	}

	file, err := s.fileRepo.GetByUUID(ctx, fileUUID)
	if err != nil {
		return fmt.Errorf("%w: %v", errFileNotFound, err)
	}

	if file.OwnerID != ownerID {
		return errAccessDenied
	}

	file.Name = filepath.Clean(newName)
	if err := s.fileRepo.Update(ctx, file); err != nil {
		return fmt.Errorf("%w: %v", errDatabaseError, err)
	}

	return nil
}

// GetFileInfo получает информацию о файле
func (s *FileService) GetFileInfo(ctx context.Context, fileUUID uuid.UUID, userID string) (*domain.File, error) {
	// Получаем информацию о файле
	file, err := s.fileRepo.GetByUUID(ctx, fileUUID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errFileNotFound, err)
	}

	// Если пользователь владелец, разрешаем доступ
	if file.OwnerID == userID {
		return file, nil
	}

	// Проверяем прямой shared доступ к файлу
	shares, err := s.shareRepo.GetSharesByResource(ctx, fileUUID.String(), domain.ResourceTypeFile)
	if err != nil {
		return nil, fmt.Errorf("failed to check shared access: %w", err)
	}

	// Проверяем все shares файла
	for _, share := range shares {
		// Проверяем срок действия share
		if share.ExpiresAt != nil && share.ExpiresAt.Before(time.Now()) {
			continue
		}

		if share.Token != "" || strings.Contains(share.UserIDs, userID) {
			return file, nil
		}
	}

	// Получаем всю иерархию папок от текущей до корневой
	var currentFolderID = file.FolderID
	for currentFolderID != 0 {
		folder, err := s.folderRepo.GetByID(ctx, currentFolderID)
		if err != nil {
			return nil, fmt.Errorf("failed to get folder: %w", err)
		}

		// Получаем все shares для текущей папки и всех её родителей
		folderShares, err := s.shareRepo.GetSharesByResource(ctx, strconv.FormatInt(currentFolderID, 10), domain.ResourceTypeFolder)
		if err != nil {
			return nil, fmt.Errorf("failed to check folder shares: %w", err)
		}

		for _, share := range folderShares {
			if share.ExpiresAt != nil && share.ExpiresAt.Before(time.Now()) {
				continue
			}

			// Проверяем доступ. Если у пользователя есть доступ к любой папке в иерархии,
			// значит у него есть доступ и к файлу
			if share.Token != "" || strings.Contains(share.UserIDs, userID) {
				// Важно! Проверяем путь файла относительно shared папки
				sharedFolder, err := s.folderRepo.GetByID(ctx, currentFolderID)
				if err != nil {
					return nil, fmt.Errorf("failed to get shared folder: %w", err)
				}

				fileFolder, err := s.folderRepo.GetByID(ctx, file.FolderID)
				if err != nil {
					return nil, fmt.Errorf("failed to get file folder: %w", err)
				}

				// Проверяем, что путь к файлу начинается с пути shared папки
				if strings.HasPrefix(fileFolder.Path, sharedFolder.Path) {
					return file, nil
				}
			}
		}

		// Если папка корневая (parent_id = nil), прерываем цикл
		if folder.ParentID == nil {
			break
		}
		currentFolderID = *folder.ParentID
	}

	return nil, errAccessDenied
}

// GetFileData изменить для поддержки записей
func (s *FileService) GetFileData(ctx context.Context, fileUUID uuid.UUID, userID string) (io.Reader, error) {
	// Получаем информацию о файле
	file, err := s.fileRepo.GetByUUID(ctx, fileUUID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errFileNotFound, err)
	}

	// Проверяем доступ
	if file.OwnerID != userID {
		// Код проверки shared доступа...
	}

	// Проверяем, является ли файл записью видеоконференции
	isRecording := false
	s3Path := ""
	if file.Metadata != nil {
		// Здесь не нужно приведение типа, так как Metadata уже нужного типа
		if val, ok := file.Metadata["isRecording"]; ok {
			if boolVal, ok := val.(bool); ok && boolVal {
				isRecording = true
				if pathVal, ok := file.Metadata["s3Path"]; ok {
					if strVal, ok := pathVal.(string); ok {
						s3Path = strVal
					}
				}
			}
		}
	}

	// Если это запись и у нас есть прямой путь в S3
	if isRecording && s3Path != "" {
		log.Printf("[FileService] Getting recording file from S3: %s", s3Path)
		data, err := s.s3Client.GetObject(ctx, s3Path)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errS3Operation, err)
		}
		return data, nil
	}

	// Обычная логика получения файла...
	s3Key := fmt.Sprintf("personal_drive_files/%s/%s", file.OwnerID, fileUUID.String())
	data, err := s.s3Client.GetObject(ctx, s3Key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errS3Operation, err)
	}

	return data, nil
}

// checkSharedAccess проверяет наличие общего доступа к файлу
func (s *FileService) checkSharedAccess(ctx context.Context, fileUUID uuid.UUID, userID string) (bool, error) {
	// Здесь должна быть проверка наличия общего доступа к файлу через shareRepo
	// Это упрощенная версия, вам нужно реализовать её в соответствии с вашей логикой общего доступа
	return false, nil
}

// CheckFileExists проверяет существование файла в папке
func (s *FileService) CheckFileExists(ctx context.Context, folderID int64, fileName string) (*domain.File, error) {
	return s.fileRepo.CheckFileExists(ctx, folderID, fileName)
}

// UploadFileVersion в file_service.go
func (s *FileService) UploadFileVersion(ctx context.Context, file multipart.File, header *multipart.FileHeader,
	existingFile *domain.File, ownerID string) error {

	// Начинаем транзакцию
	tx, err := s.fileRepo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	s3Key := fmt.Sprintf("personal_drive_files/%s/%s", ownerID, existingFile.UUID)
	filePtr := &file

	// Удаляем старое превью с использованием транзакции
	previewKey := fmt.Sprintf("previews/%s", existingFile.UUID.String())
	if err := s.s3Client.DeleteObject(previewKey); err != nil {
		log.Printf("Warning: failed to delete old preview from S3: %v", err)
	}

	if err := s.fileRepo.DeletePreview(ctx, tx, existingFile.UUID); err != nil {
		log.Printf("Warning: failed to delete preview record: %v", err)
	}

	existingFile.SizeBytes = header.Size
	existingFile.CurrentVersion++
	if err := s.fileRepo.Update(ctx, existingFile); err != nil {
		return fmt.Errorf("failed to update DB: %w", err)
	}

	if err := s.s3Client.UploadFile(s3Key, filePtr); err != nil {
		return fmt.Errorf("failed to upload: %w", err)
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// uploadFileParallel реализует параллельную загрузку файла чанками
func (s *FileService) uploadFileParallel(ctx context.Context, file multipart.File, s3Key string, totalSize int64) error {
	// Рассчитываем количество чанков
	chunks := (totalSize + chunkSize - 1) / chunkSize

	// Создаем пул воркеров
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxConcurrentUploads)
	errors := make(chan error, chunks)

	// Загружаем чанки параллельно
	for i := int64(0); i < chunks; i++ {
		wg.Add(1)
		semaphore <- struct{}{} // Ограничиваем количество параллельных загрузок

		go func(chunkNum int64) {
			defer wg.Done()
			defer func() { <-semaphore }()

			// Читаем чанк
			offset := chunkNum * chunkSize
			var size int64
			if offset+chunkSize > totalSize {
				size = totalSize - offset
			} else {
				size = chunkSize
			}

			chunk := make([]byte, size)
			_, err := file.ReadAt(chunk, offset)
			if err != nil && err != io.EOF {
				errors <- fmt.Errorf("failed to read chunk %d: %w", chunkNum, err)
				return
			}

			// Загружаем чанк в S3
			chunkKey := fmt.Sprintf("%s/chunk_%d", s3Key, chunkNum)
			err = s.s3Client.UploadBytes(chunkKey, chunk)
			if err != nil {
				errors <- fmt.Errorf("failed to upload chunk %d: %w", chunkNum, err)
				return
			}
		}(i)
	}

	// Ждем завершения всех загрузок
	wg.Wait()
	close(errors)

	// Проверяем ошибки
	for err := range errors {
		if err != nil {
			return err
		}
	}

	return nil
}

// DeleteVersion удаляет конкретную версию файла
func (s *FileService) DeleteVersion(ctx context.Context, fileUUID uuid.UUID, versionNumber int) error {
	// Начинаем транзакцию
	tx, err := s.fileRepo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Проверяем, не является ли это текущей версией
	currentVersion, err := s.fileRepo.GetCurrentVersion(ctx, tx, fileUUID)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	if versionNumber == currentVersion {
		return errors.New("cannot delete current version")
	}

	// Помечаем версию как удаленную
	err = s.fileRepo.DeleteVersion(ctx, tx, fileUUID, versionNumber)
	if err != nil {
		return fmt.Errorf("failed to mark version as deleted: %w", err)
	}

	return tx.Commit()
}

// GetFileVersions возвращает все версии файла
func (s *FileService) GetFileVersions(ctx context.Context, fileUUID uuid.UUID) ([]domain.FileVersion, error) {
	return s.fileRepo.GetFileVersions(ctx, fileUUID)
}

// GetBasicFileInfo получает базовую информацию о файле без проверки прав доступа
func (s *FileService) GetBasicFileInfo(ctx context.Context, fileUUID uuid.UUID) (*domain.File, error) {
	file, err := s.fileRepo.GetByUUID(ctx, fileUUID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errFileNotFound, err)
	}
	return file, nil
}

// GetFileDataDirect получает данные файла напрямую из S3 без проверки прав доступа
func (s *FileService) GetFileDataDirect(ctx context.Context, fileUUID uuid.UUID) (io.Reader, error) {
	file, err := s.fileRepo.GetByUUID(ctx, fileUUID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errFileNotFound, err)
	}

	// Проверяем метаданные файла для определения пути к файлу в S3
	s3Key := ""

	// Получаем метаданные, если они есть
	if file.Metadata != nil {
		// Напрямую проверяем наличие s3Path в метаданных
		if path, ok := file.Metadata["s3Path"].(string); ok && path != "" {
			s3Key = path
			log.Printf("[FileService] Используем прямой путь из метаданных для получения файла: %s", s3Key)
		}
	}

	// Если путь не найден в метаданных, используем стандартный путь
	if s3Key == "" {
		s3Key = fmt.Sprintf("personal_drive_files/%s/%s", file.OwnerID, fileUUID.String())
		log.Printf("[FileService] Используем стандартный путь для получения файла: %s", s3Key)
	}

	// Получаем данные из S3
	data, err := s.s3Client.GetObject(ctx, s3Key)
	if err != nil {
		// Если файл не найден по стандартному или метаданным пути,
		// пробуем альтернативный путь для записей LiveKit
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "NoSuchKey") {
			log.Printf("[FileService] Файл не найден по указанному пути, проверяем альтернативные пути")

			// Пробуем путь для записей LiveKit
			alternativePath := fmt.Sprintf("recordings/personal_recordings/%s/%s", file.OwnerID, file.Name)
			log.Printf("[FileService] Пробуем альтернативный путь для записи: %s", alternativePath)

			data, err = s.s3Client.GetObject(ctx, alternativePath)
			if err != nil {
				log.Printf("[FileService] Файл не найден и по альтернативному пути: %v", err)
				return nil, fmt.Errorf("%w: %v", errS3Operation, err)
			}

			log.Printf("[FileService] Файл найден по альтернативному пути!")

			// Обновляем метаданные файла, чтобы в будущем использовать правильный путь
			if file.Metadata == nil {
				file.Metadata = map[string]interface{}{
					"s3Path": alternativePath,
				}
			} else {
				// Напрямую добавляем/обновляем s3Path в метаданных
				file.Metadata["s3Path"] = alternativePath
			}

			// Сохраняем обновленные метаданные в БД асинхронно
			go func() {
				updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.fileRepo.Update(updateCtx, file); err != nil {
					log.Printf("[FileService] Ошибка обновления метаданных файла: %v", err)
				} else {
					log.Printf("[FileService] Метаданные файла успешно обновлены с путем: %s", alternativePath)
				}
			}()

			return data, nil
		}

		return nil, fmt.Errorf("%w: %v", errS3Operation, err)
	}

	return data, nil
}

// CreateContextFile создает файл с контекстом
func (s *FileService) CreateContextFile(ctx context.Context, file multipart.File, header *multipart.FileHeader, contextType string, userID string) (*domain.File, error) {
	// Проверяем размер файла
	if header.Size > maxFileSize {
		return nil, fmt.Errorf("%w: max size is %d bytes", errFileTooLarge, maxFileSize)
	}

	// Создаем новый UUID для файла
	fileUUID := uuid.New()

	// Формируем ключ для S3
	s3Key := fmt.Sprintf("personal_drive_files/%s/%s", userID, fileUUID.String())

	// Определяем тип контента
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Создаем новую запись о файле
	newFile := &domain.File{
		UUID:           fileUUID,
		Name:           filepath.Clean(header.Filename),
		MIMEType:       contentType,
		SizeBytes:      header.Size,
		OwnerID:        userID,
		ContextType:    &contextType,
		CurrentVersion: 1,
	}

	// Начинаем транзакцию
	tx, err := s.fileRepo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Загружаем файл в S3
	filePtr := &file
	if err := s.s3Client.UploadFile(s3Key, filePtr); err != nil {
		return nil, fmt.Errorf("%w: %v", errS3Operation, err)
	}

	// Создаем запись в БД
	if err := s.fileRepo.CreateContextFile(ctx, newFile); err != nil {
		// При ошибке удаляем файл из S3
		if deleteErr := s.s3Client.DeleteObject(s3Key); deleteErr != nil {
			log.Printf("failed to delete file from s3 after db error: %v", deleteErr)
		}
		return nil, fmt.Errorf("%w: %v", errDatabaseError, err)
	}

	// Создаем версию файла
	version := &domain.FileVersion{
		FileUUID:      fileUUID,
		VersionNumber: 1,
		S3Key:         s3Key,
		SizeBytes:     header.Size,
	}

	if err := s.fileRepo.CreateFileVersion(ctx, tx, version); err != nil {
		return nil, fmt.Errorf("failed to create file version: %w", err)
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// После успешной загрузки обновляем использованное пространство
	if err := s.quotaService.UpdateUsedSpace(ctx, userID); err != nil {
		log.Printf("Failed to update used space: %v", err)
	}

	return newFile, nil
}

type readCloser struct {
	io.Reader
}

func (r *readCloser) Close() error {
	if closer, ok := r.Reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// GetFileDataRange получает диапазон данных файла для потокового воспроизведения
func (s *FileService) GetFileDataRange(ctx context.Context, fileUUID uuid.UUID, userID string, start, end int64) (io.ReadCloser, error) {
	log.Printf("[FileService] Запрос диапазона для файла %s (диапазон: %d-%d)", fileUUID, start, end)
	startTime := time.Now()

	// Получаем информацию о файле
	file, err := s.GetFileInfo(ctx, fileUUID, userID)
	if err != nil {
		if strings.Contains(err.Error(), "access denied") {
			return nil, fmt.Errorf("доступ запрещен: %w", err)
		}
		return nil, fmt.Errorf("ошибка получения информации о файле: %w", err)
	}

	// Проверяем корректность диапазона
	if start < 0 {
		start = 0
	}
	if end >= file.SizeBytes {
		end = file.SizeBytes - 1
	}
	if start > end {
		return nil, fmt.Errorf("некорректный диапазон: start=%d, end=%d", start, end)
	}

	// Проверяем метаданные файла для определения пути к файлу в S3
	s3Key := ""

	// Получаем метаданные, если они есть
	if file.Metadata != nil {
		// Напрямую проверяем наличие s3Path в метаданных
		if path, ok := file.Metadata["s3Path"].(string); ok && path != "" {
			s3Key = path
			log.Printf("[FileService] Используем прямой путь из метаданных: %s", s3Key)
		}
	}

	// Если путь не найден в метаданных, используем стандартный путь
	if s3Key == "" {
		s3Key = fmt.Sprintf("personal_drive_files/%s/%s", file.OwnerID, fileUUID.String())
		log.Printf("[FileService] Используем стандартный путь: %s", s3Key)
	}

	// Получаем данные из S3 с использованием Range
	log.Printf("[FileService] Запрашиваем диапазон из S3 по пути: %s", s3Key)
	data, err := s.s3Client.GetObjectRange(ctx, s3Key, start, end)
	if err != nil {
		log.Printf("[FileService] Ошибка получения данных по пути %s: %v", s3Key, err)

		// Попробуем альтернативный путь, если это запись конференции
		// Проверяем, есть ли запись с таким UUID в базе записей
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "NoSuchKey") {
			log.Printf("[FileService] Файл не найден по стандартному пути, проверяем альтернативные пути")

			// Пробуем путь для записей LiveKit
			alternativePath := fmt.Sprintf("recordings/personal_recordings/%s/%s", file.OwnerID, file.Name)
			log.Printf("[FileService] Пробуем альтернативный путь для записи: %s", alternativePath)

			data, err = s.s3Client.GetObjectRange(ctx, alternativePath, start, end)
			if err != nil {
				log.Printf("[FileService] Файл не найден и по альтернативному пути: %v", err)
				return nil, fmt.Errorf("ошибка получения данных из S3: %w", err)
			}

			log.Printf("[FileService] Файл найден по альтернативному пути!")

			// Обновляем метаданные файла, чтобы в будущем использовать правильный путь
			if file.Metadata == nil {
				file.Metadata = map[string]interface{}{
					"s3Path": alternativePath,
				}
			} else {
				// Напрямую добавляем/обновляем s3Path в метаданных
				file.Metadata["s3Path"] = alternativePath
			}

			// Сохраняем обновленные метаданные в БД
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.fileRepo.Update(ctx, file); err != nil {
					log.Printf("[FileService] Ошибка обновления метаданных: %v", err)
				}
			}()
		} else {
			return nil, fmt.Errorf("ошибка получения данных из S3: %w", err)
		}
	}

	// Создаем буферизированный reader для оптимизации производительности
	bufferedReader := bufio.NewReaderSize(data, 32*1024) // 32KB буфер

	// Оборачиваем в ReadCloser для корректного закрытия
	result := &struct {
		*bufio.Reader
		io.Closer
	}{
		Reader: bufferedReader,
		Closer: data,
	}

	log.Printf("[FileService] Диапазон успешно получен. Размер: %d байт. Время: %v",
		end-start+1, time.Since(startTime))

	return result, nil
}

const (
	defaultChunkSize = 5 * 1024 * 1024 // 5MB размер чанка
	maxWorkers       = 5               // Максимальное количество параллельных загрузок
	maxRetries       = 3               // Максимальное количество попыток загрузки чанка
)

type ChunkInfo struct {
	Start    int64
	End      int64
	Data     []byte
	Complete bool
	Error    error
}

// DownloadFileParallel реализует параллельное скачивание файла
func (s *FileService) DownloadFileParallel(ctx context.Context, fileUUID uuid.UUID, userID string) (io.ReadCloser, error) {
	// Получаем информацию о файле
	file, err := s.GetFileInfo(ctx, fileUUID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// Создаем пайп для потоковой передачи данных
	pr, pw := io.Pipe()

	// Вычисляем количество чанков
	totalSize := file.SizeBytes
	numChunks := (totalSize + defaultChunkSize - 1) / defaultChunkSize

	// Создаем каналы для управления загрузкой
	chunks := make(chan ChunkInfo, numChunks)
	results := make(chan ChunkInfo, numChunks)
	errors := make(chan error, 1)

	// Запускаем горутину для записи данных в пайп
	go func() {
		defer pw.Close()

		// Создаем буфер для сборки файла
		assembledData := make(map[int64][]byte)
		var currentOffset int64 = 0

		for i := int64(0); i < numChunks; i++ {
			// Вычисляем границы чанка
			start := i * defaultChunkSize
			end := start + defaultChunkSize
			if end > totalSize {
				end = totalSize
			}

			// Добавляем информацию о чанке в канал
			chunks <- ChunkInfo{
				Start: start,
				End:   end - 1,
			}
		}
		close(chunks)

		// Обрабатываем результаты загрузки чанков
		for i := int64(0); i < numChunks; i++ {
			result := <-results
			if result.Error != nil {
				errors <- result.Error
				return
			}

			// Сохраняем данные чанка
			assembledData[result.Start] = result.Data

			// Пишем данные в правильном порядке
			for {
				if data, ok := assembledData[currentOffset]; ok {
					_, err := pw.Write(data)
					if err != nil {
						errors <- err
						return
					}
					delete(assembledData, currentOffset)
					currentOffset += int64(len(data))
				} else {
					break
				}
			}
		}
	}()

	// Запускаем воркеры для параллельной загрузки чанков
	var wg sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chunk := range chunks {
				// Создаем контекст с таймаутом для каждого чанка
				chunkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()

				// Пытаемся загрузить чанк с повторными попытками
				var data io.ReadCloser
				var err error
				for retry := 0; retry < maxRetries; retry++ {
					data, err = s.GetFileDataRange(chunkCtx, fileUUID, userID, chunk.Start, chunk.End)
					if err == nil {
						break
					}
					time.Sleep(time.Second * time.Duration(retry+1))
				}

				if err != nil {
					results <- ChunkInfo{Error: err}
					return
				}

				// Читаем данные чанка
				buffer := &bytes.Buffer{}
				_, err = io.Copy(buffer, data)
				data.Close()

				if err != nil {
					results <- ChunkInfo{Error: err}
					return
				}

				// Отправляем результат
				results <- ChunkInfo{
					Start: chunk.Start,
					End:   chunk.End,
					Data:  buffer.Bytes(),
				}
			}
		}()
	}

	// Запускаем горутину для ожидания завершения всех воркеров
	go func() {
		wg.Wait()
		close(results)
	}()

	// Возвращаем ReadCloser для потоковой передачи данных
	return pr, nil
}

// RenameFile переименовывает файл
func (s *FileService) RenameFile(ctx context.Context, fileUUID uuid.UUID, newName string, userID string) error {
	// Получаем информацию о файле и проверяем права
	file, err := s.GetFileInfo(ctx, fileUUID, userID)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	// Проверяем права на редактирование
	if file.OwnerID != userID {
		// Проверяем права в родительской папке для shared доступа
		hasPermission, err := s.permissionService.CheckSharedFolderPermission(
			ctx,
			userID,
			file.FolderID,
			OperationEdit,
		)
		if err != nil {
			return fmt.Errorf("failed to check permissions: %w", err)
		}
		if !hasPermission {
			// Дополнительно проверяем прямые права на файл через shares
			hasDirectPermission, err := s.permissionService.CheckPermission(
				ctx,
				userID,
				fileUUID.String(),
				domain.ResourceTypeFile,
				OperationEdit,
			)
			if err != nil {
				return fmt.Errorf("failed to check direct permissions: %w", err)
			}
			if !hasDirectPermission {
				return fmt.Errorf("access denied")
			}
		}
	}

	// Проверяем, нет ли файла с таким именем в той же папке
	existingFile, err := s.fileRepo.CheckFileExists(ctx, file.FolderID, newName)
	if err != nil {
		return fmt.Errorf("failed to check file existence: %w", err)
	}
	if existingFile != nil && existingFile.UUID != fileUUID {
		return fmt.Errorf("file with name %s already exists in this folder", newName)
	}

	// Обновляем имя файла
	if err := s.fileRepo.UpdateFileName(ctx, fileUUID, newName); err != nil {
		return fmt.Errorf("failed to update file name: %w", err)
	}

	return nil
}

// MoveFile перемещает файл в другую папку
func (s *FileService) MoveFile(ctx context.Context, fileUUID uuid.UUID, newFolderID int64, userID string) error {
	// Получаем информацию о файле
	file, err := s.GetFileInfo(ctx, fileUUID, userID)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	// Проверяем права на перемещение в исходной папке
	if file.OwnerID != userID {
		// Проверяем права через shares
		hasDirectPermission, err := s.permissionService.CheckPermission(
			ctx,
			userID,
			fileUUID.String(),
			domain.ResourceTypeFile,
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
				file.FolderID,
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

	// Проверяем права на перемещение в целевой папке
	hasTargetPermission, err := s.permissionService.CheckSharedFolderPermission(
		ctx,
		userID,
		newFolderID,
		OperationUpload,
	)
	if err != nil {
		return fmt.Errorf("failed to check target folder permissions: %w", err)
	}
	if !hasTargetPermission && file.OwnerID != userID {
		return fmt.Errorf("access denied for target folder")
	}

	// Проверяем, нет ли файла с таким именем в целевой папке
	existingFile, err := s.fileRepo.CheckFileExists(ctx, newFolderID, file.Name)
	if err != nil {
		return fmt.Errorf("failed to check file existence: %w", err)
	}
	if existingFile != nil {
		return fmt.Errorf("file with name %s already exists in target folder", file.Name)
	}

	// Начинаем транзакцию
	tx, err := s.fileRepo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Обновляем папку файла
	if err := s.fileRepo.UpdateFileFolder(ctx, tx, fileUUID, newFolderID, file.FolderID); err != nil {
		return fmt.Errorf("failed to move file: %w", err)
	}

	return tx.Commit()
}
