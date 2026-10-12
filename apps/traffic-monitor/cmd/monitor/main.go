package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"x-ui-tab/traffic-monitor/internal/monitor"
)

func main() {
	config := flag.String("config", "", "absolute path to config JSON")
	flag.Parse()
	if *config == "" {
		log.Fatal("-config is required")
	}
	cfg, err := monitor.LoadConfig(*config)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	store, err := monitor.OpenStore(cfg, time.Now())
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer store.Close()
	if err = store.Prune(time.Now()); err != nil {
		log.Fatalf("initial cleanup: %v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	collector := monitor.NewCollector(cfg, store)
	go collector.Run(ctx)
	server := &http.Server{Addr: cfg.Listen, Handler: monitor.NewServer(cfg, store, collector), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("traffic monitor listening on %s (detail %d days, summary %d days)", cfg.Listen, cfg.DetailDays, cfg.SummaryDays)
	if cfg.TLSCertFile != "" {
		err = server.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
