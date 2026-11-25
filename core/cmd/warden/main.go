package main

import (
	"log/slog"
	"os"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("warden stopped with an error", "error", err)
		os.Exit(1)
	}
}
func run(log *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL, nil)
	if err != nil {
		return err
	}
	defer pool.Close()
	// Migrate takes an advisory lock, so several instances starting together are safe.
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	store := policystore.New(pool)
	
	svc := intents.New(pool, store, nil)
	
	log.Warn("no AI checker is configured: every intent that passes the policy rules goes to human approval")
	if !cfg.Loopback() {
		log.Warn("listening on a non loopback address and there is no authentication yet", "addr", cfg.ListenAddr)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.New(svc, log),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second, 
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}