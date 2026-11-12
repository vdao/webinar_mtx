// Package web implements all HTTP handlers: public rooms, the admin panel,
// payment webhooks, the WHEP proxy and the MediaMTX auth endpoint.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/vdao/webinar_mtx/internal/auth"
	"github.com/vdao/webinar_mtx/internal/config"
	"github.com/vdao/webinar_mtx/internal/mail"
	"github.com/vdao/webinar_mtx/internal/model"
	"github.com/vdao/webinar_mtx/internal/mtx"
	"github.com/vdao/webinar_mtx/internal/store"
)

//go:embed templates/*.html static/*
var contentFS embed.FS

// Server holds shared application state for all handlers.
type Server struct {
	Cfg           *config.Config
	Store         *store.Store
	Mailer        *mail.Sender
	Mtx           *mtx.Client
	Tpl           *template.Template
	AdminHash     []byte
	CodeLimiter   *auth.Limiter
	IPCodeLimiter *auth.Limiter
}

// New builds the server and parses templates.
func New(cfg *config.Config, st *store.Store, mailer *mail.Sender, mtxClient *mtx.Client) (*Server, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	s := &Server{
		Cfg:           cfg,
		Store:         st,
		Mailer:        mailer,
		Mtx:           mtxClient,
		AdminHash:     hash,
		CodeLimiter:   auth.NewLimiter(5, 10*time.Minute), // per ip+email
		IPCodeLimiter: auth.NewLimiter(30, time.Hour),     // per ip
	}
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) parseTemplates() error {
	funcs := template.FuncMap{
		"fmttime": func(t *time.Time) string {
			if t == nil {
				return "—"
			}
			return t.Local().Format("02.01.2006 15:04")
		},
		"statuslabel": func(st string) string {
			switch st {
			case model.WebinarDraft:
				return "Черновик"
			case model.WebinarScheduled:
				return "Запланирован"
			case model.WebinarLive:
				return "В эфире"
			case model.WebinarEnded:
				return "Завершён"
			}
			return st
		},
		"sourcelabel": func(src string) string {
			switch src {
			case model.SourceImport:
				return "Импорт"
			case model.SourceManual:
				return "Вручную"
			case model.SourceWebhook:
				return "Оплата"
			}
			return src
		},
		"join": strings.Join,
	}
	t, err := template.New("").Funcs(funcs).ParseFS(contentFS, "templates/*.html")
	if err != nil {
		return fmt.Errorf("parse templates: %w", err)
	}
	s.Tpl = t
	return nil
}

