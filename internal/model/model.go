// Package model defines the database entities.
package model

import (
	"encoding/json"
	"time"
)

// Webinar statuses.
const (
	WebinarDraft     = "draft"
	WebinarScheduled = "scheduled"
	WebinarLive      = "live"
	WebinarEnded     = "ended"
)

// WebinarStatuses is the ordered list of valid webinar statuses.
var WebinarStatuses = []string{WebinarDraft, WebinarScheduled, WebinarLive, WebinarEnded}

// Participant statuses.
const (
	ParticipantActive  = "active"
	ParticipantBlocked = "blocked"
)

// Participant registration sources.
const (
	SourceImport  = "import"
	SourceManual  = "manual"
	SourceWebhook = "webhook"
)

// Webinar is a single webinar room backed by one MediaMTX path w/{StreamKey}.
type Webinar struct {
	ID          string
	Slug        string // public room URL part: /w/{slug}
	Title       string
	Description string
	StreamKey   string // secret MediaMTX path segment
	Status      string // draft | scheduled | live | ended
	ScheduledAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// Recording publishing to S3.
	RecordEnabled   bool   // record and publish this webinar when S3 is configured
	RecordingID     string // unguessable folder name in the bucket (empty = not yet assigned)
	RecordingStatus string // none | recording | uploading | published | failed
	RecordingURLs   string // JSON array of public download URLs
}

// Recording statuses.
const (
	RecordingNone      = "none"      // no recording expected yet
	RecordingActive    = "recording" // stream online, files are being written
	RecordingUploading = "uploading"
	RecordingPublished = "published"
	RecordingFailed    = "failed"
)

// RecordingURLList decodes RecordingURLs JSON into a slice; returns nil on
// empty or malformed input.
func (w *Webinar) RecordingURLList() []string {
	if w.RecordingURLs == "" {
		return nil
	}
	var out []string
	if json.Unmarshal([]byte(w.RecordingURLs), &out) != nil {
		return nil
	}
	return out
}

// Participant is a registered viewer of a specific webinar. Access to the
// room is granted by the personal Code embedded in the participant's link.
type Participant struct {
	ID         string
	WebinarID  string
	Email      string
	Name       string
	Code       string // personal access code, part of the room link
	Status     string // active | blocked
	Source     string // import | manual | webhook
	CreatedAt  time.Time
	LastSeenAt *time.Time
}

// ParticipantSession is a logged-in viewer session.
type ParticipantSession struct {
	ID            string
	ParticipantID string
	WebinarID     string
	TokenHash     string
	ExpiresAt     time.Time
	CreatedAt     time.Time
}

// Webhook is an inbound payment webhook configuration.
type Webhook struct {
	ID               string
	Name             string
	Secret           string
	EmailPath        string // dot-path to the customer email in the payload
	ProductPath      string // dot-path to the product/tariff id in the payload
	DefaultWebinarID *string
	IsActive         bool
	CreatedAt        time.Time
}

// WebhookProduct maps an external product id to a webinar.
type WebhookProduct struct {
	WebhookID  string
	ExternalID string
	WebinarID  string
}
