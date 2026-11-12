package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/vdao/webinar_mtx/internal/csvutil"
	"github.com/vdao/webinar_mtx/internal/model"
	"github.com/vdao/webinar_mtx/internal/webutil"
)

func (s *Server) adminParticipants(w http.ResponseWriter, r *http.Request) {
	wb := s.loadWebinar(w, r)
	if wb == nil {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	list, err := s.Store.ListParticipants(r.Context(), wb.ID, q, 500)
	if err != nil {
		s.renderError(w, r, 500, "Не удалось загрузить участников")
		return
	}
	total, _ := s.Store.CountParticipants(r.Context(), wb.ID)

	// import report (?added=&dups=&bad= set right after an import)
	var importStats map[string]string
	if r.URL.Query().Has("added") {
		importStats = map[string]string{
			"added": r.URL.Query().Get("added"),
			"dups":  r.URL.Query().Get("dups"),
			"bad":   r.URL.Query().Get("bad"),
		}
	}
	s.render(w, http.StatusOK, "admin_participants.html", map[string]any{
		"Active":       "webinars",
		"Title":        "Участники — " + wb.Title,
		"Webinar":      wb,
		"AppURL":       s.Cfg.AppURL,
		"Participants": list,
		"Query":        q,
		"Total":        total,
		"ImportStats":  importStats,
		"Flash":        s.flashes(r),
	})
}

func (s *Server) adminParticipantAdd(w http.ResponseWriter, r *http.Request) {
	wb := s.loadWebinar(w, r)
	if wb == nil {
		return
	}
	email := webutil.NormalizeEmail(r.PostFormValue("email"))
	name := strings.TrimSpace(r.PostFormValue("name"))
	if !webutil.ValidEmail(email) {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID+"/participants", "invalid", true)
		return
	}
	p, created, err := s.Store.AddParticipant(r.Context(), wb.ID, email, name, model.SourceManual)
	if err != nil {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID+"/participants", "notfound", true)
		return
	}
	if created {
		s.Notify.NotifyRegistered(wb, p, s.participantLink(wb, p.Code))
	}
	code := "saved"
	if !created {
		code = "duplicate"
	}
	flashRedirect(w, r, "/admin/webinars/"+wb.ID+"/participants", code, !created)
}

func (s *Server) adminParticipantsImport(w http.ResponseWriter, r *http.Request) {
	wb := s.loadWebinar(w, r)
	if wb == nil {
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID+"/participants", "invalid", true)
		return
	}
	defer file.Close()

	res := csvutil.Parse(file)
	inserted, err := s.Store.ImportParticipants(r.Context(), wb.ID, res.Emails, res.Names)
	if err != nil {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID+"/participants", "notfound", true)
		return
	}
	for _, p := range inserted {
		s.Notify.NotifyRegistered(wb, p, s.participantLink(wb, p.Code))
	}
	added := len(inserted)

	to := "/admin/webinars/" + wb.ID + "/participants?ok=imported"
	to += "&added=" + itoa(added) +
		"&dups=" + itoa(len(res.Emails)-added+res.Duplicates) +
		"&bad=" + itoa(len(res.Skipped))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// adminParticipantsExport streams a CSV of email + personal link pairs,
// ready for a CRM mailing.
func (s *Server) adminParticipantsExport(w http.ResponseWriter, r *http.Request) {
	wb := s.loadWebinar(w, r)
	if wb == nil {
		return
	}
	list, err := s.Store.ListParticipants(r.Context(), wb.ID, "", 100000)
	if err != nil {
		s.renderError(w, r, 500, "Ошибка экспорта")
		return
	}
	var b strings.Builder
	b.WriteString("email,link\n")
	for _, p := range list {
		link := s.participantLink(wb, p.Code)
		if p.Status == model.ParticipantBlocked {
			continue // blocked viewers must not receive working links
		}
		b.WriteString(p.Email + "," + link + "\n")
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=links_"+wb.Slug+".csv")
	w.Write([]byte("\xEF\xBB\xBF" + b.String())) // BOM so Excel opens UTF-8
}

func (s *Server) loadParticipant(w http.ResponseWriter, r *http.Request) (*model.Participant, *model.Webinar) {
	p, err := s.Store.ParticipantByID(r.Context(), r.PathValue("pid"))
	if err != nil {
		s.renderError(w, r, 404, "Участник не найден")
		return nil, nil
	}
	wb, err := s.Store.WebinarByID(r.Context(), p.WebinarID)
	if err != nil {
		s.renderError(w, r, 404, "Вебинар не найден")
		return nil, nil
	}
	return p, wb
}

func (s *Server) adminParticipantStatus(w http.ResponseWriter, r *http.Request) {
	p, wb := s.loadParticipant(w, r)
	if p == nil {
		return
	}
	status := model.ParticipantActive
	if r.PostFormValue("status") == model.ParticipantBlocked {
		status = model.ParticipantBlocked
		s.Store.DeleteSessionsForParticipant(r.Context(), p.ID) // kick blocked viewer
	}
	if err := s.Store.SetParticipantStatus(r.Context(), p.ID, status); err != nil {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID+"/participants", "notfound", true)
		return
	}
	flashRedirect(w, r, "/admin/webinars/"+wb.ID+"/participants", "saved", false)
}

func (s *Server) adminParticipantDelete(w http.ResponseWriter, r *http.Request) {
	p, wb := s.loadParticipant(w, r)
	if p == nil {
		return
	}
	if err := s.Store.DeleteParticipant(r.Context(), p.ID); err != nil {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID+"/participants", "notfound", true)
		return
	}
	flashRedirect(w, r, "/admin/webinars/"+wb.ID+"/participants", "deleted", false)
}

// adminParticipantRegenCode issues a new personal code (the old link dies).
func (s *Server) adminParticipantRegenCode(w http.ResponseWriter, r *http.Request) {
	p, wb := s.loadParticipant(w, r)
	if p == nil {
		return
	}
	if _, err := s.Store.RegenParticipantCode(r.Context(), p.ID); err != nil {
		flashRedirect(w, r, "/admin/webinars/"+wb.ID+"/participants", "notfound", true)
		return
	}
	flashRedirect(w, r, "/admin/webinars/"+wb.ID+"/participants", "code-regen", false)
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
