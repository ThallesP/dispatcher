package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

type notificationTargetDTO struct {
	ID              uint              `json:"id,omitempty"`
	Name            string            `json:"name"`
	Kind            string            `json:"kind"`
	URL             string            `json:"url"`
	Headers         map[string]string `json:"headers"`
	BodyTemplate    string            `json:"bodyTemplate"`
	Enabled         bool              `json:"enabled"`
	OnPayout        bool              `json:"onPayout"`
	OnHealthDrop    bool              `json:"onHealthDrop"`
	OnWeeklySummary bool              `json:"onWeeklySummary"`
	CreatedAt       time.Time         `json:"createdAt,omitempty"`
	UpdatedAt       time.Time         `json:"updatedAt,omitempty"`
	Event           string            `json:"event,omitempty"`
}

func notificationTargetToDTO(target NotificationTarget) (notificationTargetDTO, error) {
	headers, err := notificationHeaders(target)
	if err != nil {
		return notificationTargetDTO{}, err
	}
	return notificationTargetDTO{
		ID:              target.ID,
		Name:            target.Name,
		Kind:            target.Kind,
		URL:             target.URL,
		Headers:         headers,
		BodyTemplate:    target.BodyTemplate,
		Enabled:         target.Enabled,
		OnPayout:        target.OnPayout,
		OnHealthDrop:    target.OnHealthDrop,
		OnWeeklySummary: target.OnWeeklySummary,
		CreatedAt:       target.CreatedAt,
		UpdatedAt:       target.UpdatedAt,
	}, nil
}

func notificationTargetFromDTO(dto notificationTargetDTO) (NotificationTarget, error) {
	headersJSON, err := encodeNotificationHeaders(dto.Headers)
	if err != nil {
		return NotificationTarget{}, err
	}
	return NotificationTarget{
		ID:              dto.ID,
		Name:            strings.TrimSpace(dto.Name),
		Kind:            dto.Kind,
		URL:             strings.TrimSpace(dto.URL),
		HeadersJSON:     headersJSON,
		BodyTemplate:    dto.BodyTemplate,
		Enabled:         dto.Enabled,
		OnPayout:        dto.OnPayout,
		OnHealthDrop:    dto.OnHealthDrop,
		OnWeeklySummary: dto.OnWeeklySummary,
		CreatedAt:       dto.CreatedAt,
		UpdatedAt:       dto.UpdatedAt,
	}, nil
}

func handleNotificationTargets(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		targets, err := gorm.G[NotificationTarget](db).Order("created_at, id").Find(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		result := make([]notificationTargetDTO, 0, len(targets))
		for _, target := range targets {
			dto, err := notificationTargetToDTO(target)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			result = append(result, dto)
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func handleCreateNotificationTarget(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var dto notificationTargetDTO
		if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		target, err := notificationTargetFromDTO(dto)
		if err == nil {
			err = validateNotificationTarget(target)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		target.ID = 0
		target.CreatedAt = time.Time{}
		target.UpdatedAt = time.Time{}
		if err := gorm.G[NotificationTarget](db).Create(r.Context(), &target); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		created, err := notificationTargetToDTO(target)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

func handleUpdateNotificationTarget(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := notificationTargetID(w, r)
		if !ok {
			return
		}
		existing, err := gorm.G[NotificationTarget](db).Where("id = ?", id).First(r.Context())
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "notification target not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		var dto notificationTargetDTO
		if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		target, err := notificationTargetFromDTO(dto)
		if err == nil {
			target.ID = existing.ID
			target.CreatedAt = existing.CreatedAt
			err = validateNotificationTarget(target)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := db.WithContext(r.Context()).Save(&target).Error; err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		updated, err := notificationTargetToDTO(target)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

func handleDeleteNotificationTarget(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := notificationTargetID(w, r)
		if !ok {
			return
		}
		rows, err := gorm.G[NotificationTarget](db).Where("id = ?", id).Delete(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if rows == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "notification target not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func notificationTargetID(w http.ResponseWriter, r *http.Request) (uint, bool) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid notification target id"})
		return 0, false
	}
	return uint(id), true
}

type notificationTestResponse struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"statusCode"`
	Error      string `json:"error,omitempty"`
}

func handleTestNotificationTarget(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := notificationTargetID(w, r)
		if !ok {
			return
		}
		target, err := gorm.G[NotificationTarget](db).Where("id = ?", id).First(r.Context())
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "notification target not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		var req struct {
			Event string `json:"event"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		sendNotificationTest(w, r, target, req.Event)
	}
}

func handleTestNotificationDraft(w http.ResponseWriter, r *http.Request) {
	var req notificationTargetDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	target, err := notificationTargetFromDTO(req)
	if err == nil {
		err = validateNotificationTarget(target)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	sendNotificationTest(w, r, target, req.Event)
}

func sendNotificationTest(w http.ResponseWriter, r *http.Request, target NotificationTarget, event string) {
	if event != "payout" && event != "health_drop" && event != "weekly_summary" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "event must be payout, health_drop, or weekly_summary"})
		return
	}
	err := sendNotification(r.Context(), target, sampleNotificationEvent(event))
	if err == nil {
		writeJSON(w, http.StatusOK, notificationTestResponse{OK: true, StatusCode: http.StatusOK})
		return
	}
	statusCode := 0
	var statusErr *notificationStatusError
	if errors.As(err, &statusErr) {
		statusCode = statusErr.StatusCode
	}
	writeJSON(w, http.StatusBadGateway, notificationTestResponse{
		OK:         false,
		StatusCode: statusCode,
		Error:      err.Error(),
	})
}

func handleNotificationPresets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, notificationPresets)
}
