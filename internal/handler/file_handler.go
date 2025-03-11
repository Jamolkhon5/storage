package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"synxrondrive/internal/auth"
	"synxrondrive/internal/domain"
	"synxrondrive/internal/service"
	"time"
)

// UploadProgress хранит информацию о прогрессе загрузки
type uploadProgress struct {
	FileName string
	Progress float64
	Status   string
	Error    string
	Version  int
}

// progressMap для хранения прогресса загрузки
var (
	progressMap = make(map[string]*uploadProgress)
	progressMu  sync.RWMutex
)

type FileExistsResponse struct {
	Exists bool         `json:"exists"`
	File   *domain.File `json:"file,omitempty"`
}

// UploadProgressEvent представляет событие прогресса загрузки
type UploadProgressEvent struct {
	FileName   string  `json:"fileName"`
	Percentage float64 `json:"percentage"`
	Status     string  `json:"status"`
	Error      string  `json:"error,omitempty"`
	Version    int     `json:"version,omitempty"`
}

// UploadResult представляет результат загрузки файла
type UploadResult struct {
	File         *domain.File `json:"file,omitempty"`
	Error        string       `json:"error,omitempty"`
	IsNewVersion bool         `json:"isNewVersion,omitempty"`
	Version      int          `json:"version,omitempty"`
}

// MultiUploadResponse представляет ответ на множественную загрузку
type MultiUploadResponse struct {
	Results []UploadResult `json:"results"`
}

type FileHandler struct {
	fileService   *service.FileService
	folderService *service.FolderService
	trashService  *service.TrashService
	videoService  *service.VideoService
}

type fileWrapper struct {
	*bytes.Reader
}

type UploadResponse struct {
	Status   string    `json:"status"`
	FileUUID uuid.UUID `json:"file_uuid,omitempty"`
	FileName string    `json:"file_name,omitempty"`
	Message  string    `json:"message"`
	Version  int       `json:"version,omitempty"`
}

func NewFileHandler(
	fileService *service.FileService,
	folderService *service.FolderService,
	trashService *service.TrashService,
	videoService *service.VideoService,
) *FileHandler {
	return &FileHandler{
		fileService:   fileService,
		folderService: folderService,
		trashService:  trashService,
		videoService:  videoService,
	}
}

func (f *fileWrapper) Close() error {
	return nil
}

type progressReader struct {
	reader   io.ReadSeeker // Изменяем тип на ReadSeeker для поддержки Seek
	size     int64
	reporter func(n int64)
	read     int64
}

func (pr *progressReader) Read(p []byte) (n int, err error) {
	n, err = pr.reader.Read(p)
	if n > 0 {
		pr.read += int64(n)
		pr.reporter(int64(n))
	}
	return
}

func (pr *progressReader) Seek(offset int64, whence int) (int64, error) {
	return pr.reader.Seek(offset, whence)
}

