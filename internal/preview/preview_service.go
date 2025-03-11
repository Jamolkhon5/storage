package preview

import (
	"bytes"
	"context"
	"fmt"
	"github.com/h2non/bimg"
	"github.com/jmoiron/sqlx"
	"io"
	"log"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"synxrondrive/internal/service/s3"
	"time"
)

func init() {
	// В начале preview_service.go
	dirs := []string{
		"/tmp/previews",
		"/tmp/.config",
		"/tmp/.cache",
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0777); err != nil {
			log.Printf("Warning: failed to create directory %s: %v", dir, err)
		}
		if err := os.Chmod(dir, 0777); err != nil {
			log.Printf("Warning: failed to chmod directory %s: %v", dir, err)
		}
	}
}

const (
	maxImageSize  = 1024            // максимальный размер превью в пикселях
	jpegQuality   = 85              // качество JPEG
	previewPrefix = "previews/"     // префикс для превью в S3
	tmpDir        = "/tmp/previews" // директория для временных файлов
)

type Service struct {
	s3Client s3.Storage
	db       *sqlx.DB
}

// NewService создает новый сервис для работы с превью
func NewService(s3Client s3.Storage, db *sqlx.DB) *Service {
	service := &Service{
		s3Client: s3Client,
		db:       db,
	}

	return service
}

// StartCleanupTask запускает периодическую очистку старых превью
func (s *Service) StartCleanupTask() {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		for range ticker.C {
			ctx := context.Background()
			// Удаляем превью старше 30 дней
			s.cleanupOldPreviews(ctx)
		}
	}()
}

// cleanupOldPreviews удаляет старые превью из S3 и базы данных
func (s *Service) cleanupOldPreviews(ctx context.Context) {
	log.Printf("Starting preview cleanup task")

	// Получаем список старых превью для удаления
	var previewsToDelete []struct {
		FileUUID string `db:"file_uuid"`
		OwnerID  string `db:"owner_id"`
	}

	query := `
        DELETE FROM file_previews fp
        USING files f
        WHERE fp.file_uuid = f.uuid
        AND fp.created_at < NOW() - INTERVAL '30 days'
        RETURNING fp.file_uuid, f.owner_id
    `

	err := s.db.SelectContext(ctx, &previewsToDelete, query)
	if err != nil {
		log.Printf("Error cleaning up old previews from database: %v", err)
		return
	}

	// Удаляем превью из S3
	for _, preview := range previewsToDelete {
		s3Key := fmt.Sprintf("personal_drive_files/%s/%s%s", preview.OwnerID, previewPrefix, preview.FileUUID)
		if err := s.s3Client.DeleteObject(s3Key); err != nil {
			log.Printf("Error deleting preview from S3: %v", err)
		}
	}

	log.Printf("Completed preview cleanup task. Removed %d old previews", len(previewsToDelete))
}

