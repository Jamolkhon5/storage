package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"synxrondrive/internal/auth"
	"synxrondrive/internal/service"
)

type TrashHandler struct {
	trashService *service.TrashService
}

func NewTrashHandler(trashService *service.TrashService) *TrashHandler {
	return &TrashHandler{trashService: trashService}
}

// GetTrashItems обрабатывает запрос на получение содержимого корзины
func (h *TrashHandler) GetTrashItems(w http.ResponseWriter, r *http.Request) {
	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("Authorization failed: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем содержимое корзины
	items, err := h.trashService.GetTrashItems(r.Context(), userID)
	if err != nil {
		log.Printf("Failed to get trash items: %v", err)
		http.Error(w, "Failed to get trash items", http.StatusInternalServerError)
		return
	}

	// Отправляем ответ
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

// EmptyTrash обрабатывает запрос на очистку корзины
func (h *TrashHandler) EmptyTrash(w http.ResponseWriter, r *http.Request) {
	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("Authorization failed: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Очищаем корзину
	if err := h.trashService.EmptyTrash(r.Context(), userID); err != nil {
		log.Printf("Failed to empty trash: %v", err)
		http.Error(w, "Failed to empty trash", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// RestoreItem обрабатывает запрос на восстановление элемента из корзины
func (h *TrashHandler) RestoreItem(w http.ResponseWriter, r *http.Request) {
	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("Authorization failed: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем данные из запроса
	var req struct {
		ItemID   string `json:"item_id"`
		ItemType string `json:"item_type"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Failed to decode request: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Восстанавливаем элемент
	if err := h.trashService.RestoreFromTrash(r.Context(), req.ItemID, req.ItemType, userID); err != nil {
		log.Printf("Failed to restore item: %v", err)
		http.Error(w, "Failed to restore item", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// DeletePermanently обрабатывает запрос на окончательное удаление элемента
func (h *TrashHandler) DeletePermanently(w http.ResponseWriter, r *http.Request) {
	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("Authorization failed: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем данные из запроса
	var req struct {
		ItemID   string `json:"item_id"`
		ItemType string `json:"item_type"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Failed to decode request: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Удаляем элемент
	if err := h.trashService.DeletePermanently(r.Context(), req.ItemID, req.ItemType, userID); err != nil {
		log.Printf("Failed to delete item permanently: %v", err)
		http.Error(w, "Failed to delete item", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// UpdateSettings обрабатывает запрос на обновление настроек корзины
func (h *TrashHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("Authorization failed: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем данные из запроса
	var req struct {
		RetentionPeriod string `json:"retention_period"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Failed to decode request: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Обновляем настройки
	if err := h.trashService.UpdateRetentionPeriod(r.Context(), userID, req.RetentionPeriod); err != nil {
		log.Printf("Failed to update settings: %v", err)
		http.Error(w, "Failed to update settings", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GetSettings обрабатывает запрос на получение настроек корзины
func (h *TrashHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	// Проверяем авторизацию
	userID, err := auth.VerifyToken(r)
	if err != nil {
		log.Printf("Authorization failed: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Получаем настройки
	settings, err := h.trashService.GetSettings(r.Context(), userID)
	if err != nil {
		log.Printf("Failed to get settings: %v", err)
		http.Error(w, "Failed to get settings", http.StatusInternalServerError)
		return
	}

	// Отправляем ответ
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}