func (pr *progressReader) Close() error {
	if closer, ok := pr.reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (pr *progressReader) ReadAt(p []byte, off int64) (n int, err error) {
	if seeker, ok := pr.reader.(io.ReaderAt); ok {
		n, err = seeker.ReadAt(p, off)
		if n > 0 {
			pr.reporter(int64(n))
		}
		return
	}
	return 0, fmt.Errorf("ReadAt not supported")
}

func newProgressReader(reader io.ReadSeeker, size int64, reporter func(n int64)) *progressReader {
	return &progressReader{
		reader:   reader,
		size:     size,
		reporter: reporter,
		read:     0,
	}
}

// UploadFile обрабатывает загрузку файла
func (h *FileHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	folderIDStr := r.FormValue("folder_id")
	var folderID int64
	if folderIDStr != "" {
		folderID, err = strconv.ParseInt(folderIDStr, 10, 64)
		if err != nil {
			http.Error(w, "Invalid folder ID", http.StatusBadRequest)
			return
		}
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(w, "No files uploaded", http.StatusBadRequest)
		return
	}

	results := make([]UploadResult, len(files))
	for i, fileHeader := range files {
		progressID := fmt.Sprintf("%s_%s", userID, fileHeader.Filename)

		// Начало загрузки - 0%
		setProgress(progressID, 0, "uploading", "", 0)

		file, err := fileHeader.Open()
		if err != nil {
			setProgress(progressID, 0, "error", err.Error(), 0)
			results[i] = UploadResult{Error: err.Error()}
			continue
		}
		defer file.Close()

		// Проверка существования файла - 30%
		setProgress(progressID, 30, "uploading", "", 0)
		existingFile, err := h.fileService.CheckFileExists(r.Context(), folderID, fileHeader.Filename)
		if err != nil {
			setProgress(progressID, 0, "error", err.Error(), 0)
			results[i] = UploadResult{Error: err.Error()}
			continue
		}

		if existingFile != nil {
			// Загрузка новой версии - 60%
			setProgress(progressID, 60, "uploading", "", existingFile.CurrentVersion)
			err = h.fileService.UploadFileVersion(r.Context(), file, fileHeader, existingFile, userID)
			if err != nil {
				setProgress(progressID, 0, "error", err.Error(), 0)
				results[i] = UploadResult{Error: err.Error()}
				continue
			}

			// Успешное завершение загрузки новой версии - 100%
			results[i] = UploadResult{
				File:         existingFile,
				IsNewVersion: true,
				Version:      existingFile.CurrentVersion + 1,
			}
			setProgress(progressID, 100, "completed", "", existingFile.CurrentVersion+1)
		} else {
			// Загрузка нового файла - 60%
			setProgress(progressID, 60, "uploading", "", 0)
			uploadedFile, err := h.fileService.UploadFile(r.Context(), fileHeader, file, folderID, userID)
			if err != nil {
				setProgress(progressID, 0, "error", err.Error(), 0)
				results[i] = UploadResult{Error: err.Error()}
				continue
			}

			// Успешное завершение загрузки нового файла - 100%
			results[i] = UploadResult{
				File:    uploadedFile,
				Version: 1,
			}
			setProgress(progressID, 100, "completed", fmt.Sprintf("Файл %s успешно загружен", fileHeader.Filename), 1)
		}
	}

	// Отправляем финальный ответ
	response := MultiUploadResponse{
		Results: results,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetUploadProgress отдает SSE события о прогрессе загрузки
func (h *FileHandler) GetUploadProgress(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "No token provided", http.StatusUnauthorized)
		return
	}

	r.Header.Set("Authorization", "Bearer "+token)
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ctx := r.Context()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			progressMu.RLock()
			hasUpdates := false
			for id, progress := range progressMap {
				if strings.HasPrefix(id, userID+"_") {
					hasUpdates = true
					event := UploadProgressEvent{
						FileName:   progress.FileName,
						Percentage: progress.Progress,
						Status:     progress.Status,
						Error:      progress.Error,
						Version:    progress.Version,
					}

					data, err := json.Marshal(event)
					if err != nil {
						continue
					}

					fmt.Fprintf(w, "data: %s\n\n", data)
				}
			}
			progressMu.RUnlock()

			if hasUpdates {
				flusher.Flush()
			}
		}
	}
}

// Вспомогательная функция для отправки SSE событий
func sendSSEEvent(w http.ResponseWriter, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("error marshaling SSE data: %w", err)
	}

	if _, err := fmt.Fprintf(w, "data: %s\n\n", jsonData); err != nil {
		return fmt.Errorf("error writing SSE data: %w", err)
	}

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	return nil
}

// Вспомогательная функция для отправки ошибок через SSE
func sendSSEError(w http.ResponseWriter, message string, err error) error {
	event := UploadProgressEvent{
		Status: "error",
		Error:  message,
	}
	if err != nil {
		event.Error = fmt.Sprintf("%s: %v", message, err)
	}
	return sendSSEEvent(w, event)
}
func setProgress(id string, progress float64, status, error string, version int) {
	progressMu.Lock()
	if p, ok := progressMap[id]; ok {
		p.Progress = progress
		p.Status = status
		p.Error = error
		p.Version = version
	}
	progressMu.Unlock()
}

