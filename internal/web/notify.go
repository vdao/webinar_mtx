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
	"strings"
	"time"

	"github.com/vdao/webinar_mtx/internal/config"
	"github.com/vdao/webinar_mtx/internal/model"
)

// ParticipantNotification is the payload of the participant.registered event.
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

// RecordingNotification is the payload of the recording.published event,
// sent once per active participant when a webinar recording goes public.
// The recipient is responsible for templating and delivering the message.
type RecordingNotification struct {
	Event         string   `json:"event"` // recording.published
	WebinarID     string   `json:"webinar_id"`
	WebinarSlug   string   `json:"webinar_slug"`
	WebinarTitle  string   `json:"webinar_title"`
	Email         string   `json:"email"`
	Name          string   `json:"name"`           // ФИО, may be empty
	RecordingURL  string   `json:"recording_url"`  // primary download link
	RecordingURLs []string `json:"recording_urls"` // all segments, if several
}

// queuedNotification is a pre-marshaled webhook delivery.
type queuedNotification struct {
	event  string
	url    string
	secret string
	body   []byte
}

// target is one configurable webhook endpoint.
type target struct {
	url    string
	secret string
}

// Notifier delivers outgoing webhook events asynchronously: source requests
// (manual add, CSV import, payment webhook, recording publishing) never wait
// for or fail because of the delivery.
type Notifier struct {
	Client    *http.Client
	queue     chan queuedNotification
	regTarget target // participant.registered channel
	recTarget target // recording.published channel
}

// NewNotifier returns nil when neither webhook endpoint is configured.
func NewNotifier(cfg config.NotifyConfig) *Notifier {
	reg := target{url: cfg.WebhookURL, secret: cfg.WebhookSecret}
	rec := target{url: cfg.RecordingURL, secret: cfg.RecordingSecret}
	if reg.url == "" && rec.url == "" {
		return nil
	}
	return &Notifier{
		Client:    &http.Client{Timeout: 10 * time.Second},
		queue:     make(chan queuedNotification, 4096),
		regTarget: reg,
		recTarget: rec,
	}
}

// RegistrationsEnabled reports whether the registration channel is set up.
func (n *Notifier) RegistrationsEnabled() bool {
	return n != nil && n.regTarget.url != ""
}

// NotifyRegistered enqueues a participant.registered event.
func (n *Notifier) NotifyRegistered(wb *model.Webinar, p *model.Participant, link string) {
	if n == nil || n.regTarget.url == "" {
		return
	}
	n.enqueue(ParticipantNotification{
		Event:        "participant.registered",
		WebinarID:    wb.ID,
		WebinarSlug:  wb.Slug,
		WebinarTitle: wb.Title,
		Email:        strings.ToLower(p.Email),
		Name:         p.Name,
		Code:         p.Code,
		Link:         link,
		Source:       p.Source,
	}, n.regTarget)
}

// NotifyRecording enqueues a recording.published event for one participant.
func (n *Notifier) NotifyRecording(wb *model.Webinar, p *model.Participant, urls []string) {
	if n == nil || n.recTarget.url == "" || len(urls) == 0 {
		return
	}
	n.enqueue(RecordingNotification{
		Event:         "recording.published",
		WebinarID:     wb.ID,
		WebinarSlug:   wb.Slug,
		WebinarTitle:  wb.Title,
		Email:         strings.ToLower(p.Email),
		Name:          p.Name,
		RecordingURL:  urls[0],
		RecordingURLs: urls,
	}, n.recTarget)
}

func (n *Notifier) enqueue(payload any, t target) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("notify: marshal %T: %v", payload, err)
		return
	}
	event := ""
	switch v := payload.(type) {
	case ParticipantNotification:
		event = v.Event
	case RecordingNotification:
		event = v.Event
	}
	select {
	case n.queue <- queuedNotification{event: event, url: t.url, secret: t.secret, body: body}:
	default:
		log.Printf("notify: queue overflow, dropping event (%d bytes)", len(body))
	}
}

// Run drains the queue until ctx is cancelled. Each delivery is retried
// twice with a short backoff.
func (n *Notifier) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case q := <-n.queue:
			n.deliver(ctx, q)
		}
	}
}

func (n *Notifier) deliver(ctx context.Context, q queuedNotification) {
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.url, bytes.NewReader(q.body))
		if err != nil {
			log.Printf("notify: bad request: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Webhook-Event", q.event)
		if q.secret != "" {
			mac := hmac.New(sha256.New, []byte(q.secret))
			mac.Write(q.body)
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
	log.Printf("notify: giving up on %s (%d bytes)", q.event, len(q.body))
}

// participantLink builds the personal room link for a participant.
func (s *Server) participantLink(wb *model.Webinar, code string) string {
	return s.Cfg.AppURL + "/w/" + wb.Slug + "?c=" + code
}