// GetOrGeneratePreview получает или генерирует превью файла
func (s *Service) GetOrGeneratePreview(ctx context.Context, fileUUID string, fileType string, data io.Reader) ([]byte, error) {
	log.Printf("[Preview] Запрос превью для файла: %s (тип: %s)", fileUUID, fileType)

	// Получаем информацию о файле
	var ownerID string
	var fileName string
	var version int

	err := s.db.QueryRowContext(ctx,
		"SELECT owner_id, name, current_version FROM files WHERE uuid = $1",
		fileUUID).Scan(&ownerID, &fileName, &version)
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// Формируем ключ для превью в S3 с учетом версии и owner_id
	previewKey := fmt.Sprintf("personal_drive_files/%s/%s%s_v%d", ownerID, previewPrefix, fileUUID, version)

	// Пытаемся получить существующее превью
	preview, err := s.s3Client.GetObject(ctx, previewKey)
	if err == nil {
		log.Printf("[Preview] Найдено существующее превью: %s", previewKey)
		defer preview.Close()
		return io.ReadAll(preview)
	}

	log.Printf("[Preview] Превью не найдено, генерируем новое")

	// Пробуем определить альтернативный путь для записей видеоконференций
	if fileType == "video/mp4" {
		// Пробуем путь для записей LiveKit
		alternativePath := fmt.Sprintf("recordings/personal_recordings/%s/%s", ownerID, fileName)
		log.Printf("[Preview] Пробуем альтернативный путь для записи: %s", alternativePath)

		// Получаем данные по альтернативному пути
		alternativeData, err := s.s3Client.GetObject(ctx, alternativePath)
		if err == nil {
			log.Printf("[Preview] Успешно получены данные по альтернативному пути")
			defer alternativeData.Close()
			fileData, err := io.ReadAll(alternativeData)
			if err != nil {
				return nil, fmt.Errorf("failed to read file data: %w", err)
			}

			// Генерируем превью из полученных данных
			previewData, err := s.generateVideoPreview(bytes.NewReader(fileData))
			if err != nil {
				log.Printf("[Preview] Ошибка генерации превью: %v", err)
				return nil, fmt.Errorf("failed to generate preview: %w", err)
			}

			// Сохраняем превью в S3
			err = s.savePreviewToS3(ctx, previewKey, previewData)
			if err != nil {
				log.Printf("[Preview] Предупреждение: не удалось сохранить превью в S3: %v", err)
			} else {
				log.Printf("[Preview] Превью успешно сохранено в S3: %s", previewKey)
			}

			return previewData, nil
		}
		log.Printf("[Preview] Не удалось получить данные по альтернативному пути: %v", err)
	}

	// Стандартная логика, если не найдены специальные пути
	fileData, err := io.ReadAll(data)
	if err != nil {
		return nil, fmt.Errorf("failed to read file data: %w", err)
	}

	log.Printf("[Preview] Генерация превью стандартным способом, размер данных: %d байт", len(fileData))

	// Генерируем превью в зависимости от типа файла
	var previewData []byte
	switch fileType {
	case "application/pdf":
		previewData, err = s.generatePDFPreview(fileData)
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		previewData, err = s.generateDocxPreview(fileData)
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		previewData, err = s.generateXlsxPreview(fileData)
	case "image/jpeg", "image/png", "image/gif":
		previewData, err = s.generateImagePreview(fileData)
	case "video/mp4", "video/webm", "video/x-matroska":
		// Создаем io.Reader из []byte
		previewData, err = s.generateVideoPreview(bytes.NewReader(fileData))
	default:
		return nil, fmt.Errorf("unsupported file type: %s", fileType)
	}

	if err != nil {
		log.Printf("[Preview] Ошибка генерации превью: %v", err)
		return nil, fmt.Errorf("failed to generate preview: %w", err)
	}

	// Сохраняем превью в S3
	err = s.savePreviewToS3(ctx, previewKey, previewData)
	if err != nil {
		log.Printf("[Preview] Предупреждение: не удалось сохранить превью в S3: %v", err)
	} else {
		log.Printf("[Preview] Превью успешно сохранено в S3: %s", previewKey)
	}

	return previewData, nil
}

// generatePDFPreview генерирует превью для PDF файла
func (s *Service) generatePDFPreview(data []byte) ([]byte, error) {
	// Создаем временную директорию
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("preview_%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tmpPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpPath)

	// Сохраняем PDF во временный файл
	pdfPath := filepath.Join(tmpPath, "input.pdf")
	if err := os.WriteFile(pdfPath, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write PDF file: %w", err)
	}

	// Используем pdftoppm для конвертации первой страницы в изображение
	outputPath := filepath.Join(tmpPath, "output")
	cmd := exec.Command("pdftoppm",
		"-jpeg",
		"-f", "1",
		"-l", "1",
		"-scale-to", fmt.Sprintf("%d", maxImageSize),
		"-singlefile",
		pdfPath,
		outputPath,
	)

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to convert PDF: %w", err)
	}

	// Читаем получившееся изображение
	jpegPath := outputPath + ".jpg"
	imgData, err := os.ReadFile(jpegPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read converted image: %w", err)
	}

	return s.optimizeImage(imgData)
}

