package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/studio"
	"ytcartell/internal/version"
)

type Server struct {
	engine *studio.Engine
	store  *config.Store
	port   int
}

func main() {
	port := 5195
	if pStr := os.Getenv("PORT"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil {
			port = p
		}
	}

	store, err := config.NewStore()
	if err != nil {
		log.Printf("config error: %v", err)
	}

	engine, err := studio.NewEngine(store)
	if engine == nil {
		log.Fatalf("failed to initialize studio engine: %v", err)
	}

	srv := &Server{
		engine: engine,
		store:  store,
		port:   port,
	}

	mux := http.NewServeMux()

	// API Routes
	mux.HandleFunc("/api/status", srv.handleStatus)
	mux.HandleFunc("/api/settings", srv.handleSettings)
	mux.HandleFunc("/api/voices", srv.handleVoices)
	mux.HandleFunc("/api/projects", srv.handleProjects)
	mux.HandleFunc("/api/projects/create", srv.handleProjectCreate)
	mux.HandleFunc("/api/project/detail", srv.handleProjectDetail)
	mux.HandleFunc("/api/project/autopilot", srv.handleProjectAutopilot)
	mux.HandleFunc("/api/project/events", srv.handleProjectEvents)
	mux.HandleFunc("/api/project/video", srv.handleProjectVideo)
	mux.HandleFunc("/api/project/asset", srv.handleProjectAsset)

	// Deb download handler
	mux.HandleFunc("/yt-cartell-studio.deb", srv.handleDebDownload)

	// Web Static Files
	webDir := filepath.Join(getRootDir(), "web")
	fs := http.FileServer(http.Dir(webDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(webDir, filepath.Clean(r.URL.Path))
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		// Fallback to index.html for SPA routes
		http.ServeFile(w, r, filepath.Join(webDir, "index.html"))
	})

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	log.Printf("=== YT Cartell Studio Web Server v%s listening on http://%s ===", version.String(), addr)
	if err := http.ListenAndServe(addr, corsMiddleware(mux)); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func getRootDir() string {
	execPath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(execPath)
		if _, err := os.Stat(filepath.Join(dir, "web")); err == nil {
			return dir
		}
		// Check parent if in cmd/server
		parent := filepath.Dir(filepath.Dir(dir))
		if _, err := os.Stat(filepath.Join(parent, "web")); err == nil {
			return parent
		}
	}
	// Fallback to working directory or default /root/yt-cartell
	if _, err := os.Stat("/root/yt-cartell/web"); err == nil {
		return "/root/yt-cartell"
	}
	cwd, _ := os.Getwd()
	return cwd
}

func corsMiddleware(next http.Handler) http.Handler {
	allowedOrigins := map[string]bool{
		"https://yt-cartel.n8n-accaisona.site":  true,
		"https://yt-cartell.n8n-accaisona.site": true,
		"http://localhost:5195":                 true,
		"http://127.0.0.1:5195":                 true,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	googleKey := ""
	if len(cfg.Google.Keys) > 0 {
		googleKey = cfg.Google.Keys[0]
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": version.String(),
		"time":    time.Now().Format(time.RFC3339),
		"config": map[string]any{
			"googleConfigured": googleKey != "" || cfg.Google.OAuthToken != "",
			"piperVoice":       cfg.TTS.Voice,
			"stockProvider":    cfg.Stock.Provider,
			"pexelsActive":     cfg.Stock.PexelsKey != "",
			"pixabayActive":    cfg.Stock.PixabayKey != "",
		},
	})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := s.store.Get()
		googleKey := ""
		if len(cfg.Google.Keys) > 0 {
			googleKey = cfg.Google.Keys[0]
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"googleApiKey":  maskKey(googleKey),
			"pexelsKey":     maskKey(cfg.Stock.PexelsKey),
			"pixabayKey":    maskKey(cfg.Stock.PixabayKey),
			"stockProvider": cfg.Stock.Provider,
			"aspect":        "16:9",
			"voice":         cfg.TTS.Voice,
		})
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			GoogleApiKey  *string `json:"googleApiKey"`
			PexelsKey     *string `json:"pexelsKey"`
			PixabayKey    *string `json:"pixabayKey"`
			StockProvider *string `json:"stockProvider"`
			Voice         *string `json:"voice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}

		_ = s.store.Update(func(cf *config.Config) {
			if req.GoogleApiKey != nil && *req.GoogleApiKey != "" && !strings.Contains(*req.GoogleApiKey, "•••") {
				cf.Google.Keys = []string{strings.TrimSpace(*req.GoogleApiKey)}
			}
			if req.PexelsKey != nil && *req.PexelsKey != "" && !strings.Contains(*req.PexelsKey, "•••") {
				cf.Stock.PexelsKey = strings.TrimSpace(*req.PexelsKey)
			}
			if req.PixabayKey != nil && *req.PixabayKey != "" && !strings.Contains(*req.PixabayKey, "•••") {
				cf.Stock.PixabayKey = strings.TrimSpace(*req.PixabayKey)
			}
			if req.StockProvider != nil {
				cf.Stock.Provider = strings.TrimSpace(*req.StockProvider)
			}
			if req.Voice != nil {
				cf.TTS.Voice = strings.TrimSpace(*req.Voice)
			}
		})

		jsonResponse(w, http.StatusOK, map[string]any{"success": true})
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func maskKey(k string) string {
	if len(k) <= 8 {
		if len(k) > 0 {
			return "••••••••"
		}
		return ""
	}
	return k[:4] + "••••••••" + k[len(k)-4:]
}

func (s *Server) handleVoices(w http.ResponseWriter, r *http.Request) {
	voices := []map[string]any{
		{"id": "tetiana", "name": "Тетяна (Українська, жіночий)", "lang": "uk"},
		{"id": "mykyta", "name": "Микита (Українська, чоловічий)", "lang": "uk"},
		{"id": "lada", "name": "Лада (Українська, жіночий)", "lang": "uk"},
	}
	jsonResponse(w, http.StatusOK, voices)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	summaries := s.engine.List()
	jsonResponse(w, http.StatusOK, map[string]any{"projects": summaries})
}

func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Title     string `json:"title"`
		Text      string `json:"text"`
		Aspect    string `json:"aspect"`
		Duration  int    `json:"duration"`
		Mode      string `json:"mode"`      // "story" or "script"
		FitToText bool   `json:"fitToText"` // fit video duration to text
		Pipeline  string `json:"pipeline"`
		Language  string `json:"language"`
		Autopilot bool   `json:"autopilot"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": "invalid json: " + err.Error()})
		return
	}

	text := strings.TrimSpace(req.Text)
	if text == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": "Тема або текст історії не може бути порожнім"})
		return
	}

	aspect := req.Aspect
	if aspect == "" {
		aspect = "16:9"
	}

	pipeline := req.Pipeline
	if pipeline == "" {
		pipeline = "stock"
	}

	mode := studio.ModeStory
	if req.Mode == "script" || req.FitToText {
		mode = studio.ModeScript
	}

	duration := req.Duration
	if mode == studio.ModeStory {
		if duration <= 0 {
			duration = 60
		}
	} else {
		// In script mode (fit to text), if duration is not explicitly specified,
		// estimate it from word count (~2.5 words per second spoken pace)
		if duration <= 0 {
			wordCount := len(strings.Fields(text))
			duration = int(float64(wordCount) / 2.5)
			if duration < 10 {
				duration = 10
			}
		}
	}

	lang := req.Language
	if lang == "" {
		lang = "uk"
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		words := strings.Fields(text)
		if len(words) > 6 {
			title = strings.Join(words[:6], " ") + "…"
		} else {
			title = text
		}
	}

	opt := studio.NewProjectOptions{
		Title:         title,
		Text:          text,
		Mode:          mode,
		Aspect:        aspect,
		TargetSeconds: duration,
		Pipeline:      pipeline,
		PipelineSet:   true,
		Language:      lang,
		LanguageSet:   true,
		Autopilot:     req.Autopilot,
	}

	p, err := s.engine.CreateProject(opt)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]any{"error": "не вдалося створити проєкт: " + err.Error()})
		return
	}

	if req.Autopilot {
		go func(pid string) {
			_ = s.engine.Run(pid, studio.StageAutopilot, false, "")
		}(p.ID)
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"success": true,
		"id":      p.ID,
		"title":   p.Title,
	})
}

