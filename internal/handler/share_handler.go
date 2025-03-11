package handler

import (
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"io"
	"log"
	"net/http"
	"strconv"
	"synxrondrive/internal/auth"
	"synxrondrive/internal/domain"
	"synxrondrive/internal/service"
	"time"
)

type ShareHandler struct {
	shareService *service.ShareService
}

type createShareRequest struct {
	ResourceID   string              `json:"resource_id"`
	ResourceType domain.ResourceType `json:"resource_type"`
	AccessType   domain.AccessType   `json:"access_type"`
	ExpiresIn    *int64              `json:"expires_in,omitempty"`
}

func NewShareHandler(shareService *service.ShareService) *ShareHandler {
	return &ShareHandler{shareService: shareService}
}

// handler/share_handler.go

func (h *ShareHandler) CreateShare(w http.ResponseWriter, r *http.Request) {
	log.Printf("[CreateShare] Processing new share request")

	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("[CreateShare] Authentication failed: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req createShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[CreateShare] Failed to decode request body: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	log.Printf("[CreateShare] Creating share for resource: %s, type: %s", req.ResourceID, req.ResourceType)

	var expiresIn *time.Duration
	if req.ExpiresIn != nil {
		duration := time.Duration(*req.ExpiresIn) * time.Second
		expiresIn = &duration
	}

	share, err := h.shareService.CreateShare(
		r.Context(),
		req.ResourceID,
		req.ResourceType,
		userID,
		req.AccessType,
		expiresIn,
		userID,
	)
	if err != nil {
		log.Printf("[CreateShare] Failed to create share: %v", err)
		http.Error(w, fmt.Sprintf("Failed to create share: %v", err), http.StatusInternalServerError)
		return
	}

	// Создаем расширенный ответ
	response := struct {
		ID           string              `json:"id"`
		ResourceID   string              `json:"resource_id"`
		ResourceType domain.ResourceType `json:"resource_type"`
		AccessType   domain.AccessType   `json:"access_type"`
		Token        string              `json:"token"`
		ExpiresAt    *time.Time          `json:"expires_at,omitempty"`
		CreatedAt    time.Time           `json:"created_at"`
	}{
		ID:           share.ID.String(),
		ResourceID:   share.ResourceID,
		ResourceType: share.ResourceType,
		AccessType:   share.AccessType,
		Token:        share.Token,
		ExpiresAt:    share.ExpiresAt,
		CreatedAt:    share.CreatedAt,
	}

	log.Printf("[CreateShare] Successfully created share with ID: %s", share.ID)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

func (h *ShareHandler) GetSharedResource(w http.ResponseWriter, r *http.Request) {
	log.Printf("[GetSharedResource] Started with path: %s", r.URL.String())

	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("[GetSharedResource] Authorization error: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	log.Printf("[GetSharedResource] Authorized user: %s", userID)

	shareID := chi.URLParam(r, "id")
	if shareID == "" {
		log.Printf("[GetSharedResource] Missing share ID in request")
		http.Error(w, "Share ID is required", http.StatusBadRequest)
		return
	}
	log.Printf("[GetSharedResource] Processing share ID: %s", shareID)

	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	log.Printf("[GetSharedResource] Path parameter: %s", path)

	content, err := h.shareService.GetSharedContent(r.Context(), shareID, path, userID)
	if err != nil {
		log.Printf("[GetSharedResource] Error getting content: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.shareService.AddUserToShare(r.Context(), shareID, userID); err != nil {
		log.Printf("[GetSharedResource] Error adding user to share: %v", err)
		// Не возвращаем ошибку, так как доступ уже получен
	}

	log.Printf("[GetSharedResource] Successfully retrieved content for share %s", shareID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(content)
}

func (h *ShareHandler) GetSharedWithMe(w http.ResponseWriter, r *http.Request) {
	log.Printf("[GetSharedWithMe] Starting request")

	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("[GetSharedWithMe] Authorization failed: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	contents, err := h.shareService.GetUserSharedContent(r.Context(), userID)
	if err != nil {
		log.Printf("[GetSharedWithMe] Failed to get shared content: %v", err)
		http.Error(w, "Failed to get shared content", http.StatusInternalServerError)
		return
	}

	log.Printf("[GetSharedWithMe] Successfully retrieved %d shared items", len(contents))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(contents)
}

func (h *ShareHandler) GrantAccess(w http.ResponseWriter, r *http.Request) {
	log.Printf("[GrantAccess] Starting access request")

	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("[GrantAccess] Authorization failed: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	token := chi.URLParam(r, "token")
	if token == "" {
		log.Printf("[GrantAccess] No token provided")
		http.Error(w, "Token is required", http.StatusBadRequest)
		return
	}
	log.Printf("[GrantAccess] Processing token: %s", token)

	var requestBody struct {
		FolderID string `json:"folder_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		if err != io.EOF {
			log.Printf("[GrantAccess] Error decoding request body: %v", err)
		}
	}
	log.Printf("[GrantAccess] Requested folder ID: %s", requestBody.FolderID)

	// Получаем share и проверяем доступ
	share, resource, err := h.shareService.GrantAccess(r.Context(), token, userID)
	if err != nil {
		log.Printf("[GrantAccess] Error granting access: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[GrantAccess] Access granted for share: %s", share.ID)

	if requestBody.FolderID != "" {
		log.Printf("[GrantAccess] Retrieving content for folder: %s", requestBody.FolderID)
		content, err := h.shareService.GetSharedContent(
			r.Context(),
			share.ID.String(),
			fmt.Sprintf("/folders/%s", requestBody.FolderID),
			userID,
		)
		if err != nil {
			log.Printf("[GrantAccess] Error getting folder content: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(content)
		return
	}

	response := struct {
		ShareID      string      `json:"shareId"`
		ResourceID   string      `json:"resourceId"`
		ResourceType string      `json:"resourceType"`
		Resource     interface{} `json:"resource"`
	}{
		ShareID:      share.ID.String(),
		ResourceID:   share.ResourceID,
		ResourceType: string(share.ResourceType),
		Resource:     resource,
	}

	log.Printf("[GrantAccess] Successfully completed access request")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *ShareHandler) GetSharedFolderContent(w http.ResponseWriter, r *http.Request) {
	log.Printf("[GetSharedFolderContent] Starting request")

	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("[GetSharedFolderContent] Authorization failed: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	token := chi.URLParam(r, "token")
	if token == "" {
		log.Printf("[GetSharedFolderContent] No token provided")
		http.Error(w, "Token is required", http.StatusBadRequest)
		return
	}

	// Получаем share по токену
	share, err := h.shareService.GetSharedResource(r.Context(), token)
	if err != nil {
		log.Printf("[GetSharedFolderContent] Failed to get share: %v", err)
		http.Error(w, fmt.Sprintf("Failed to get share: %v", err), http.StatusNotFound)
		return
	}

	// Получаем ID папки из query параметров
	folderID := r.URL.Query().Get("folder_id")
	log.Printf("[GetSharedFolderContent] Requested folder ID: %s", folderID)

	// Если folder_id не указан, используем ID корневой папки
	if folderID == "" && share != nil {
		if folderContent, ok := share.Data.(domain.FolderContent); ok {
			folderID = strconv.FormatInt(folderContent.Folder.ID, 10)
			log.Printf("[GetSharedFolderContent] Using root folder ID: %s", folderID)
		}
	}

	// Получаем содержимое через ShareRepository
	content, err := h.shareService.GetSharedFolderContent(r.Context(), token, folderID, userID)
	if err != nil {
		log.Printf("[GetSharedFolderContent] Failed to get folder content: %v", err)
		http.Error(w, fmt.Sprintf("Failed to get folder content: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("[GetSharedFolderContent] Successfully retrieved folder content")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(content)
}

func (h *ShareHandler) GetSharedFolderStructure(w http.ResponseWriter, r *http.Request) {
	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем ID share из URL
	shareID := chi.URLParam(r, "id")
	if shareID == "" {
		http.Error(w, "Share ID is required", http.StatusBadRequest)
		return
	}

	// Получаем структуру папок
	folders, err := h.shareService.GetSharedFolderStructure(r.Context(), shareID, userID)
	if err != nil {
		if err.Error() == "access denied" {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to get folder structure: %v", err), http.StatusInternalServerError)
		return
	}

	// Отправляем ответ
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(folders)
}