// generateDocxPreview генерирует превью для DOCX файла
func (s *Service) generateDocxPreview(data []byte) ([]byte, error) {
	log.Printf("Starting DOCX preview generation")
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("preview_%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tmpPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpPath)

	// Сохраняем DOCX во временный файл
	docxPath := filepath.Join(tmpPath, "input.docx")
	if err := os.WriteFile(docxPath, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write DOCX file: %w", err)
	}
	log.Printf("Saved DOCX to temporary file: %s", docxPath)

	// Проверяем, что файл существует
	if _, err := os.Stat(docxPath); err != nil {
		return nil, fmt.Errorf("temp file not found: %w", err)
	}

	// Проверяем права доступа
	log.Printf("Checking LibreOffice installation...")
	libreOfficePath, err := exec.LookPath("soffice")
	if err != nil {
		return nil, fmt.Errorf("libreoffice not found: %w", err)
	}
	log.Printf("Found LibreOffice at: %s", libreOfficePath)

	// Конвертируем в PDF используя LibreOffice
	cmd := exec.Command("soffice",
		"--headless",
		"--convert-to", "pdf:writer_pdf_Export",
		"--outdir", tmpPath,
		docxPath,
	)

	// Устанавливаем переменные окружения для LibreOffice
	cmd.Env = append(os.Environ(),
		"HOME=/tmp",
		"TMPDIR=/tmp",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	)

	// Перехватываем вывод команды
	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("LibreOffice conversion failed. Output: %s", string(cmdOutput))
		return nil, fmt.Errorf("failed to convert DOCX to PDF: %w (output: %s)", err, string(cmdOutput))
	}
	log.Printf("LibreOffice conversion output: %s", string(cmdOutput))

	// Проверяем создание PDF
	pdfPath := filepath.Join(tmpPath, "input.pdf")
	if _, err := os.Stat(pdfPath); err != nil {
		log.Printf("Expected PDF file not found at: %s", pdfPath)
		// Смотрим содержимое директории
		files, _ := os.ReadDir(tmpPath)
		for _, file := range files {
			log.Printf("Found file in temp dir: %s", file.Name())
		}
		return nil, fmt.Errorf("PDF file not created: %w", err)
	}
	log.Printf("PDF file created successfully at: %s", pdfPath)

	// Читаем получившийся PDF
	pdfData, err := os.ReadFile(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read converted PDF: %w", err)
	}
	log.Printf("Successfully read PDF data, size: %d bytes", len(pdfData))

	// Генерируем превью из PDF
	return s.generatePDFPreview(pdfData)
}

