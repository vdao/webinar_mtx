package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/vdao/webinar_mtx/internal/model"
)

// ParticipantNotification is the payload of the outgoing registration webhook.
type ParticipantNotification struct {
	Event        string `json:"event"` // participant.registered
	WebinarID    string `json:"webinar_id"`
	WebinarSlug  string `json:"webinar_slug"`
	WebinarTitle string `json:"webinar_title"`
	Email        string `json:"email"`
	Name         string `json:"name,omitempty"`
	Code         string `json:"code"`
	Link         string `json:"link"` // personal room link with the code
	Source       string `json:"source"`
}

// Notifier delivers registration events to NOTIFY_WEBHOOK_URL asynchronously:
// registration requests (manual add, CSV import, payment webhook) never wait
// for or fail because of the delivery.
type Notifier struct {
	URL    string
	Secret string
	Client *http.Client
	queue  chan ParticipantNotification
}

// NewNotifier returns nil when notifications are not configured.
func NewNotifier(url, secret string) *Notifier {
	if url == "" {
		return nil
	}
	return &Notifier{
		URL:    url,
		Secret: secret,
		Client: &http.Client{Timeout: 10 * time.Second},
		queue:  make(chan ParticipantNotification, 4096),
	}
}

// NotifyRegistered enqueues a registration event; overflow drops the event
// with a log line (registration itself must always succeed).
func (n *Notifier) NotifyRegistered(wb *model.Webinar, p *model.Participant, link string) {
	if n == nil {
		return
	}
	event := ParticipantNotification{
		Event:        "participant.registered",
		WebinarID:    wb.ID,
		WebinarSlug:  wb.Slug,
		WebinarTitle: wb.Title,
		Email:        p.Email,
		Name:         p.Name,
		Code:         p.Code,
		Link:         link,
		Source:       p.Source,
	}
	select {
	case n.queue <- event:
	default:
		log.Printf("notify: queue overflow, dropping event for %s", p.Email)
	}
}

// Run drains the queue until ctx is cancelled. Each delivery is retried
// twice with a short backoff.
func (n *Notifier) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-n.queue:
			n.deliver(ctx, ev)
		}
	}
}

func (n *Notifier) deliver(ctx context.Context, ev ParticipantNotification) {
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
		if err != nil {
			log.Printf("notify: bad request: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Webhook-Event", ev.Event)
		if n.Secret != "" {
			mac := hmac.New(sha256.New, []byte(n.Secret))
			mac.Write(body)
			req.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}
		resp, err := n.Client.Do(req)
		if err != nil {
			log.Printf("notify: attempt %d: %v", attempt+1, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return
		}
		log.Printf("notify: attempt %d: status %s", attempt+1, resp.Status)
	}
	log.Printf("notify: giving up on %s %s", ev.Event, ev.Email)
}

// participantLink builds the personal room link for a participant.
func (s *Server) participantLink(wb *model.Webinar, code string) string {
	return s.Cfg.AppURL + "/w/" + wb.Slug + "?c=" + code
}
