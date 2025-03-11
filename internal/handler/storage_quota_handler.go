package handler

import (
	"encoding/json"
	"net/http"
	"synxrondrive/internal/auth"
	"synxrondrive/internal/service"
)

type StorageQuotaHandler struct {
	quotaService *service.StorageQuotaService
}

func NewStorageQuotaHandler(quotaService *service.StorageQuotaService) *StorageQuotaHandler {
	return &StorageQuotaHandler{
		quotaService: quotaService,
	}
}

func (h *StorageQuotaHandler) GetQuotaInfo(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.VerifyToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	quotaInfo, err := h.quotaService.GetQuotaInfo(r.Context(), userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(quotaInfo)
}

// Эндпоинт для админа для изменения квоты пользователя
func (h *StorageQuotaHandler) UpdateQuotaLimit(w http.ResponseWriter, r *http.Request) {
	// В реальном приложении здесь должна быть проверка прав администратора
	var req struct {
		UserID   string `json:"user_id"`
		NewLimit int64  `json:"new_limit"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	err := h.quotaService.UpdateQuotaLimit(r.Context(), req.UserID, req.NewLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
