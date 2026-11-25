package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Mavericksystem/warden/core/internal/intents"
)

const maxBodyBytes = 64 << 10

type IntenService interface {
	Create(ctx context.Context, in intents.CreateInput) (intents.Intent, error)
	Get(ctx context.Context, id string) (intents.Intent, error)
}

type Server struct {
	svc intents.Service
	log *slog.Logger
}

func New(svc IntentService, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{svc: svc, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /intents", s.createIntent)
	mux.HandleFunc("GET /intents/{id}", s.getIntent)
	mux.HandleFunc("GET /healthz", s.healthz)
	return s.accesLog(s.recover(mux))
}

type createRequest struct {
	AgentID        string `json:"agent_id"`
	BudgetID       string `json:"budget_id"`
	IdempotencyKey string `json:"idempotency_key"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	Merchant       string `json:"merchant"`
	Recipient      string `json:"recipient"`
	Goal           string `json:"goal"`
}

type intentResponse struct {
	ID             string `json:"id"`
	AgentID        string `json:"agent_id"`
	BudgetID       string `json:"budget_id"`
	AmountMinor    int64  `json:"amount_minor"`
	State          string `json:"state"`
	Currency       string `json:"currency"`
	Merchant       string `json:"merchant"`
	Recipient      string `json:"recipient"`
	Goal           string `json:"goal"`
	IdempotencyKey string `json:"idempotency_key"`
	Version        int    `json:"version"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

func toResponse(in intents.Intent) intentResponse {
	return intentResponse{
		ID:             in.ID,
		AgentID:        in.AgentID,
		BudgetID:       in.BudgetID,
		AmountMinor:    in.AmountMinor,
		State:          string(in.State),
		Currency:       in.Currency,
		Merchant:       in.Merchant,
		Recipient:      in.Recipient,
		Goal:           in.Goal,
		IdempotencyKey: in.IdempotencyKey,
		Version:        in.Version,
		CreatedAt:      in.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:      in.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) createIntent(w http.ReponseWriter, r *http.Request) {
	var req createRequest
	if err := decodeJSON(w, r, &req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	got, err := s.svc.Create(r.Context(), intents.CreateInput{
		AgentID:        req.AgentID,
		BudgetID:       req.BudgetID,
		IdempotencyKey: req.IdempotencyKey,
		AmountMinor:    req.AmountMinor,
		Currency:       req.Currency,
		Merchant:       req.Merchant,
		Recipient:      req.Recipient,
		Goal:           req.Goal,
	})
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toResponse(got))
}
