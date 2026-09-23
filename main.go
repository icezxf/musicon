package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
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
	if err := db.Migrate(database); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if err := auth.EnsureDefaultUser(database, cfg.WebUser, cfg.WebPass); err != nil {
		log.Fatalf("ensure default user: %v", err)
	}
	log.Printf("[boot] DB ready: %s", cfg.DBPath)

	holder := &db.Holder{DB: database}
	settingsMgr := settings.New(database)
	alistClient := alist.New(settingsMgr)
	lxClient := lx.New(settingsMgr)

	var bgWG sync.WaitGroup

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(cfg.StaticDir))))
	// 只暴露 covers 目录，不再暴露 DataDir（保护 musicon.db）
	mux.Handle("/app/data/covers/", http.StripPrefix("/app/data/covers/",
		http.FileServer(http.Dir(cfg.DataDir+"/covers"))))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, cfg.StaticDir+"/index.html")
	})

	api.New(holder, alistClient, lxClient, cfg, settingsMgr, &bgWG).Mount(mux)
	subsonic.New(holder, alistClient, lxClient, cfg).Mount(mux)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           recoverMiddleware(logMiddleware(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0, // stream 是流式，不设限
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("[shutdown] signal received")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("[shutdown] %v", err)
		}
		// 等后台 goroutine
		done := make(chan struct{})
		go func() { bgWG.Wait(); close(done) }()
		select {
		case <-done:
			log.Println("[shutdown] background tasks done")
		case <-time.After(8 * time.Second):
			log.Println("[shutdown] timeout waiting for background tasks")
		}
		_ = database.Close()
		log.Println("[shutdown] bye")
		os.Exit(0)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" && r.URL.Path != "/favicon.ico" {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[panic] %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
