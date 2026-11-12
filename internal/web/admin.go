package web

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/vdao/webinar_mtx/internal/auth"
	"github.com/vdao/webinar_mtx/internal/config"
	"github.com/vdao/webinar_mtx/internal/model"
	"github.com/vdao/webinar_mtx/internal/store"
	"github.com/vdao/webinar_mtx/internal/webutil"
)

func (s *Server) adminLoginPage(w http.ResponseWriter, r *http.Request) {
	if auth.IsAdmin(r, s.Cfg.AppSecret) {
		http.Redirect(w, r, "/admin/webinars", http.StatusFound)
		return
	}
	s.render(w, http.StatusOK, "admin_login.html", map[string]any{
		"Flash":  s.flashes(r),
		"OIDCOn": s.OIDC != nil,
	})
}

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	if s.OIDC != nil {
		// OIDC mode replaces password login entirely.
		flashRedirect(w, r, "/admin/login", "oidc-off", true)
		return
	}
	email := webutil.NormalizeEmail(r.PostFormValue("email"))
	pass := r.PostFormValue("password")
	if email != s.Cfg.AdminEmail ||
		bcrypt.CompareHashAndPassword(s.AdminHash, []byte(pass)) != nil {
		flashRedirect(w, r, "/admin/login", "badlogin", true)
		return
	}
	auth.SetAdminCookie(w, r, s.Cfg.AppSecret)
	http.Redirect(w, r, "/admin/webinars", http.StatusSeeOther)
}

func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request) {
	auth.ClearAdminCookie(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// flashes extracts ok/err codes from the query string.
func (s *Server) flashes(r *http.Request) map[string]string {
	out := map[string]string{}
	if ok := r.URL.Query().Get("ok"); ok != "" {
		if t, found := flashText[ok]; found {
			out["ok"] = t
		}
	}
	if errc := r.URL.Query().Get("err"); errc != "" {
		if t, found := flashText[errc]; found {
			out["err"] = t
		}
	}
	return out
}

func (s *Server) adminWebinars(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.Store.ListWebinars(ctx)
	if err != nil {
		s.renderError(w, r, 500, "Не удалось загрузить вебинары")
		return
	}
	counts, _ := s.Store.WebinarParticipantCounts(ctx)
	s.render(w, http.StatusOK, "admin_webinars.html", map[string]any{
		"Active":   "webinars",
		"Title":    "Вебинары",
		"Webinars": list,
		"Counts":   counts,
		"Flash":    s.flashes(r),
	})
}

func (s *Server) adminWebinarCreate(w http.ResponseWriter, r *http.Request) {
	wb := &model.Webinar{
		ID:            "wb_" + config.RandomHex(12),
		Title:         strings.TrimSpace(r.PostFormValue("title")),
		Status:        model.WebinarDraft,
		StreamKey:     config.RandomHex(16),
		RecordEnabled: true,
	}
	if wb.Title == "" {
		flashRedirect(w, r, "/admin/webinars", "invalid", true)
		return
	}
	wb.Slug = uniqueSlug(r.PostFormValue("slug"))
	if err := s.Store.CreateWebinar(r.Context(), wb); err != nil {
		flashRedirect(w, r, "/admin/webinars", "duplicate", true)
		return
	}
	s.syncRecordingConfig(r, wb)
	http.Redirect(w, r, "/admin/webinars/"+wb.ID+"?ok=created", http.StatusSeeOther)
}

// syncRecordingConfig keeps the MediaMTX path-level recording config in sync
// with the webinar's RecordEnabled flag (only when S3 publishing is on).
// Creating the config before the stream starts matters: patching a path while
// it is publishing can interrupt the stream.
func (s *Server) syncRecordingConfig(r *http.Request, wb *model.Webinar) {
	if s.Recorder == nil {
		return
	}
	name := "w/" + wb.StreamKey
	ctx := r.Context()
	if wb.RecordEnabled {
		if err := s.Mtx.EnsurePathRecord(ctx, name, "168h"); err != nil {
			log.Printf("sync recording config %s: %v", wb.Slug, err)
		}
		return
	}
	// Recording disabled: drop the path config while the stream is offline.
	info, err := s.Mtx.GetPath(ctx, name)
	if err == nil && info != nil && info.Ready {
		return // stream is live; the worker will not re-assert a disabled flag anyway
	}
	_ = s.Mtx.DeletePathConfig(ctx, name)
}

// uniqueSlug normalizes an admin-provided slug or generates a random one.
func uniqueSlug(raw string) string {
	var b strings.Builder
	prevDash := true
	for _, ch := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9':
			b.WriteByte(byte(ch))
			prevDash = false
		case !prevDash && (ch == '-' || ch == '_' || ch == ' '):
			b.WriteByte('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) < 3 {
		slug = config.RandomHex(5) // 10 random chars
	}
	if len(slug) > 64 {
		slug = slug[:64]
	}
	return slug
}

func (s *Server) loadWebinar(w http.ResponseWriter, r *http.Request) *model.Webinar {
	wb, err := s.Store.WebinarByID(r.Context(), r.PathValue("id"))
	if err == store.ErrNotFound {
		s.renderError(w, r, 404, "Вебинар не найден")
		return nil
	}
	if err != nil {
		s.renderError(w, r, 500, "Ошибка базы данных")
		return nil
	}
	return wb
}

func (s *Server) adminWebinarEdit(w http.ResponseWriter, r *http.Request) {
	wb := s.loadWebinar(w, r)
	if wb == nil {
		return
	}
	total, _ := s.Store.CountParticipants(r.Context(), wb.ID)
	s.render(w, http.StatusOK, "admin_webinar_edit.html", map[string]any{
		"Active":        "webinars",
		"Title":         wb.Title,
		"Webinar":       wb,
		"Statuses":      model.WebinarStatuses,
		"Total":         total,
		"RoomURL":       s.roomURL(wb),
		"RTMPURL":       s.Cfg.RTMPURL,
		"S3Configured":  s.Cfg.S3.Configured(),
		"RecordingURLs": wb.RecordingURLList(),
		"Flash":         s.flashes(r),
	})
}

func (s *Server) adminWebinarUpdate(w http.ResponseWriter, r *http.Request) {
	wb := s.loadWebinar(w, r)
	if wb == nil {
		return
	}
	title := strings.TrimSpace(r.PostFormValue("title"))
	if title == "" {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID, "invalid", true)
		return
	}
	status := r.PostFormValue("status")
	valid := false
	for _, st := range model.WebinarStatuses {
		if st == status {
			valid = true
		}
	}
	if !valid {
		status = wb.Status
	}
	var sched *time.Time
	if v := strings.TrimSpace(r.PostFormValue("scheduled_at")); v != "" {
		if t, err := time.ParseInLocation("2006-01-02T15:04", v, time.Local); err == nil {
			sched = &t
		}
	}
	slug := wb.Slug
	if v := strings.TrimSpace(r.PostFormValue("slug")); v != "" {
		slug = uniqueSlug(v)
	}
	wb.Title, wb.Description, wb.Status, wb.Slug, wb.ScheduledAt =
		title, strings.TrimSpace(r.PostFormValue("description")), status, slug, sched
	// The checkbox is rendered only when S3 is configured; without the field
	// keep the stored value.
	if r.PostForm.Has("record_enabled") {
		wb.RecordEnabled = r.PostFormValue("record_enabled") == "on"
	}
	if err := s.Store.UpdateWebinar(r.Context(), wb); err != nil {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID, "duplicate", true)
		return
	}
	s.syncRecordingConfig(r, wb)
	flashRedirect(w, r, "/admin/webinars/"+wb.ID, "saved", false)
}

