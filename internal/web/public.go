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

// GET /w/{slug} — the room: a personal link (?c=CODE) opens the player,
// otherwise a "personal link required" page (or the player when a valid
// session cookie already exists).
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

	// Already logged in → straight to the player.
	if _, p := s.roomSession(r, wb); p != nil {
		s.renderWatch(w, r, wb, p)
		return
	}

	code := r.URL.Query().Get("c")
	if code == "" {
		s.render(w, http.StatusOK, "room_join.html", map[string]any{
			"Webinar": wb,
		})
		return
	}

	// A personal link: validate the code with per-IP rate limiting.
	if !s.IPCodeLimiter.Allow(webutil.ClientIP(r)) {
		s.renderError(w, r, http.StatusTooManyRequests, "Слишком много попыток входа. Попробуйте позже.")
		return
	}
	p, err := s.Store.ParticipantByCode(r.Context(), wb.ID, code)
	if err == store.ErrNotFound {
		s.renderError(w, r, http.StatusForbidden,
			"Ссылка недействительна. Проверьте, что вы открыли именно вашу персональную ссылку.")
		return
	}
	if err != nil {
		s.renderError(w, r, 500, "Ошибка базы данных")
		return
	}
	if p.Status == model.ParticipantBlocked {
		s.renderError(w, r, http.StatusForbidden, "Доступ к вебинару закрыт.")
		return
	}

	// Issue the session and show the player with a clean URL.
	token, err := s.Store.CreateSession(r.Context(), p.ID, wb.ID, s.Cfg.SessionTTL)
	if err != nil {
		s.renderError(w, r, 500, "Не удалось создать сессию")
		return
	}
	auth.SetRoomCookie(w, r, wb.Slug, token, s.Cfg.SessionTTL)
	http.Redirect(w, r, "/w/"+wb.Slug, http.StatusSeeOther)
}

// renderWatch draws the player page for an authenticated participant.
func (s *Server) renderWatch(w http.ResponseWriter, r *http.Request, wb *model.Webinar, p *model.Participant) {
	s.render(w, http.StatusOK, "room_watch.html", map[string]any{
		"Webinar":       wb,
		"Participant":   p,
		"WhepURL":       "/whep/" + wb.Slug,
		"Email":         p.Email,
		"RecordingOn":   wb.RecordingStatus == model.RecordingPublished,
		"RecordingURLs": wb.RecordingURLList(),
	})
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
