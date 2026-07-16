package webapp

import (
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"fahriddin-ai/internal/billz"
	"fahriddin-ai/internal/summary"
	"fahriddin-ai/internal/tasks"
)

//go:embed web
var webFS embed.FS

// Server serves the Mini App's static frontend and JSON API.
type Server struct {
	tasks    *tasks.Store
	summary  *summary.Builder
	billz    *billz.Client // optional: nil disables the dashboard's business section
	db       *sql.DB
	loc      *time.Location
	botToken string
	ownerIDs []int64
	log      *slog.Logger
}

func NewServer(taskStore *tasks.Store, summaryBuilder *summary.Builder, billzClient *billz.Client, db *sql.DB, loc *time.Location, botToken string, ownerIDs []int64, log *slog.Logger) *Server {
	return &Server{
		tasks: taskStore, summary: summaryBuilder, billz: billzClient, db: db, loc: loc,
		botToken: botToken, ownerIDs: ownerIDs, log: log,
	}
}

func (s *Server) isOwner(id int64) bool {
	for _, ownerID := range s.ownerIDs {
		if ownerID == id {
			return true
		}
	}
	return false
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	mux.HandleFunc("GET /api/dashboard", s.withAnyAuth(s.handleDashboard))
	mux.HandleFunc("POST /api/dashboard/refresh", s.withAnyAuth(s.handleDashboardRefresh))
	mux.HandleFunc("GET /api/tasks", s.withAnyAuth(s.handleListTasks))
	mux.HandleFunc("POST /api/tasks", s.withAnyAuth(s.handleAddTask))
	mux.HandleFunc("POST /api/tasks/{id}/complete", s.withAnyAuth(s.handleCompleteTask))
	mux.HandleFunc("GET /api/analytics/monthly", s.withAnyAuth(s.handleAnalyticsMonthly))
	mux.HandleFunc("GET /api/analytics/store/{id}", s.withAnyAuth(s.handleStoreDetail))
	mux.HandleFunc("GET /api/analytics/vmi", s.withAnyAuth(s.handleVMI))
	mux.HandleFunc("GET /api/analytics/categories", s.withAnyAuth(s.handleAnalyticsCategories))
	mux.HandleFunc("GET /api/analytics/forecast", s.withAnyAuth(s.handleAnalyticsForecast))
	mux.HandleFunc("GET /api/analytics/payments", s.withAnyAuth(s.handleAnalyticsPayments))
	mux.HandleFunc("GET /api/employees", s.withAnyAuth(s.handleEmployees))
	mux.HandleFunc("GET /api/me", s.withAnyAuth(s.handleMe))

	static, err := fs.Sub(webFS, "web")
	if err != nil {
		s.log.Error("webapp: embedded fs error", "err", err)
	}
	mux.Handle("/", http.FileServer(http.FS(static)))

	return mux
}

// withAnyAuth accepts either a valid Telegram Mini App initData (the
// original path — "Authorization: tma <initData>", identifying the bot
// owner) or a valid browser-login session cookie. Both paths hit the same
// handlers, so the Mini App and the browser platform share one API.
func (s *Server) withAnyAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "tma ") {
			initData := strings.TrimPrefix(authHeader, "tma ")
			if initData != "" {
				userID, err := ValidateInitData(initData, s.botToken)
				if err == nil && s.isOwner(userID) {
					next(w, r)
					return
				}
			}
		}

		if cookie, err := r.Cookie(SessionCookieName); err == nil {
			if _, err := ValidateSession(r.Context(), s.db, cookie.Value); err == nil {
				next(w, r)
				return
			}
		}

		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

// handleLoginPage serves the standalone browser login form — plain HTML/CSS,
// no Telegram WebApp JS SDK, since this path is for opening the platform
// directly in a browser rather than inside Telegram.
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	data, err := webFS.ReadFile("web/login.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Password == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	token, _, err := Login(r.Context(), s.db, req.Username, req.Password)
	if err != nil {
		s.log.Warn("webapp: login failed", "username", req.Username, "err", err)
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		_ = DeleteSession(r.Context(), s.db, cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleMe returns the current browser-session user's display name — used
// by the sidebar profile when there's no Telegram user object to read from
// (i.e. the platform is open directly in a browser, not inside Telegram).
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		if user, err := ValidateSession(r.Context(), s.db, cookie.Value); err == nil {
			writeJSON(w, http.StatusOK, map[string]string{
				"display_name": user.DisplayName,
				"username":     user.Username,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
