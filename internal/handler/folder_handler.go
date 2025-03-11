package handler

import (
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"log"
	"net/http"
	"strconv"
	"synxrondrive/internal/auth"
	"synxrondrive/internal/domain"
	"synxrondrive/internal/service"
)

type FolderHandler struct {
	folderService *service.FolderService
	trashService  *service.TrashService
}

type createFolderRequest struct {
	Name     string `json:"name"`
	ParentID *int64 `json:"parent_id,omitempty"`
}

func NewFolderHandler(folderService *service.FolderService, trashService *service.TrashService) *FolderHandler {
	return &FolderHandler{
		folderService: folderService,
		trashService:  trashService,
	}
}

func (h *FolderHandler) CreateFolder(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req createFolderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	folder, err := h.folderService.CreateFolder(r.Context(), req.Name, req.ParentID, userID)
	if err != nil {
		http.Error(w, "Failed to create folder", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(folder)
}

func (h *FolderHandler) GetFolderContent(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("Authorization failed: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	log.Printf("Authenticated user: %s", userID)

	var folderID int64
	folderIDStr := chi.URLParam(r, "id")
	if folderIDStr == "" {
		log.Printf("No folder ID provided, getting root folder")
		rootFolder, err := h.folderService.GetOrCreateRootFolder(r.Context(), userID)
		if err != nil {
			log.Printf("Failed to get root folder: %v", err)
			http.Error(w, "Failed to get root folder", http.StatusInternalServerError)
			return
		}
		folderID = rootFolder.ID
		log.Printf("Using root folder ID: %d", folderID)
	} else {
		var err error
		folderID, err = strconv.ParseInt(folderIDStr, 10, 64)
		if err != nil {
			log.Printf("Invalid folder ID provided: %s, error: %v", folderIDStr, err)
			http.Error(w, "Invalid folder ID", http.StatusBadRequest)
			return
		}
		log.Printf("Getting content for folder ID: %d", folderID)
	}

	content, err := h.folderService.GetFolderContent(r.Context(), folderID, userID)
	if err != nil {
		log.Printf("Error getting folder content: %v", err)
		http.Error(w, fmt.Sprintf("Failed to get folder content: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully retrieved folder content. Sending response")

	// Изменённая структура ответа
	response := struct {
		FolderID int64           `json:"folder_id"`
		Folder   *domain.Folder  `json:"folder,omitempty"` // Добавляем информацию о текущей папке
		Files    []domain.File   `json:"files"`
		Folders  []domain.Folder `json:"folders"`
	}{
		FolderID: folderID,
		Folder:   &content.Folder, // Включаем информацию о текущей папке
		Files:    content.Files,
		Folders:  content.Folders,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding response: %v", err)
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		return
	}
	log.Printf("Response sent successfully")
}

func (h *FolderHandler) DeleteFolder(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	folderID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid folder ID", http.StatusBadRequest)
		return
	}

	err = h.folderService.DeleteFolder(r.Context(), folderID, userID)
	if err != nil {
		http.Error(w, "Failed to delete folder", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// В FolderHandler добавьте:
func (h *FolderHandler) GetFolderStructure(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	folders, err := h.folderService.GetFolderStructure(r.Context(), userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(folders)
}

// RenameFolder обрабатывает запрос на переименование папки
func (h *FolderHandler) RenameFolder(w http.ResponseWriter, r *http.Request) {
	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем ID папки из URL
	folderID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid folder ID", http.StatusBadRequest)
		return
	}

	// Читаем JSON из тела запроса
	var req struct {
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Проверяем, что новое имя не пустое
	if req.NewName == "" {
		http.Error(w, "New name is required", http.StatusBadRequest)
		return
	}

	// Переименовываем папку
	if err := h.folderService.RenameFolder(r.Context(), folderID, req.NewName, userID); err != nil {
		if err.Error() == "access denied" {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to rename folder: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// MoveFolder обрабатывает запрос на перемещение папки
func (h *FolderHandler) MoveFolder(w http.ResponseWriter, r *http.Request) {
	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем ID папки из URL
	folderID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid folder ID", http.StatusBadRequest)
		return
	}

	// Читаем JSON из тела запроса
	var req struct {
		NewParentID int64 `json:"new_parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Перемещаем папку
	if err := h.folderService.MoveFolder(r.Context(), folderID, req.NewParentID, userID); err != nil {
		if err.Error() == "access denied" {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to move folder: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