// DownloadFile обрабатывает скачивание файла с поддержкой стриминга
func (h *FileHandler) DownloadFile(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	log.Printf("[Download] Начало запроса на скачивание")

	// CORS заголовки
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Origin, Authorization, Range, Content-Type")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Accept-Ranges, Content-Length, Content-Disposition")

	// Предварительный запрос OPTIONS
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("[Download] Ошибка авторизации: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем UUID файла
	fileUUID, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		log.Printf("[Download] Некорректный UUID: %v", err)
		http.Error(w, "Некорректный UUID файла", http.StatusBadRequest)
		return
	}

	// Получаем информацию о файле
	file, err := h.fileService.GetFileInfo(r.Context(), fileUUID, userID)
	if err != nil {
		if strings.Contains(err.Error(), "access denied") {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
		log.Printf("[Download] Ошибка получения информации о файле: %v", err)
		http.Error(w, "Ошибка получения информации о файле", http.StatusInternalServerError)
		return
	}

	log.Printf("[Download] Информация о файле: ID=%s, Name=%s, Size=%d, MimeType=%s",
		fileUUID, file.Name, file.SizeBytes, file.MIMEType)

	// Логируем метаданные, если они есть
	if file.Metadata != nil {
		log.Printf("[Download] Метаданные файла: %+v", file.Metadata)
	}

	// Подготавливаем имя файла для Content-Disposition
	encodedFileName := url.QueryEscape(file.Name)
	asciiName := strings.ReplaceAll(file.Name, `"`, `\"`)
	contentDisposition := fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, asciiName, encodedFileName)

	// Устанавливаем базовые заголовки
	w.Header().Set("Content-Type", file.MIMEType)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Disposition", contentDisposition)
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	// Получаем размер файла
	w.Header().Set("Content-Length", strconv.FormatInt(file.SizeBytes, 10))

	// Обработка Range запроса
	var start, end int64
	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" {
		log.Printf("[Download] Получен Range запрос: %s", rangeHeader)
		ranges, err := parseRange(rangeHeader, file.SizeBytes)
		if err != nil {
			log.Printf("[Download] Ошибка парсинга Range: %v", err)
			http.Error(w, err.Error(), http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if len(ranges) != 1 {
			log.Printf("[Download] Несколько диапазонов не поддерживаются")
			http.Error(w, "Multiple ranges not supported", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		start = ranges[0][0]
		end = ranges[0][1]

		// Устанавливаем заголовок Content-Range
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.SizeBytes))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		log.Printf("[Download] Отдаем частичный контент: %d-%d/%d", start, end, file.SizeBytes)
		w.WriteHeader(http.StatusPartialContent)
	} else {
		start = 0
		end = file.SizeBytes - 1
		log.Printf("[Download] Отдаем полный файл: %d байт", file.SizeBytes)
		w.WriteHeader(http.StatusOK)
	}

	// Получаем данные из S3 с использованием Range
	reader, err := h.fileService.GetFileDataRange(r.Context(), fileUUID, userID, start, end)
	if err != nil {
		log.Printf("[Download] Ошибка получения данных файла: %v", err)
		http.Error(w, "Failed to get file data", http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	// Настраиваем буфер для оптимальной производительности
	buf := make([]byte, 32*1024) // 32KB буфер

	// Отправляем данные клиенту через буфер
	var written int64
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			nw, ew := w.Write(buf[:n])
			if nw > 0 {
				written += int64(nw)

				// Периодически сообщаем о прогрессе для больших файлов
				if written%(1024*1024) == 0 { // Каждый мегабайт
					log.Printf("[Download] Отправлено %d MB / %d MB", written/(1024*1024), file.SizeBytes/(1024*1024))
				}
			}
			if ew != nil {
				log.Printf("[Download] Ошибка записи: %v", ew)
				err = ew
				break
			}
			if nw != n {
				log.Printf("[Download] Короткая запись: %d < %d", nw, n)
				err = io.ErrShortWrite
				break
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("[Download] Ошибка при чтении файла: %v", err)
			return
		}

		// Сбрасываем буфер после каждого чанка
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	duration := time.Since(startTime)
	speed := float64(written) / duration.Seconds() / 1024 / 1024 // MB/s
	log.Printf("[Download] Завершено. Отправлено %d байт за %v (%.2f MB/s)", written, duration, speed)
}

// Вспомогательная функция для парсинга Range заголовка
func parseRange(rangeHeader string, fileSize int64) ([][2]int64, error) {
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		return nil, fmt.Errorf("invalid range format")
	}

	rangeHeader = strings.TrimPrefix(rangeHeader, "bytes=")
	var ranges [][2]int64

	for _, r := range strings.Split(rangeHeader, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}

		parts := strings.Split(r, "-")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid range format")
		}

		var start, end int64
		var err error

		if parts[0] == "" {
			// Suffix range: -N
			end, err = strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return nil, err
			}
			start = fileSize - end
			end = fileSize - 1
		} else {
			// Standard range: N-M
			start, err = strconv.ParseInt(parts[0], 10, 64)
			if err != nil {
				return nil, err
			}

			if parts[1] == "" {
				// Range: N-
				end = fileSize - 1
			} else {
				end, err = strconv.ParseInt(parts[1], 10, 64)
				if err != nil {
					return nil, err
				}
			}
		}

		if start < 0 || end < 0 || start > end || end >= fileSize {
			return nil, fmt.Errorf("invalid range values")
		}

		ranges = append(ranges, [2]int64{start, end})
	}

	return ranges, nil
}

