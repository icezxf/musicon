package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/icezxf/musicon-go/internal/alist"
	"github.com/icezxf/musicon-go/internal/api"
	"github.com/icezxf/musicon-go/internal/auth"
	"github.com/icezxf/musicon-go/internal/config"
	"github.com/icezxf/musicon-go/internal/db"
	"github.com/icezxf/musicon-go/internal/lx"
	"github.com/icezxf/musicon-go/internal/settings"
	"github.com/icezxf/musicon-go/internal/subsonic"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	cfg := config.Load()
	log.Printf("[boot] MusicOn Go listening on %s", cfg.Listen)

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if err := auth.EnsureDefaultUser(database, cfg.WebUser, cfg.WebPass); err != nil {
		log.Printf("ensure default user: %v", err)
	}
	log.Printf("[boot] DB ready: %s", cfg.DBPath)

	holder := &db.Holder{DB: database}
	settingsMgr := settings.New(database)
	alistClient := alist.New(settingsMgr)
	lxClient := lx.New(settingsMgr.GetLXURL())

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(cfg.StaticDir))))
	mux.Handle("/app/data/", http.StripPrefix("/app/data/", http.FileServer(http.Dir(cfg.DataDir))))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, cfg.StaticDir+"/index.html")
	})

	api.New(holder, alistClient, lxClient, cfg, settingsMgr).Mount(mux)
	subsonic.New(holder, alistClient, lxClient, cfg).Mount(mux)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           logMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("[shutdown] signal received")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}
