package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vdao/webinar_mtx/internal/config"
	"github.com/vdao/webinar_mtx/internal/model"
)

type received struct {
	event string
	sig   string
	body  map[string]any
}

func startReceiver(t *testing.T) (*httptest.Server, chan received) {
	t.Helper()
	ch := make(chan received, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(body, &m)
		ch <- received{event: r.Header.Get("X-Webhook-Event"), sig: r.Header.Get("X-Webhook-Signature"), body: m}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

func TestNotifierRecordingEvent(t *testing.T) {
	recv, ch := startReceiver(t)
	n := NewNotifier(config.NotifyConfig{
		RecordingURL:    recv.URL + "/rec",
		RecordingSecret: "s3cret",
	})
	if n == nil {
		t.Fatal("notifier must be created when recording URL is set")
	}
	if n.RegistrationsEnabled() {
		t.Error("registration channel must be off")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)

	wb := &model.Webinar{ID: "wb1", Slug: "demo", Title: "Демо"}
	p := &model.Participant{ID: "p1", Email: "ANNA@test.ru", Name: "Анна Иванова"}
	urls := []string{"https://cdn.example.com/recordings/abc/seg1.mp4"}
	n.NotifyRecording(wb, p, urls)
	// Without URLs the event is dropped silently.
	n.NotifyRecording(wb, p, nil)

	select {
	case got := <-ch:
		if got.event != "recording.published" {
			t.Errorf("event = %q", got.event)
		}
		if got.body["email"] != "anna@test.ru" {
			t.Errorf("email = %v", got.body["email"])
		}
		if got.body["name"] != "Анна Иванова" {
			t.Errorf("name (ФИО) = %v", got.body["name"])
		}
		if got.body["recording_url"] != urls[0] {
			t.Errorf("recording_url = %v", got.body["recording_url"])
		}
		if arr, ok := got.body["recording_urls"].([]any); !ok || len(arr) != 1 {
			t.Errorf("recording_urls = %v", got.body["recording_urls"])
		}
		if got.sig == "" {
			t.Error("signature header must be present with a configured secret")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recording event was not delivered")
	}
	// Only one event must have arrived (the nil-URLs one is dropped).
	select {
	case extra := <-ch:
		t.Fatalf("unexpected extra event: %+v", extra)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestNotifierNilAndDisabledChannels(t *testing.T) {
	if NewNotifier(config.NotifyConfig{}) != nil {
		t.Error("no endpoints configured must yield nil notifier")
	}
	var n *Notifier // nil receiver: methods must be safe
	n.NotifyRegistered(&model.Webinar{}, &model.Participant{}, "x")
	n.NotifyRecording(&model.Webinar{}, &model.Participant{}, []string{"u"})

	// Registration-only notifier ignores recording events.
	regOnly := NewNotifier(config.NotifyConfig{WebhookURL: "http://x/"})
	if regOnly == nil || !regOnly.RegistrationsEnabled() {
		t.Fatal("registration notifier expected")
	}
	regOnly.NotifyRecording(&model.Webinar{}, &model.Participant{}, []string{"u"})
	if len(regOnly.queue) != 0 {
		t.Error("recording event must not be queued on a registration-only notifier")
	}
}