// DeleteFile теперь перемещает файл в корзину вместо непосредственного удаления
func (h *FileHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("Authorization failed: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем UUID файла
	fileUUID := chi.URLParam(r, "uuid")
	if fileUUID == "" {
		log.Printf("Missing file UUID")
		http.Error(w, "Missing file UUID", http.StatusBadRequest)
		return
	}

	// Перемещаем файл в корзину вместо удаления
	if err := h.trashService.MoveToTrash(r.Context(), fileUUID, "file", userID); err != nil {
		log.Printf("Failed to move file to trash: %v", err)
		http.Error(w, "Failed to delete file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// Вспомогательная функция для проверки ASCII символов
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// GetFileVersions возвращает все версии файла
func (h *FileHandler) GetFileVersions(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	fileUUID := chi.URLParam(r, "uuid")
	if fileUUID == "" {
		http.Error(w, "Missing file UUID", http.StatusBadRequest)
		return
	}

	parsedUUID, err := uuid.Parse(fileUUID)
	if err != nil {
		http.Error(w, "Invalid UUID format", http.StatusBadRequest)
		return
	}

	// Проверяем доступ к файлу и используем полученный результат
	file, err := h.fileService.GetFileInfo(r.Context(), parsedUUID, userID)
	if err != nil {
		if err.Error() == "access denied" {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
		http.Error(w, "Failed to verify file access", http.StatusInternalServerError)
		return
	}

	// Проверяем, что файл принадлежит пользователю
	if file.OwnerID != userID {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	versions, err := h.fileService.GetFileVersions(r.Context(), parsedUUID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get file versions: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(versions)
}

func (h *FileHandler) CheckFileExists(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	fileName := r.URL.Query().Get("name")
	folderID, err := strconv.ParseInt(r.URL.Query().Get("folder_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid folder ID", http.StatusBadRequest)
		return
	}

	// Проверяем доступ к папке - этой проверки достаточно, так как GetFolderContent
	// уже включает в себя проверку shared доступа
	_, err = h.folderService.GetFolderContent(r.Context(), folderID, userID)
	if err != nil {
		if strings.Contains(err.Error(), "access denied") {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
		http.Error(w, "Failed to verify folder access", http.StatusInternalServerError)
		return
	}

	file, err := h.fileService.CheckFileExists(r.Context(), folderID, fileName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := FileExistsResponse{
		Exists: file != nil,
		File:   file,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// UploadContextFile загружает файл для определенного контекста (чат, группа и т.д.)
func (h *FileHandler) UploadContextFile(w http.ResponseWriter, r *http.Request) {
	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Проверяем размер файла и парсим форму
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	// Получаем тип контекста
	contextType := r.FormValue("context_type")
	if contextType == "" {
		http.Error(w, "Context type is required", http.StatusBadRequest)
		return
	}

	// Проверяем валидность типа контекста
	validContextTypes := map[string]bool{
		"chat":    true,
		"group":   true,
		"project": true,
		"task":    true,
		"test":    true,
	}
	if !validContextTypes[contextType] {
		http.Error(w, "Invalid context type", http.StatusBadRequest)
		return
	}

	// Получаем файл
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		http.Error(w, "No file uploaded", http.StatusBadRequest)
		return
	}
	fileHeader := files[0]

	// Открываем файл
	file, err := fileHeader.Open()
	if err != nil {
		log.Printf("Error opening file: %v", err)
		http.Error(w, "Failed to process file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Создаем файл через сервис
	newFile, err := h.fileService.CreateContextFile(r.Context(), file, fileHeader, contextType, userID)
	if err != nil {
		log.Printf("Failed to create context file: %v", err)
		http.Error(w, "Failed to process file", http.StatusInternalServerError)
		return
	}

	// Формируем ответ
	response := domain.FileUploadResponse{
		UUID:        newFile.UUID,
		Name:        newFile.Name,
		MIMEType:    newFile.MIMEType,
		SizeBytes:   newFile.SizeBytes,
		OwnerID:     newFile.OwnerID,
		ContextType: newFile.ContextType,
		CreatedAt:   newFile.CreatedAt,
	}

	// Отправляем ответ
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode response: %v", err)
		http.Error(w, "Failed to generate response", http.StatusInternalServerError)
		return
	}
}

func (h *FileHandler) StreamVideo(w http.ResponseWriter, r *http.Request) {
	log.Printf("[StreamVideo] Начало запроса на стриминг")

	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("[StreamVideo] Ошибка авторизации: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	fileUUID, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		log.Printf("[StreamVideo] Некорректный UUID: %v", err)
		http.Error(w, "Invalid UUID", http.StatusBadRequest)
		return
	}

	file, err := h.fileService.GetFileInfo(r.Context(), fileUUID, userID)
	if err != nil {
		log.Printf("[StreamVideo] Ошибка получения информации о файле: %v", err)
		http.Error(w, "Failed to get file info", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", file.MIMEType)
	w.Header().Set("Accept-Ranges", "bytes")

	rangeHeader := r.Header.Get("Range")
	var start, end int64

	if rangeHeader == "" {
		start = 0
		end = file.SizeBytes - 1
		w.Header().Set("Content-Length", strconv.FormatInt(file.SizeBytes, 10))
		w.WriteHeader(http.StatusOK)
	} else {
		ranges, err := parseRange(rangeHeader, file.SizeBytes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if len(ranges) != 1 {
			http.Error(w, "Multiple ranges not supported", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		start = ranges[0][0]
		end = ranges[0][1]

		size := end - start + 1
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.SizeBytes))
		w.WriteHeader(http.StatusPartialContent)
	}

	reader, err := h.fileService.GetFileDataRange(r.Context(), fileUUID, userID, start, end)
	if err != nil {
		log.Printf("[StreamVideo] Ошибка получения данных: %v", err)
		http.Error(w, "Failed to get file data", http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	// Используем более эффективное копирование
	written, err := io.Copy(w, reader)
	if err != nil {
		log.Printf("[StreamVideo] Ошибка при стриминге: %v", err)
		return
	}

	log.Printf("[StreamVideo] Успешно отправлено %d байт", written)
}

// RenameFile обрабатывает запрос на переименование файла
func (h *FileHandler) RenameFile(w http.ResponseWriter, r *http.Request) {
	// Включаем подробное логирование
	log.Printf("[RenameFile] Начало обработки запроса")
	log.Printf("[RenameFile] Метод запроса: %s", r.Method)
	log.Printf("[RenameFile] URI: %s", r.RequestURI)

	// Проверяем метод запроса
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Проверяем Content-Type
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("[RenameFile] Ошибка авторизации: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем UUID файла
	fileUUID, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		log.Printf("[RenameFile] Некорректный UUID: %v", err)
		http.Error(w, "Invalid file UUID", http.StatusBadRequest)
		return
	}

	// Декодируем тело запроса
	var req struct {
		NewName string `json:"new_name"`
	}

	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		log.Printf("[RenameFile] Ошибка декодирования JSON: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Проверяем новое имя
	if req.NewName == "" {
		log.Printf("[RenameFile] Пустое новое имя")
		http.Error(w, "New name is required", http.StatusBadRequest)
		return
	}

	// Переименовываем файл
	if err := h.fileService.RenameFile(r.Context(), fileUUID, req.NewName, userID); err != nil {
		log.Printf("[RenameFile] Ошибка переименования: %v", err)
		switch {
		case strings.Contains(err.Error(), "access denied"):
			http.Error(w, "Access denied", http.StatusForbidden)
		case strings.Contains(err.Error(), "not found"):
			http.Error(w, "File not found", http.StatusNotFound)
		default:
			http.Error(w, fmt.Sprintf("Failed to rename file: %v", err), http.StatusInternalServerError)
		}
		return
	}

	// Отправляем успешный ответ
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "File renamed successfully",
	})

	log.Printf("[RenameFile] Файл успешно переименован")
}

// MoveFile обрабатывает запрос на перемещение файла
func (h *FileHandler) MoveFile(w http.ResponseWriter, r *http.Request) {
	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем UUID файла из URL
	fileUUID, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		http.Error(w, "Invalid file UUID", http.StatusBadRequest)
		return
	}

	// Читаем JSON из тела запроса
	var req struct {
		NewFolderID int64 `json:"new_folder_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Перемещаем файл
	if err := h.fileService.MoveFile(r.Context(), fileUUID, req.NewFolderID, userID); err != nil {
		if err.Error() == "access denied" {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to move file: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
