// Package recorder watches recorded webinars and publishes the finished
// recordings to S3.
//
// MediaMTX writes segments of a webinar under {RecordingsDir}/w/{streamKey}/.
// The worker enables per-path recording through the MediaMTX API, waits for
// the stream to go offline (>= grace, or webinar status "ended"), waits for
// the files to become stable (mtime unchanged between ticks — a segment is
// finalized when the publisher disconnects), uploads every segment to
// {S3Prefix}/{recordingID}/{file} and records the public URLs in the
// database. Participants then see download links on the room page.
package recorder

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vdao/webinar_mtx/internal/config"
	"github.com/vdao/webinar_mtx/internal/model"
	"github.com/vdao/webinar_mtx/internal/mtx"
	"github.com/vdao/webinar_mtx/internal/s3"
	"github.com/vdao/webinar_mtx/internal/store"
)

const (
	tick          = 30 * time.Second
	offlineGrace  = 10 * time.Minute // stream considered finished after this much offline
	retryAfter    = 5 * time.Minute  // retry cadence for failed uploads
	recordKeepFor = "168h"           // local segment retention (MediaMTX recordDeleteAfter)
	noFilesFailIn = 30 * time.Minute // ended+offline with no files → mark failed
)

// Worker is the background recording publisher.
type Worker struct {
	Cfg        *config.Config
	Store      *store.Store
	Mtx        *mtx.Client
	S3         *s3.Client
	PublishNow chan string // webinar ids pushed by the admin button

	mu         sync.Mutex
	offline    map[string]time.Time            // webinar id → offline since
	lastFail   map[string]time.Time            // webinar id → last failed attempt
	fileSigs   map[string]map[string]time.Time // webinar id → last segment snapshot (name → mtime)
	nowPublish chan string                     // internal wakeup
}

// New builds the worker. Returns nil when S3 publishing is not configured.
func New(cfg *config.Config, st *store.Store, mtxClient *mtx.Client, s3c *s3.Client) *Worker {
	if s3c == nil || !cfg.S3.Configured() {
		return nil
	}
	return &Worker{
		Cfg: cfg, Store: st, Mtx: mtxClient, S3: s3c,
		PublishNow: make(chan string, 16),
		nowPublish: make(chan string, 16),
		offline:    map[string]time.Time{},
		lastFail:   map[string]time.Time{},
		fileSigs:   map[string]map[string]time.Time{},
	}
}

// RequestPublish asks the worker to publish the webinar recording on its
// next tick (used by the admin button).
func (w *Worker) RequestPublish(webinarID string) error {
	select {
	case w.PublishNow <- webinarID:
		return nil
	default:
		return staticErr("publish queue is busy")
	}
}

// Run drives the worker until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case id := <-w.PublishNow:
			w.nowPublish <- id
		}
		w.tick(ctx)
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	webinars, err := w.Store.ListWebinars(ctx)
	if err != nil {
		log.Printf("recorder: list webinars: %v", err)
		return
	}
	now := time.Now()

	var force map[string]bool
	for len(w.nowPublish) > 0 {
		id := <-w.nowPublish
		if force == nil {
			force = map[string]bool{}
		}
		force[id] = true
	}

	for _, wb := range webinars {
		if !wb.RecordEnabled {
			continue
		}
		switch wb.Status {
		case model.WebinarDraft:
			continue // nothing to record yet
		case model.WebinarLive, model.WebinarScheduled, model.WebinarEnded:
		}

		// Re-assert the runtime path config (MediaMTX restart wipes it).
		if wb.Status != model.WebinarEnded {
			if err := w.Mtx.EnsurePathRecord(ctx, "w/"+wb.StreamKey, recordKeepFor); err != nil {
				log.Printf("recorder: ensure path record %s: %v", wb.Slug, err)
			}
		}

		// Stream state.
		info, err := w.Mtx.GetPath(ctx, "w/"+wb.StreamKey)
		if err != nil {
			log.Printf("recorder: get path %s: %v", wb.Slug, err)
			continue
		}
		online := info != nil && info.Ready

		w.mu.Lock()
		if online {
			delete(w.offline, wb.ID)
		} else if _, ok := w.offline[wb.ID]; !ok {
			w.offline[wb.ID] = now
		}
		offlineFor := now.Sub(w.offline[wb.ID])
		w.mu.Unlock()

		if online && wb.RecordingStatus == model.RecordingNone {
			_ = w.Store.SetRecordingStatus(ctx, wb.ID, model.RecordingActive)
			continue
		}
		if online && wb.RecordingStatus == model.RecordingActive {
			continue // still live, nothing to publish
		}

		if wb.RecordingStatus == model.RecordingPublished {
			continue
		}

		// Decide whether to publish now.
		want := false
		if wb.Status == model.WebinarEnded && !online {
			want = true
		}
		if !online && offlineFor >= offlineGrace {
			want = true
		}
		if force[wb.ID] {
			if online {
				log.Printf("recorder: %s publish requested but stream is online, skipping", wb.Slug)
				continue
			}
			want = true
		}
		if !want {
			continue
		}

		// Failed uploads retry with backoff (unless forced).
		w.mu.Lock()
		lastFail := w.lastFail[wb.ID]
		w.mu.Unlock()
		if !force[wb.ID] && time.Since(lastFail) < retryAfter {
			continue
		}

		if err := w.publish(ctx, wb, force[wb.ID]); err != nil {
			if IsNotStable(err) {
				// Segment files are still being finalized — a transient state,
				// retry on the next tick without touching the status.
				continue
			}
			log.Printf("recorder: publish %s: %v", wb.Slug, err)
			w.mu.Lock()
			w.lastFail[wb.ID] = time.Now()
			w.mu.Unlock()
			_ = w.Store.SetRecordingStatus(ctx, wb.ID, model.RecordingFailed)
		}
	}
}

