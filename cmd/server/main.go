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
	"github.com/vdao/webinar_mtx/internal/mail"
	"github.com/vdao/webinar_mtx/internal/mtx"
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

	srv, err := web.New(cfg, store.New(sqlDB), mail.New(cfg.SMTP), mtx.New(cfg.MtxAPIURL))
	if err != nil {
		log.Fatalf("init web: %v", err)
	}

	// periodic cleanup of expired sessions and codes
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
