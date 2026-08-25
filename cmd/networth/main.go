package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MrShanks/networth/internal/fx"
	"github.com/MrShanks/networth/internal/store"
	"github.com/MrShanks/networth/internal/web"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "address to listen on")
	dbPath := flag.String("db", "networth.db", "path to the SQLite database file")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(*addr, *dbPath, log); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(addr, dbPath string, log *slog.Logger) error {
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	handler, err := web.NewServer(db, fx.NewClient(fx.DefaultEndpoint, store.Base, store.Foreign()), log)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", "http://"+addr, "db", dbPath, "base", store.Base)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
