package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/webhookpayload"
	"github.com/degoke/tronvent/internal/webhookspec"
)

var endpointIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// endpointIDParam returns the {endpointID} path value. Endpoint ids are uuids, so a
// malformed one cannot exist: answer 404 here instead of letting Postgres reject the
// cast and surface a 500.
func endpointIDParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("endpointID")
	if !endpointIDPattern.MatchString(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "webhook endpoint not found"})
		return "", false
	}
	return id, true
}

func (s *Server) handleListWebhookEndpoints(w http.ResponseWriter, r *http.Request) {
	eps, err := s.db.ListWebhookEndpoints(r.Context())
	if err != nil {
		slog.Error("list webhook endpoints", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list webhook endpoints"})
		return
	}
	items := make([]map[string]any, 0, len(eps))
	for _, ep := range eps {
		items = append(items, webhookEndpointResponse(ep))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleGetWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	endpointID, ok := endpointIDParam(w, r)
	if !ok {
		return
	}
	ep, err := s.db.GetWebhookEndpoint(r.Context(), endpointID)
	if err != nil {
		slog.Error("get webhook endpoint", "endpointId", endpointID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load webhook endpoint"})
		return
	}
	if ep == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "webhook endpoint not found"})
		return
	}
	writeJSON(w, http.StatusOK, webhookEndpointResponse(*ep))
}

func (s *Server) handlePostWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	var req webhookEndpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	ep, err := s.buildEndpointFromRequest(req, "")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ep.IsActive = true
	ep.Source = "api"
	out, err := s.db.UpsertWebhookEndpoint(r.Context(), ep)
	if err != nil {
		writeWebhookSaveError(w, err, "failed to save webhook endpoint")
		return
	}
	s.webhookConfig.UpsertEndpoint(*out)
	writeJSON(w, http.StatusCreated, webhookEndpointResponse(*out))
}

func (s *Server) handlePatchWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	endpointID, ok := endpointIDParam(w, r)
	if !ok {
		return
	}
	existing, err := s.db.GetWebhookEndpoint(r.Context(), endpointID)
	if err != nil {
		slog.Error("get webhook endpoint for patch", "endpointId", endpointID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load webhook endpoint"})
		return
	}
	if existing == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "webhook endpoint not found"})
		return
	}
	var req webhookEndpointPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	merged, err := mergeEndpointPatch(*existing, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	out, err := s.db.UpsertWebhookEndpoint(r.Context(), merged)
	if err != nil {
		writeWebhookSaveError(w, err, "failed to save webhook endpoint")
		return
	}
	s.webhookConfig.UpsertEndpoint(*out)
	writeJSON(w, http.StatusOK, webhookEndpointResponse(*out))
}

func (s *Server) handleDeleteWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	endpointID, ok := endpointIDParam(w, r)
	if !ok {
		return
	}
	force := r.URL.Query().Get("force") == "true"
	if err := s.db.DeleteWebhookEndpoint(r.Context(), endpointID, force); err != nil {
		if errors.Is(err, internaldb.ErrWebhookEndpointNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "webhook endpoint not found"})
			return
		}
		if errors.Is(err, internaldb.ErrWebhookEndpointHasUndeliveredEvents) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "webhook endpoint has undelivered events; wait for them to finish or pass ?force=true to discard them",
			})
			return
		}
		slog.Error("delete webhook endpoint", "endpointId", endpointID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete webhook endpoint"})
		return
	}
	s.webhookConfig.RemoveEndpoint(endpointID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListWebhookSchemas(w http.ResponseWriter, r *http.Request) {
	schemas, err := webhookpayload.AllEventSchemas()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load schemas"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schemas": schemas})
}

