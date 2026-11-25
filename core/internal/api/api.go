package api

import (
	"context"
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
