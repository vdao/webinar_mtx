package web

import (
	"io"
	"net/http"
	"strings"

	"github.com/vdao/webinar_mtx/internal/model"
	"github.com/vdao/webinar_mtx/internal/webutil"
)

// POST /whep/{slug} — WHEP signaling proxy.
//
// Authenticates the viewer session (cookie, same-origin), then forwards the
// SDP offer to MediaMTX, appending the session token as ?token= so the
// MediaMTX external-auth endpoint can validate reads too. Only the tiny SDP
// exchange passes through the app; audio/video flows browser ↔ MediaMTX
// directly over WebRTC.
func (s *Server) whepProxy(w http.ResponseWriter, r *http.Request) {
	wb, err := s.Store.WebinarBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "room not found"})
		return
	}
	c, err := r.Cookie("wm_room_" + wb.Slug)
	if err != nil || c.Value == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no session"})
		return
	}
	sess, p, err := s.Store.SessionByToken(r.Context(), c.Value)
	if err != nil || sess.WebinarID != wb.ID || p.Status != model.ParticipantActive {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid session"})
		return
	}
	if wb.Status == model.WebinarEnded {
		writeJSON(w, http.StatusGone, map[string]string{"error": "webinar ended"})
		return
	}

	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "application/sdp") && r.ContentLength > 0 {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "expected application/sdp"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}

	// The session token travels to MediaMTX in the query string; its external
	// auth request will contain it and our /internal/mtx/auth endpoint checks it.
	// MediaMTX expects the WHEP resource at /{path}/whep.
	upstream := s.Cfg.MtxWHEPURL + "/w/" + wb.StreamKey + "/whep?token=" + webutil.SafeQuery(c.Value)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstream, strings.NewReader(string(body)))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "proxy error"})
		return
	}
	req.Header.Set("Content-Type", "application/sdp")
	req.Header.Set("Accept", "application/sdp")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "stream server unreachable"})
		return
	}
	defer resp.Body.Close()

	s.Store.TouchParticipant(r.Context(), p.ID)

	w.Header().Set("Cache-Control", "no-store")
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}

// mtxAuthRequest mirrors the MediaMTX external authentication JSON payload.
type mtxAuthRequest struct {
	IP        string `json:"ip"`
	User      string `json:"user"`
	Password  string `json:"password"`
	Token     string `json:"token"`
	Action    string `json:"action"`   // publish | read | playback | api | metrics | pprof
	Path      string `json:"path"`     // "w/{streamKey}"
	Protocol  string `json:"protocol"` // rtmp | srt | webrtc | rtsp | hls | moq
	ID        any    `json:"id"`
	Query     string `json:"query"`
	UserAgent string `json:"userAgent"`
}

// POST /internal/mtx/auth — MediaMTX external authentication endpoint.
//
// publish: allowed only for a valid stream key of a non-ended webinar;
// read: allowed only with a valid viewer session token for that exact webinar.
func (s *Server) mtxAuth(w http.ResponseWriter, r *http.Request) {
	var req mtxAuthRequest
	if err := jsonDecode(r, &req); err != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	key, ok := strings.CutPrefix(req.Path, "w/")
	if !ok || key == "" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	wb, err := s.Store.WebinarByStreamKey(r.Context(), key)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	switch req.Action {
	case "publish":
		if wb.Status == model.WebinarEnded {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)

	case "read":
		token := tokenFromQuery(req.Query)
		if token == "" {
			token = req.Token
		}
		sess, p, err := s.Store.SessionByToken(r.Context(), token)
		if err != nil || sess.WebinarID != wb.ID || p.Status != model.ParticipantActive {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)

	default: // api/metrics/playback/pprof — not used, deny
		w.WriteHeader(http.StatusForbidden)
	}
}

// tokenFromQuery extracts token=... from a raw query string.
func tokenFromQuery(raw string) string {
	for _, pair := range strings.Split(raw, "&") {
		k, v, found := strings.Cut(pair, "=")
		if found && k == "token" {
			return v
		}
	}
	return ""
}

func jsonDecode(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return err
	}
	return jsonUnmarshal(body, v)
}