// publish uploads stable segment files of one webinar and marks it published.
func (w *Worker) publish(ctx context.Context, wb *model.Webinar, forced bool) error {
	dir := filepath.Join(w.Cfg.RecordingsDir, "w", wb.StreamKey)
	files, ready := w.stableFiles(wb.ID, dir)
	if !ready || len(files) == 0 {
		if forced {
			if len(files) == 0 && !dirExists(dir) {
				return errNoFiles
			}
			return errNotStable
		}
		w.mu.Lock()
		since := w.offline[wb.ID]
		w.mu.Unlock()
		if wb.Status == model.WebinarEnded && !since.IsZero() && time.Since(since) > noFilesFailIn {
			return errNoFiles
		}
		return errNotStable // retry on the next tick
	}

	recordingID, err := w.Store.EnsureRecordingID(ctx, wb.ID, config.RandomHex(16))
	if err != nil {
		return err
	}
	_ = w.Store.SetRecordingStatus(ctx, wb.ID, model.RecordingUploading)

	var urls []string
	for _, f := range files {
		key := w.S3.ObjectKey(recordingID, filepath.Base(f))
		if err := w.S3.UploadFile(ctx, key, f); err != nil {
			return err
		}
		urls = append(urls, w.S3.PublicURL(key))
	}
	b, _ := json.Marshal(urls)
	if err := w.Store.SetRecordingResult(ctx, wb.ID, model.RecordingPublished, string(b)); err != nil {
		return err
	}
	log.Printf("recorder: published %d segment(s) of %s", len(files), wb.Slug)
	return nil
}

var (
	errNoFiles   = staticErr("no recording files found")
	errNotStable = staticErr("recording files are still being written")
)

type staticErr string

func (e staticErr) Error() string { return string(e) }

// IsNoFiles reports the "nothing was recorded" sentinel error.
func IsNoFiles(err error) bool { return err == errNoFiles }

// IsNotStable reports the "files still changing" sentinel error.
func IsNotStable(err error) bool { return err == errNotStable }

func dirExists(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// stableFiles lists finalized *.mp4 segments. A snapshot (names + mtimes) must
// stay identical between two ticks to be considered final: MediaMTX closes
// the segment file when the publisher disconnects. Returns ready=false when
// the directory is missing, empty, or the files are still changing.
func (w *Worker) stableFiles(webinarID, dir string) (files []string, ready bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		w.mu.Lock()
		delete(w.fileSigs, webinarID)
		w.mu.Unlock()
		return nil, false
	}
	var names []string
	modTime := map[string]time.Time{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".mp4") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		names = append(names, filepath.Join(dir, e.Name()))
		modTime[e.Name()] = info.ModTime()
	}
	if len(names) == 0 {
		w.mu.Lock()
		delete(w.fileSigs, webinarID)
		w.mu.Unlock()
		return nil, false
	}

	w.mu.Lock()
	prev, had := w.fileSigs[webinarID]
	w.fileSigs[webinarID] = modTime
	w.mu.Unlock()

	if had && sameSnapshot(prev, modTime) {
		sort.Strings(names)
		return names, true
	}
	return nil, false
}

func sameSnapshot(a, b map[string]time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok || !v.Equal(bv) {
			return false
		}
	}
	return true
}