func (s *Server) handleGetWebhookSchema(w http.ResponseWriter, r *http.Request) {
	eventType := r.URL.Query().Get("type")
	if eventType == "" {
		eventType = r.PathValue("eventType")
	}
	raw, err := webhookpayload.EventSchemaJSON(eventType)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown event type"})
		return
	}
	w.Header().Set("Content-Type", "application/schema+json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

type webhookEndpointRequest struct {
	WebhookURL         string   `json:"webhookUrl"`
	SigningSecret      string   `json:"signingSecret"`
	EventTypes         []string `json:"eventTypes"`
	FailureNotifyEmail string   `json:"failureNotifyEmail"`
}

type webhookEndpointPatchRequest struct {
	WebhookURL         *string  `json:"webhookUrl"`
	SigningSecret      *string  `json:"signingSecret"`
	EventTypes         []string `json:"eventTypes"`
	IsActive           *bool    `json:"isActive"`
	FailureNotifyEmail *string  `json:"failureNotifyEmail"`
}

func (s *Server) buildEndpointFromRequest(req webhookEndpointRequest, id string) (internaldb.WebhookEndpoint, error) {
	req.WebhookURL = strings.TrimSpace(req.WebhookURL)
	if req.WebhookURL == "" {
		return internaldb.WebhookEndpoint{}, errors.New("webhookUrl is required")
	}
	signingKey := strings.TrimSpace(req.SigningSecret)
	publicKey := ""
	if signingKey == "" {
		priv, pub, err := webhookspec.GenerateSigningKeyPair()
		if err != nil {
			return internaldb.WebhookEndpoint{}, err
		}
		signingKey, publicKey = priv, pub
	} else if strings.HasPrefix(signingKey, "whsk_") {
		publicKey, _ = webhookspec.PublicKeyFromPrivateKey(signingKey)
	}
	if err := webhookpayload.ValidateEventTypes(req.EventTypes); err != nil {
		return internaldb.WebhookEndpoint{}, err
	}
	return internaldb.WebhookEndpoint{
		ID: id, WebhookURL: req.WebhookURL, SigningSecret: signingKey, SigningPublicKey: publicKey,
		EventTypes: req.EventTypes, FailureNotifyEmail: strings.TrimSpace(req.FailureNotifyEmail),
	}, nil
}

func mergeEndpointPatch(existing internaldb.WebhookEndpoint, req webhookEndpointPatchRequest) (internaldb.WebhookEndpoint, error) {
	if req.WebhookURL != nil {
		existing.WebhookURL = strings.TrimSpace(*req.WebhookURL)
	}
	if req.SigningSecret != nil && strings.TrimSpace(*req.SigningSecret) != "" {
		existing.SigningSecret = strings.TrimSpace(*req.SigningSecret)
		if strings.HasPrefix(existing.SigningSecret, "whsk_") {
			existing.SigningPublicKey, _ = webhookspec.PublicKeyFromPrivateKey(existing.SigningSecret)
		}
	}
	if len(req.EventTypes) > 0 {
		if err := webhookpayload.ValidateEventTypes(req.EventTypes); err != nil {
			return existing, err
		}
		existing.EventTypes = req.EventTypes
	}
	if req.IsActive != nil {
		existing.IsActive = *req.IsActive
	}
	if req.FailureNotifyEmail != nil {
		existing.FailureNotifyEmail = strings.TrimSpace(*req.FailureNotifyEmail)
	}
	return existing, nil
}

// writeWebhookSaveError answers 400 with the reason when the endpoint was rejected
// (bad URL, signing key, or event types), 503 when the webhook host could not be
// resolved right now, and logs anything else as a 500.
func writeWebhookSaveError(w http.ResponseWriter, err error, msg string) {
	if errors.Is(err, internaldb.ErrInvalidWebhookEndpoint) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if errors.Is(err, webhookspec.ErrHostLookup) {
		slog.Warn(msg, "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not resolve the webhook host right now; try again"})
		return
	}
	slog.Error(msg, "err", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
}

func webhookEndpointResponse(ep internaldb.WebhookEndpoint) map[string]any {
	return map[string]any{
		"id":                 ep.ID,
		"webhookUrl":         ep.WebhookURL,
		"signingPublicKey":   ep.SigningPublicKey,
		"eventTypes":         ep.EventTypes,
		"isActive":           ep.IsActive,
		"failureNotifyEmail": ep.FailureNotifyEmail,
		"updatedAt":          ep.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}
