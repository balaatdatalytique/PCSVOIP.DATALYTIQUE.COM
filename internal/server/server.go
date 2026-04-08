package server

import (
	"fmt"
	"log"
	"net/http"

	"pcsvoip-cms/internal/ai"
	"pcsvoip-cms/internal/auth"
	"pcsvoip-cms/internal/cms"
	"pcsvoip-cms/internal/config"
	"pcsvoip-cms/internal/middleware"
	"pcsvoip-cms/internal/routes"
	"pcsvoip-cms/internal/storage"
)

func Run(cfg *config.Config) error {
	// Initialize storage
	store, err := storage.NewService(cfg.ContentDir)
	if err != nil {
		return fmt.Errorf("storage init failed: %w", err)
	}

	// Initialize AI provider (optional — runs without AI if no key)
	var aiProvider ai.AIProvider
	if cfg.AIAPIKey != "" {
		aiProvider, err = ai.NewProvider(ai.ProviderConfig{
			Provider: cfg.AIProvider,
			APIKey:   cfg.AIAPIKey,
			Model:    cfg.AIModel,
		})
		if err != nil {
			log.Printf("WARNING: AI provider init failed: %v (AI features disabled)", err)
		}
	} else {
		log.Println("INFO: No AI API key configured — AI features disabled")
	}

	// Initialize auth
	authMgr := auth.NewManager(cfg.AdminUser, cfg.AdminPass)

	// Initialize CMS
	cmsSvc := cms.NewService(store, aiProvider)

	// Setup routes
	templateDir := "web/templates"
	handler, err := routes.NewHandler(cmsSvc, authMgr, templateDir)
	if err != nil {
		return fmt.Errorf("route handler init failed: %w", err)
	}

	mux := http.NewServeMux()
	handler.SetupRoutes(mux, cfg.ContentDir)

	// Apply global middleware
	var h http.Handler = mux
	h = middleware.Logging(h)
	h = middleware.Recovery(h)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Starting CMS server on %s (content: %s)", addr, cfg.ContentDir)
	return http.ListenAndServe(addr, h)
}
