package web

import (
	"net/http"

	"github.com/vdao/webinar_mtx/internal/auth"
	"github.com/vdao/webinar_mtx/internal/model"
	"github.com/vdao/webinar_mtx/internal/store"
	"github.com/vdao/webinar_mtx/internal/webutil"
)

// roomSession resolves the participant session for a room from its cookie.
func (s *Server) roomSession(r *http.Request, wb *model.Webinar) (*model.ParticipantSession, *model.Participant) {
	c, err := r.Cookie(auth.RoomCookieName(wb.Slug))
	if err != nil || c.Value == "" {
		return nil, nil
	}
	sess, p, err := s.Store.SessionByToken(r.Context(), c.Value)
	if err != nil {
		return nil, nil
	}
	if sess.WebinarID != wb.ID || p.Status != model.ParticipantActive {
		return nil, nil
	}
	return sess, p
}

// GET /w/{slug} — join form or the player page.
func (s *Server) roomPage(w http.ResponseWriter, r *http.Request) {
	wb, err := s.Store.WebinarBySlug(r.Context(), r.PathValue("slug"))
	if err == store.ErrNotFound {
		s.renderError(w, r, 404, "Вебинар не найден. Проверьте ссылку.")
		return
	}
	if err != nil {
		s.renderError(w, r, 500, "Ошибка базы данных")
		return
	}

	if _, p := s.roomSession(r, wb); p != nil {
		s.render(w, http.StatusOK, "room_watch.html", map[string]any{
			"Webinar":     wb,
			"Participant": p,
			"WhepURL":     "/whep/" + wb.Slug,
			"Email":       p.Email,
		})
		return
	}
	s.render(w, http.StatusOK, "room_join.html", map[string]any{
		"Webinar":  wb,
		"CodeMode": s.Cfg.AccessMode == "code",
		"Email":    r.URL.Query().Get("e"),
		"Stage":    r.URL.Query().Get("stage"),
		"Flash":    s.flashes(r),
	})
}

// POST /w/{slug}/enter — email check; sends a code or logs in directly.
func (s *Server) roomEnter(w http.ResponseWriter, r *http.Request) {
	wb, err := s.Store.WebinarBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.renderError(w, r, 404, "Вебинар не найден")
		return
	}
	email := webutil.NormalizeEmail(r.PostFormValue("email"))
	if !webutil.ValidEmail(email) {
		flashRedirect(w, r, "/w/"+wb.Slug, "invalid", true)
		return
	}
	ip := webutil.ClientIP(r)
	if !s.IPCodeLimiter.Allow(ip) || !s.CodeLimiter.Allow(ip+"|"+email) {
		flashRedirect(w, r, "/w/"+wb.Slug, "rate", true)
		return
	}

	p, err := s.Store.ParticipantByEmail(r.Context(), wb.ID, email)
	if err == store.ErrNotFound {
		// Do not leak whether the email is registered: generic message.
		flashRedirect(w, r, "/w/"+wb.Slug, "notallowed", true)
		return
	}
	if err != nil {
		flashRedirect(w, r, "/w/"+wb.Slug, "mailfail", true)
		return
	}
	if p.Status == model.ParticipantBlocked {
		flashRedirect(w, r, "/w/"+wb.Slug, "notallowed", true)
		return
	}

	if s.Cfg.AccessMode == "code" {
		code, err := s.Store.CreateLoginCode(r.Context(), wb.ID, email, s.Cfg.CodeTTL)
		if err != nil {
			flashRedirect(w, r, "/w/"+wb.Slug, "mailfail", true)
			return
		}
		if err := s.Mailer.SendLoginCode(email, code, wb.Title); err != nil {
			flashRedirect(w, r, "/w/"+wb.Slug, "mailfail", true)
			return
		}
		http.Redirect(w, r, "/w/"+wb.Slug+"?stage=code&e="+webutil.SafeQuery(email), http.StatusSeeOther)
		return
	}

	s.startSession(w, r, wb, p)
}

// POST /w/{slug}/verify — checks the one-time code and logs in.
func (s *Server) roomVerify(w http.ResponseWriter, r *http.Request) {
	wb, err := s.Store.WebinarBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.renderError(w, r, 404, "Вебинар не найден")
		return
	}
	email := webutil.NormalizeEmail(r.PostFormValue("email"))
	code := r.PostFormValue("code")
	if email == "" || code == "" {
		flashRedirect(w, r, "/w/"+wb.Slug, "invalid", true)
		return
	}
	if !s.CodeLimiter.Allow(webutil.ClientIP(r) + "|" + email) {
		flashRedirect(w, r, "/w/"+wb.Slug, "rate", true)
		return
	}
	if err := s.Store.ConsumeLoginCode(r.Context(), wb.ID, email, code); err != nil {
		http.Redirect(w, r, "/w/"+wb.Slug+"?stage=code&e="+webutil.SafeQuery(email)+"&err=wrongcode", http.StatusSeeOther)
		return
	}
	p, err := s.Store.ParticipantByEmail(r.Context(), wb.ID, email)
	if err != nil || p.Status == model.ParticipantBlocked {
		flashRedirect(w, r, "/w/"+wb.Slug, "notallowed", true)
		return
	}
	s.startSession(w, r, wb, p)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, wb *model.Webinar, p *model.Participant) {
	token, err := s.Store.CreateSession(r.Context(), p.ID, wb.ID, s.Cfg.SessionTTL)
	if err != nil {
		s.renderError(w, r, 500, "Не удалось создать сессию")
		return
	}
	auth.SetRoomCookie(w, r, wb.Slug, token, s.Cfg.SessionTTL)
	http.Redirect(w, r, "/w/"+wb.Slug, http.StatusSeeOther)
}

// POST /w/{slug}/exit — leave the room and drop the session.
func (s *Server) roomExit(w http.ResponseWriter, r *http.Request) {
	wb, err := s.Store.WebinarBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.renderError(w, r, 404, "Вебинар не найден")
		return
	}
	if c, err := r.Cookie(auth.RoomCookieName(wb.Slug)); err == nil && c.Value != "" {
		s.Store.DeleteSession(r.Context(), c.Value)
	}
	auth.ClearRoomCookie(w, wb.Slug)
	http.Redirect(w, r, "/w/"+wb.Slug, http.StatusSeeOther)
}