func (s *Server) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": "missing id"})
		return
	}

	p, ok := s.engine.Get(id)
	if !ok {
		jsonResponse(w, http.StatusNotFound, map[string]any{"error": "project not found"})
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"project": p,
	})
}

func (s *Server) handleProjectAutopilot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": "missing id"})
		return
	}

	_, ok := s.engine.Get(id)
	if !ok {
		jsonResponse(w, http.StatusNotFound, map[string]any{"error": "project not found"})
		return
	}

	go func(pid string) {
		_ = s.engine.Run(pid, studio.StageAutopilot, false, "")
	}(id)

	jsonResponse(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleProjectEvents(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			p, ok := s.engine.Get(id)
			if !ok {
				fmt.Fprintf(w, "event: error\ndata: {\"error\":\"not found\"}\n\n")
				flusher.Flush()
				return
			}

			job := p.Job
			var status, message, jobErr string
			var progress int
			var kind studio.JobKind

			if job != nil {
				status = string(job.Status)
				message = job.Message
				jobErr = job.Error
				progress = job.Progress
				kind = job.Kind
			}

			data, _ := json.Marshal(map[string]any{
				"status":        status,
				"kind":          kind,
				"progress":      progress,
				"message":       message,
				"error":         jobErr,
				"step":          p.Step,
				"sceneCount":    len(p.Scenes),
				"hasVideo":      p.Montage.Output.Status == studio.StatusDone,
				"videoPath":     p.Montage.Output.Path,
				"videoDuration": p.Montage.Output.Duration,
			})

			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

			if job != nil && (job.Status == studio.JobDone || job.Status == studio.JobError) {
				return
			}
		}
	}
}

func (s *Server) handleProjectVideo(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	p, ok := s.engine.Get(id)
	if !ok {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	if p.Montage.Output.Path == "" {
		http.Error(w, "video not ready yet", http.StatusNotFound)
		return
	}

	absPath := s.engine.Abs(id, p.Montage.Output.Path)
	if _, err := os.Stat(absPath); err != nil {
		http.Error(w, "video file not found on disk", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeFile(w, r, absPath)
}

func (s *Server) handleProjectAsset(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	rel := r.URL.Query().Get("rel")
	if id == "" || rel == "" {
		http.Error(w, "missing id or rel", http.StatusBadRequest)
		return
	}

	absPath := s.engine.Abs(id, rel)
	if _, err := os.Stat(absPath); err != nil {
		http.Error(w, "asset not found", http.StatusNotFound)
		return
	}

	http.ServeFile(w, r, absPath)
}

func (s *Server) handleDebDownload(w http.ResponseWriter, r *http.Request) {
	debPath := filepath.Join(getRootDir(), "yt-cartell-studio.deb")
	if _, err := os.Stat(debPath); err != nil {
		http.Error(w, "deb package not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.debian.binary-package")
	w.Header().Set("Content-Disposition", "attachment; filename=\"yt-cartell-studio.deb\"")
	http.ServeFile(w, r, debPath)
}
