package preview

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"log"
	"net/http"
	"synxrondrive/internal/service"
)

type Handler struct {
	service     *Service
	fileService *service.FileService
}

func NewHandler(service *Service, fileService *service.FileService) *Handler {
	return &Handler{
		service:     service,
		fileService: fileService,
	}
}

func (h *Handler) GetPreview(w http.ResponseWriter, r *http.Request) {
	// Получаем UUID файла
	fileUUID, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		log.Printf("Invalid UUID: %v", err)
		http.Error(w, "Invalid file UUID", http.StatusBadRequest)
		return
	}

	// Получаем базовую информацию о файле без проверки прав доступа
	file, err := h.fileService.GetBasicFileInfo(r.Context(), fileUUID)
	if err != nil {
		log.Printf("Failed to get file info: %v", err)
		http.Error(w, "Failed to get file info", http.StatusInternalServerError)
		return
	}

	// Получаем данные файла напрямую из S3 без проверки прав доступа
	fileData, err := h.fileService.GetFileDataDirect(r.Context(), fileUUID)
	if err != nil {
		log.Printf("Failed to get file data: %v", err)
		http.Error(w, "Failed to get file data", http.StatusInternalServerError)
		return
	}

	// Получаем или генерируем превью
	previewData, err := h.service.GetOrGeneratePreview(
		r.Context(),
		fileUUID.String(),
		file.MIMEType,
		fileData,
	)
	if err != nil {
		log.Printf("Failed to generate preview: %v", err)
		http.Error(w, fmt.Sprintf("Failed to generate preview: %v", err), http.StatusInternalServerError)
		return
	}

	// Устанавливаем заголовки
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400") // кешируем на 24 часа

	// Отправляем превью
	w.WriteHeader(http.StatusOK)
	w.Write(previewData)
}