// Handler builds the routing table with middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.Handle("GET /static/", http.FileServerFS(contentFS))

	// public room
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusFound)
	})
	mux.HandleFunc("GET /w/{slug}", s.roomPage)
	mux.HandleFunc("POST /w/{slug}/enter", s.roomEnter)
	mux.HandleFunc("POST /w/{slug}/verify", s.roomVerify)
	mux.HandleFunc("POST /w/{slug}/exit", s.roomExit)
	mux.HandleFunc("POST /whep/{slug}", s.whepProxy)

	// payment webhooks + mediamtx auth (no session required)
	mux.HandleFunc("POST /hooks/{id}", s.webhookReceive)
	mux.HandleFunc("POST /internal/mtx/auth", s.mtxAuth)

	// admin
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/webinars", http.StatusFound)
	})
	mux.HandleFunc("GET /admin/login", s.adminLoginPage)
	mux.HandleFunc("POST /admin/login", s.adminLogin)
	mux.HandleFunc("POST /admin/logout", s.adminLogout)

	mux.HandleFunc("GET /admin/webinars", s.requireAdmin(s.adminWebinars))
	mux.HandleFunc("POST /admin/webinars", s.requireAdmin(s.adminWebinarCreate))
	mux.HandleFunc("GET /admin/webinars/{id}", s.requireAdmin(s.adminWebinarEdit))
	mux.HandleFunc("POST /admin/webinars/{id}", s.requireAdmin(s.adminWebinarUpdate))
	mux.HandleFunc("POST /admin/webinars/{id}/delete", s.requireAdmin(s.adminWebinarDelete))
	mux.HandleFunc("POST /admin/webinars/{id}/key", s.requireAdmin(s.adminWebinarRegenKey))
	mux.HandleFunc("GET /admin/webinars/{id}/viewers", s.requireAdmin(s.adminWebinarViewers))

	mux.HandleFunc("GET /admin/webinars/{id}/participants", s.requireAdmin(s.adminParticipants))
	mux.HandleFunc("POST /admin/webinars/{id}/participants", s.requireAdmin(s.adminParticipantAdd))
	mux.HandleFunc("POST /admin/webinars/{id}/participants/import", s.requireAdmin(s.adminParticipantsImport))
	mux.HandleFunc("GET /admin/webinars/{id}/participants/export", s.requireAdmin(s.adminParticipantsExport))
	mux.HandleFunc("POST /admin/participants/{pid}/status", s.requireAdmin(s.adminParticipantStatus))
	mux.HandleFunc("POST /admin/participants/{pid}/delete", s.requireAdmin(s.adminParticipantDelete))

	mux.HandleFunc("GET /admin/webhooks", s.requireAdmin(s.adminWebhooks))
	mux.HandleFunc("POST /admin/webhooks", s.requireAdmin(s.adminWebhookCreate))
	mux.HandleFunc("GET /admin/webhooks/{id}", s.requireAdmin(s.adminWebhookEdit))
	mux.HandleFunc("POST /admin/webhooks/{id}", s.requireAdmin(s.adminWebhookUpdate))
	mux.HandleFunc("POST /admin/webhooks/{id}/delete", s.requireAdmin(s.adminWebhookDelete))
	mux.HandleFunc("POST /admin/webhooks/{id}/secret", s.requireAdmin(s.adminWebhookRegenSecret))
	mux.HandleFunc("POST /admin/webhooks/{id}/test", s.requireAdmin(s.adminWebhookTest))

	return logRequests(mux)
}

// requireAdmin wraps admin-only handlers.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth.IsAdmin(r, s.Cfg.AppSecret) {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/healthz" { // keep logs quiet
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}

// flash maps flash codes from query params to human messages.
var flashText = map[string]string{
	"saved":        "Сохранено",
	"deleted":      "Удалено",
	"created":      "Создано",
	"imported":     "Импорт завершён",
	"key-regen":    "Новый ключ сгенерирован. Обновите настройки в OBS/кодировщике!",
	"secret-regen": "Новый секрет сгенерирован. Обновите его в платёжной системе!",
	"badlogin":     "Неверный email или пароль",
	"notfound":     "Не найдено",
	"duplicate":    "Такой slug или email уже существует",
	"wrongcode":    "Неверный или истёкший код",
	"notallowed":   "Доступ запрещён: email не зарегистрирован или заблокирован",
	"rate":         "Слишком часто. Попробуйте позже",
	"mailfail":     "Не удалось отправить письмо. Попробуйте позже",
	"invalid":      "Проверьте правильность заполнения полей",
}

// render executes a template with common view fields.
func (s *Server) render(w http.ResponseWriter, status int, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.Tpl.ExecuteTemplate(w, page, data); err != nil {
		log.Printf("render %s: %v", page, err)
	}
}

// renderError shows a friendly error page.
func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.render(w, status, "error.html", map[string]any{
		"Status":  status,
		"Text":    msg,
		"IsAdmin": auth.IsAdmin(r, s.Cfg.AppSecret),
	})
}

// flashRedirect redirects adding an ok/err flash code.
func flashRedirect(w http.ResponseWriter, r *http.Request, to, code string, isErr bool) {
	q := "?ok=" + code
	if isErr {
		q = "?err=" + code
	}
	http.Redirect(w, r, to+q, http.StatusSeeOther)
}

// roomURL builds the public room link.
func (s *Server) roomURL(wb *model.Webinar) string {
	return s.Cfg.AppURL + "/w/" + wb.Slug
}