// generateXlsxPreview генерирует превью для XLSX файла
func (s *Service) generateXlsxPreview(data []byte) ([]byte, error) {
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("preview_%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tmpPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpPath)

	// Сохраняем XLSX во временный файл
	xlsxPath := filepath.Join(tmpPath, "input.xlsx")
	if err := os.WriteFile(xlsxPath, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write XLSX file: %w", err)
	}

	// Конвертируем в PDF используя LibreOffice
	cmd := exec.Command("soffice",
		"--headless",
		"--convert-to", "pdf:calc_pdf_Export",
		"--outdir", tmpPath,
		xlsxPath,
	)

	// Устанавливаем переменные окружения для LibreOffice
	cmd.Env = append(os.Environ(),
		"HOME=/tmp",
		"TMPDIR=/tmp",
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("failed to convert XLSX to PDF: %w (output: %s)", err, string(out))
	}

	// Читаем получившийся PDF
	pdfPath := filepath.Join(tmpPath, "input.pdf")
	pdfData, err := os.ReadFile(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read converted PDF: %w", err)
	}

	// Генерируем превью из PDF
	return s.generatePDFPreview(pdfData)
}

// generateImagePreview генерирует превью для изображений
func (s *Service) generateImagePreview(data []byte) ([]byte, error) {
	return s.optimizeImage(data)
}

// optimizeImage оптимизирует изображение до нужного размера
func (s *Service) optimizeImage(data []byte) ([]byte, error) {
	// Используем bimg для оптимизации
	image := bimg.NewImage(data)

	// Получаем текущие размеры
	size, err := image.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get image size: %w", err)
	}

	// Вычисляем новые размеры с сохранением пропорций
	width, height := calculateNewDimensions(size.Width, size.Height, maxImageSize)

	// Создаем превью
	processed, err := image.Process(bimg.Options{
		Width:   width,
		Height:  height,
		Quality: jpegQuality,
		Type:    bimg.JPEG,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to process image: %w", err)
	}

	return processed, nil
}

// calculateNewDimensions вычисляет новые размеры с сохранением пропорций
func calculateNewDimensions(width, height, maxSize int) (newWidth, newHeight int) {
	if width > height {
		newWidth = maxSize
		newHeight = (height * maxSize) / width
	} else {
		newHeight = maxSize
		newWidth = (width * maxSize) / height
	}
	return
}

// savePreviewToS3 сохраняет превью в S3
func (s *Service) savePreviewToS3(ctx context.Context, key string, data []byte) error {
	// Создаем временный файл в памяти
	file, err := os.CreateTemp("", "preview_*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(file.Name())

	// Записываем данные во временный файл
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("failed to write data to temp file: %w", err)
	}

	// Перемещаем указатель в начало файла
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek file: %w", err)
	}

	// Конвертируем в multipart.File
	mpFile := multipart.File(file)

	// Загружаем файл в S3
	return s.s3Client.UploadFile(key, &mpFile)
}

const (
	maxPreviewSize    = 5 * 1024 * 1024  // 5MB максимальный размер превью
	previewTimeOffset = "10%"            // Позиция кадра для превью (10% от начала)
	ffmpegTimeout     = 30 * time.Second // Таймаут для ffmpeg
)

func (s *Service) generateVideoPreview(data io.Reader) ([]byte, error) {
	// Создаем временную директорию
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("preview_%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tmpPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpPath)

	// Сохраняем видео во временный файл
	videoPath := filepath.Join(tmpPath, "input.mp4")
	videoFile, err := os.Create(videoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	// Копируем данные во временный файл
	if _, err := io.Copy(videoFile, data); err != nil {
		videoFile.Close()
		return nil, fmt.Errorf("failed to save video data: %w", err)
	}
	videoFile.Close()

	// Получаем длительность видео
	duration, err := getVideoDuration(videoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get video duration: %w", err)
	}

	// Вычисляем оптимальное время для кадра
	previewTime := calculatePreviewTime(duration)
	outputPath := filepath.Join(tmpPath, "output.jpg")

	// Создаем контекст с таймаутом
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Формируем команду ffmpeg
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-ss", previewTime, // Позиция для кадра
		"-i", videoPath, // Входной файл
		"-vf", fmt.Sprintf("scale=%d:-1:force_original_aspect_ratio=decrease", maxImageSize),
		"-frames:v", "1", // Один кадр
		"-q:v", "2", // Качество JPEG
		"-f", "image2", // Формат - изображение
		"-y", // Перезаписать если существует
		outputPath,
	)

	// Буфер для ошибок
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to extract frame: %w (stderr: %s)", err, stderr.String())
	}

	// Читаем и оптимизируем изображение
	imgData, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read frame image: %w", err)
	}

	return s.optimizeImage(imgData)
}

// getVideoDuration получает длительность видео
func getVideoDuration(videoPath string) (string, error) {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get duration: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// calculatePreviewTime вычисляет время для кадра превью
func calculatePreviewTime(duration string) string {
	durationFloat, err := strconv.ParseFloat(duration, 64)
	if err != nil {
		return "00:00:01" // По умолчанию 1 секунда
	}

	if durationFloat <= 10 {
		return "00:00:01"
	}

	// Берем кадр на 10% от начала видео
	previewSeconds := durationFloat * 0.1
	hours := int(previewSeconds) / 3600
	minutes := (int(previewSeconds) % 3600) / 60
	seconds := int(previewSeconds) % 60

	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}
