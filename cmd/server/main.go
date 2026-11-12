// Command server runs the webinar control application.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vdao/webinar_mtx/internal/config"
	"github.com/vdao/webinar_mtx/internal/db"
	"github.com/vdao/webinar_mtx/internal/model"
	"github.com/vdao/webinar_mtx/internal/mtx"
	"github.com/vdao/webinar_mtx/internal/oidc"
	"github.com/vdao/webinar_mtx/internal/recorder"
	"github.com/vdao/webinar_mtx/internal/s3"
	"github.com/vdao/webinar_mtx/internal/store"
	"github.com/vdao/webinar_mtx/internal/web"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe GET /healthz on localhost and exit")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sqlDB, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer sqlDB.Close()
	if err := db.Migrate(ctx, sqlDB); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("database ready, migrations applied")

	// OIDC admin login (optional).
	var oidcManager *oidc.Manager
	if cfg.OIDC.Enabled {
		oidcManager, err = oidc.New(ctx, cfg.OIDC)
		if err != nil {
			log.Fatalf("oidc: %v", err)
		}
		log.Printf("oidc: admin login via %s, access by group %q", cfg.OIDC.IssuerURL, cfg.OIDC.AdminGroup)
	}

	// S3 recording publishing (optional).
	var s3Client *s3.Client
	var recWorker *recorder.Worker
	if cfg.S3.Configured() {
		s3Client, err = s3.New(cfg.S3)
		if err != nil {
			log.Fatalf("s3: %v", err)
		}
		recWorker = recorder.New(cfg, store.New(sqlDB), mtx.New(cfg.MtxAPIURL), s3Client)
		log.Printf("s3: recording publishing to %s/%s", cfg.S3.PublicURL, cfg.S3.Prefix)
	} else {
		log.Println("s3: not configured, recording publishing disabled")
	}

	// Outgoing webhook notifications (optional, independent channels).
	notifier := web.NewNotifier(cfg.Notify)
	if notifier != nil {
		if cfg.Notify.WebhookURL != "" {
			log.Printf("notify: registration events to %s", cfg.Notify.WebhookURL)
		}
		if cfg.Notify.RecordingURL != "" {
			log.Printf("notify: recording events to %s", cfg.Notify.RecordingURL)
		}
	}

	// When a recording goes public, notify every active participant.
	if recWorker != nil {
		st := store.New(sqlDB)
		recWorker.OnPublished = func(ctx context.Context, wb *model.Webinar, urls []string) {
			if notifier == nil {
				return
			}
			participants, err := st.ListParticipants(ctx, wb.ID, "", 100000)
			if err != nil {
				log.Printf("notify: list participants of %s: %v", wb.Slug, err)
				return
			}
			n := 0
			for _, p := range participants {
				if p.Status != model.ParticipantActive {
					continue // blocked viewers must not receive the recording
				}
				notifier.NotifyRecording(wb, p, urls)
				n++
			}
			log.Printf("notify: recording of %s queued for %d participant(s)", wb.Slug, n)
		}
	}

	srv, err := web.New(cfg, store.New(sqlDB), mtx.New(cfg.MtxAPIURL), oidcManager, recWorker, notifier)
	if err != nil {
		log.Fatalf("init web: %v", err)
	}

	// registration notifier worker
	notifyDone := make(chan struct{})
	if notifier != nil {
		go func() {
			defer close(notifyDone)
			notifier.Run(ctx)
		}()
	} else {
		close(notifyDone)
	}

	// recording publisher worker
	recorderDone := make(chan struct{})
	if recWorker != nil {
		go func() {
			defer close(recorderDone)
			recWorker.Run(ctx)
		}()
	} else {
		close(recorderDone)
	}

	// periodic cleanup of expired sessions
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				srv.Store.CleanupExpired(context.Background())
			}
		}
	}()

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("listening on %s (admin: http://localhost%s/admin)", cfg.Listen, cfg.Listen)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down…")
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpSrv.Shutdown(shCtx)
	<-cleanupDone
	<-recorderDone
	<-notifyDone
	log.Println("bye")
}

// runHealthcheck powers the Docker healthcheck without a shell or wget.
func runHealthcheck() int {
	listen := "127.0.0.1:8080"
	if v := os.Getenv("LISTEN"); v != "" && v[0] == ':' {
		listen = "127.0.0.1" + v
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/healthz", listen))
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
