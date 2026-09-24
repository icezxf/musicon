package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/icezxf/musicon-go/internal/alist"
	"github.com/icezxf/musicon-go/internal/api"
	"github.com/icezxf/musicon-go/internal/auth"
	"github.com/icezxf/musicon-go/internal/config"
	"github.com/icezxf/musicon-go/internal/db"
	"github.com/icezxf/musicon-go/internal/lx"
	"github.com/icezxf/musicon-go/internal/ncm"
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
	lxClient := lx.New(settingsMgr, holder)

	// ---------- 内置 ncm-server 子进程 ----------
	var ncmCmd *exec.Cmd
	if settingsMgr.GetNCMMode() == "internal" {
		port := settingsMgr.GetNCMEmbeddedPort()
		ncmCmd, err = startNCMEmbedded(port)
		if err != nil {
			log.Printf("[ncm] 内置 ncm-server 启动失败，将仅使用外部模式: %v", err)
			ncmCmd = nil
		} else {
			_ = settingsMgr.Set("ncm_server_url", fmt.Sprintf("http://127.0.0.1:%d", port))
			log.Printf("[ncm] 内置 ncm-server 已启动，监听 127.0.0.1:%d", port)
		}
	} else {
		log.Printf("[ncm] 使用外部模式: %s", settingsMgr.GetNCMURL())
	}
	if ncmCmd != nil {
		defer stopNCMEmbedded(ncmCmd)
	}

	ncmClient := ncm.New(settingsMgr.GetNCMURL(), holder)

	var bgWG sync.WaitGroup

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(cfg.StaticDir))))
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

	api.New(holder, alistClient, lxClient, ncmClient, cfg, settingsMgr, &bgWG).Mount(mux)
	subsonic.New(holder, alistClient, lxClient, ncmClient, cfg).Mount(mux)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           recoverMiddleware(logMiddleware(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0,
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
		// 先停 ncm-server，再等后台任务
		if ncmCmd != nil {
			stopNCMEmbedded(ncmCmd)
		}
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

// ---------- ncm-server 子进程管理 ----------

const ncmBinaryPath = "/app/ncm-server"

func startNCMEmbedded(port int) (*exec.Cmd, error) {
	if _, err := os.Stat(ncmBinaryPath); err != nil {
		return nil, fmt.Errorf("未找到内置 ncm-server (%s): %w", ncmBinaryPath, err)
	}

	cmd := exec.Command(ncmBinaryPath,
		"--port", strconv.Itoa(port),
		"--bind", "127.0.0.1",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// 等端口就绪（最多 10 秒）
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil, fmt.Errorf("ncm-server 提前退出")
		}
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return cmd, nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("ncm-server 启动超时")
}

func stopNCMEmbedded(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	log.Println("[ncm] 正在停止内置 ncm-server")
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Println("[ncm] ncm-server 已停止")
	case <-time.After(5 * time.Second):
		log.Println("[ncm] ncm-server 停止超时，强制 kill")
		_ = cmd.Process.Kill()
	}
}

// ---------- 中间件 ----------

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