// adminWebinarPublishRecord asks the recorder worker to publish the recording
// right away (admin button).
func (s *Server) adminWebinarPublishRecord(w http.ResponseWriter, r *http.Request) {
	wb := s.loadWebinar(w, r)
	if wb == nil {
		return
	}
	if s.Recorder == nil {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID, "notfound", true)
		return
	}
	info, err := s.Mtx.GetPath(r.Context(), "w/"+wb.StreamKey)
	if err == nil && info != nil && info.Ready {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID, "record-busy", true)
		return
	}
	if err := s.Recorder.RequestPublish(wb.ID); err != nil {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID, "record-none", true)
		return
	}
	flashRedirect(w, r, "/admin/webinars/"+wb.ID, "record-queued", false)
}

func (s *Server) adminWebinarDelete(w http.ResponseWriter, r *http.Request) {
	wb := s.loadWebinar(w, r)
	if wb == nil {
		return
	}
	if err := s.Store.DeleteWebinar(r.Context(), wb.ID); err != nil {
		flashRedirect(w, r, "/admin/webinars", "notfound", true)
		return
	}
	// Best-effort: drop the runtime recording config of the path too.
	_ = s.Mtx.DeletePathConfig(r.Context(), "w/"+wb.StreamKey)
	flashRedirect(w, r, "/admin/webinars", "deleted", false)
}

func (s *Server) adminWebinarRegenKey(w http.ResponseWriter, r *http.Request) {
	wb := s.loadWebinar(w, r)
	if wb == nil {
		return
	}
	if err := s.Store.RegenStreamKey(r.Context(), wb.ID, config.RandomHex(16)); err != nil {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID, "notfound", true)
		return
	}
	flashRedirect(w, r, "/admin/webinars/"+wb.ID, "key-regen", false)
}

// adminWebinarViewers returns live reader stats from MediaMTX as JSON.
func (s *Server) adminWebinarViewers(w http.ResponseWriter, r *http.Request) {
	wb := s.loadWebinar(w, r)
	if wb == nil {
		return
	}
	info, err := s.Mtx.GetPath(r.Context(), "w/"+wb.StreamKey)
	if err != nil || info == nil {
		writeJSON(w, 200, map[string]any{"online": false, "viewers": 0})
		return
	}
	writeJSON(w, 200, map[string]any{
		"online":  info.Ready && info.Source.Type != "",
		"viewers": info.Viewers(),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprint(w, mustJSON(v))
}
