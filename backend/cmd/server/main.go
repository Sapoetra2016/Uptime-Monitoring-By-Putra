package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/uptime/monitoring/internal/ai"
	"github.com/uptime/monitoring/internal/api"
	"github.com/uptime/monitoring/internal/auth"
	"github.com/uptime/monitoring/internal/config"
	"github.com/uptime/monitoring/internal/db"
	"github.com/uptime/monitoring/internal/events"
	"github.com/uptime/monitoring/internal/models"
	"github.com/uptime/monitoring/internal/monitor"
	"github.com/uptime/monitoring/internal/notifier"
	"github.com/uptime/monitoring/internal/store"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DBURL)
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	st := store.New(pool)

	// seed the default admin account from env/config so a fresh install is usable.
	hash, err := auth.HashPassword(cfg.AdminPassword)
	if err != nil {
		log.Fatalf("failed to hash admin password: %v", err)
	}
	if err := st.EnsureUser(ctx, cfg.AdminUser, hash, models.RoleAdmin); err != nil {
		log.Printf("warning: could not seed admin user: %v", err)
	}

	hub := events.NewHub()
	dispatcher := notifier.NewDispatcher(st)
	eng := monitor.NewEngine(st, hub, dispatcher, cfg.CheckTimeout)

	// Optional AI root-cause analysis (Qwen via DashScope). Dormant until
	// UPTIME_LLM_API_KEY is set in the environment.
	aiClient := ai.NewClient(ai.Config{APIKey: cfg.LLMAPIKey, BaseURL: cfg.LLMBaseURL, Model: cfg.LLMModel})
	eng.SetAIClient(aiClient)
	if aiClient.Enabled() {
		log.Printf("[ai] enabled: model %s via %s", cfg.LLMModel, cfg.LLMBaseURL)
	}

	if err := eng.Start(ctx); err != nil {
		log.Fatalf("failed to start monitor engine: %v", err)
	}
	go dispatcher.StartEscalationLoop(ctx)

	authenticator := auth.New(cfg.JWTSecret)
	srv := api.NewServer(st, eng, hub, dispatcher, authenticator, cfg.AllowedOrigins)

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("Uptime Monitoring API listening on :%s", cfg.Port)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpSrv.Shutdown(shutdownCtx)
	eng.Stop()
	dispatcher.Stop()
	log.Println("bye")
}
