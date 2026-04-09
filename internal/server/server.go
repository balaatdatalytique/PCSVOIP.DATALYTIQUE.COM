package server

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"pcsvoip-cms/internal/admin"
	"pcsvoip-cms/internal/ai"
	"pcsvoip-cms/internal/auth"
	"pcsvoip-cms/internal/cms"
	"pcsvoip-cms/internal/config"
	"pcsvoip-cms/internal/cryptox"
	"pcsvoip-cms/internal/db"
	"pcsvoip-cms/internal/middleware"
	"pcsvoip-cms/internal/routes"
	"pcsvoip-cms/internal/storage"
)

func Run(cfg *config.Config) error {
	// 1. Embedded DB and master key. The master key file lives next to the
	// bbolt database so a single volume backs up everything.
	dataDir := filepath.Dir(cfg.DBPath)
	if _, err := cryptox.LoadMasterKey(dataDir); err != nil {
		return fmt.Errorf("master key: %w", err)
	}
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	log.Printf("admin: bbolt opened at %s", database.Path())

	// 2. Bootstrap admin user, default bot config, default settings.
	if err := admin.Bootstrap(database, cfg.AdminUser, cfg.AdminPass, cfg.BotContextFile); err != nil {
		return fmt.Errorf("admin bootstrap: %w", err)
	}

	// 3. Storage + CMS file editor (existing).
	store, err := storage.NewService(cfg.ContentDir)
	if err != nil {
		return fmt.Errorf("storage init failed: %w", err)
	}

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

	// 4. Auth and CMS service.
	authMgr := auth.NewManager(database, cfg.AdminUser, cfg.AdminPass)
	cmsSvc := cms.NewService(store, aiProvider)
	cmsHandler, err := routes.NewHandler(cmsSvc, authMgr, "web/templates")
	if err != nil {
		return fmt.Errorf("cms route init: %w", err)
	}

	// 5. Admin module.
	geoService := admin.NewGeoService(database)
	visitorRepo := admin.NewVisitorRepo(database, geoService)
	adminHandler, err := admin.New(admin.Handler{
		Auth:          authMgr,
		Users:         admin.NewUserRepo(database),
		Settings:      admin.NewSettingsRepo(database),
		Bot:           admin.NewBotRepo(database),
		KB:            admin.NewKBRepo(database),
		Visitors:      visitorRepo,
		VoiceProxyURL: cfg.VoiceProxyURL,
	}, "web/templates/admin")
	if err != nil {
		return fmt.Errorf("admin init: %w", err)
	}
	apiHandler := admin.NewAPI(
		admin.NewBotRepo(database),
		admin.NewKBRepo(database),
		visitorRepo,
		cfg.InternalAPIToken,
	)

	// 6. Routing.
	mux := http.NewServeMux()
	registerAdminRoutes(mux, adminHandler, cmsHandler, authMgr)
	registerInternalAPI(mux, apiHandler)

	// Static assets for admin UI live under /web/static/.
	staticFS := http.FileServer(http.Dir(filepath.Join(cfg.ContentDir, "web", "static")))
	mux.Handle("/web/static/", http.StripPrefix("/web/static/", staticFS))

	// Public site (must be last).
	site := http.FileServer(http.Dir(cfg.ContentDir))
	// Wrap site fileserver with visitor tracking.
	track := func(ip, ua, path, referrer string) {
		_ = visitorRepo.Track(admin.VisitorEvent{
			VisitorID: admin.VisitorID(ip, ua),
			Type:      "page_view",
			Path:      path,
			Referrer:  referrer,
			IP:        ip,
		})
	}
	mux.Handle("/", middleware.VisitorTrack(track, site))

	// Global middleware (logging, recovery).
	var h http.Handler = mux
	h = middleware.Logging(h)
	h = middleware.Recovery(h)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Starting CMS server on %s (content: %s)", addr, cfg.ContentDir)
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

// registerAdminRoutes wires the new admin module routes plus the existing
// CMS file editor (now under /admin/files/*) behind the auth + CSRF stack.
func registerAdminRoutes(mux *http.ServeMux, h *admin.Handler, cmsH *routes.Handler, authMgr *auth.Manager) {
	// Public login + logout (rate-limited POST).
	loginRL := middleware.NewRateLimit(5, time.Minute)
	mux.Handle("/admin/login", middleware.CSRF(loginRL.Middleware(http.HandlerFunc(h.LoginPage))))
	mux.HandleFunc("/admin/logout", h.Logout)

	// Protected sub-mux.
	protected := http.NewServeMux()
	protected.HandleFunc("/admin/", func(w http.ResponseWriter, r *http.Request) {
		// Default landing page.
		if r.URL.Path == "/admin/" || r.URL.Path == "/admin" {
			http.Redirect(w, r, "/admin/dashboard", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	protected.HandleFunc("/admin/dashboard", h.Dashboard)
	protected.HandleFunc("/admin/bot", h.BotPage)
	protected.HandleFunc("/admin/bot/test", h.BotTest)
	protected.HandleFunc("/admin/kb", h.KBList)
	protected.HandleFunc("/admin/kb/new", h.KBNew)
	protected.HandleFunc("/admin/kb/edit", h.KBEdit)
	protected.HandleFunc("/admin/kb/update", h.KBUpdate)
	protected.HandleFunc("/admin/kb/delete", h.KBDelete)
	protected.HandleFunc("/admin/kb/toggle", h.KBToggle)
	protected.HandleFunc("/admin/visitors", h.VisitorsPage)
	protected.HandleFunc("/admin/settings", h.SettingsPage)
	protected.HandleFunc("/admin/settings/test-smtp", h.SettingsTestSMTP)

	// KB form posts (multipart) — same path as KBList so use one handler that
	// branches on method.
	// (KBList already handles GET /admin/kb; the form posts to /admin/kb)
	// To support both GET (list) and POST (create) on /admin/kb, override:
	protected.HandleFunc("/admin/kb_post", h.KBCreate)

	// Existing CMS file editor — preserved under /admin/files/*.
	protected.HandleFunc("/admin/files", cmsH.HandleFiles)
	protected.HandleFunc("/admin/files/", cmsH.HandleFiles)
	protected.HandleFunc("/admin/files/edit", cmsH.HandleEdit)
	protected.HandleFunc("/admin/files/save", cmsH.HandleSave)
	protected.HandleFunc("/admin/files/preview", cmsH.HandlePreview)
	protected.HandleFunc("/admin/files/ai", cmsH.HandleAI)
	protected.HandleFunc("/admin/files/ai/approve", cmsH.HandleAIApprove)

	// CSRF + Auth wrapping. Order: outer Auth (redirects to login), inner CSRF.
	mux.Handle("/admin/", middleware.Auth(authMgr, middleware.CSRF(adminPostRouter(h, protected))))
}

// adminPostRouter intercepts POST /admin/kb so we can route to KBCreate without
// clashing with the GET handler bound on the same path. ServeMux is path-only,
// so we branch by method here.
func adminPostRouter(h *admin.Handler, base http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/kb" && r.Method == http.MethodPost {
			h.KBCreate(w, r)
			return
		}
		base.ServeHTTP(w, r)
	})
}

// registerInternalAPI wires the voice-proxy facing endpoints. These are
// auth-by-token, not session-based.
func registerInternalAPI(mux *http.ServeMux, api *admin.API) {
	mux.HandleFunc("/api/bot/context", api.Context)
	mux.HandleFunc("/api/visitors/log", api.VisitorLog)
}
