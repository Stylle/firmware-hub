package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Stylle/firmware-hub/internal/hub"
)

//go:embed all:web/dist
var assets embed.FS

func main() {
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		dir = "data"
	}
	if len(os.Args) > 1 && os.Args[1] == "admin-sync" {
		if e := hub.ResetAdmin(dir, os.Getenv("USER"), os.Getenv("PASSWORD")); e != nil {
			log.Fatal(e)
		}
		fmt.Println("Administrator updated; previous sessions revoked.")
		return
	}
	a, e := hub.Open(dir, os.Getenv("USER"), os.Getenv("PASSWORD"))
	if e != nil {
		log.Fatal(e)
	}
	defer a.Close()
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		dir := filepath.Join(dir, "backups")
		if e = os.MkdirAll(dir, 0700); e != nil {
			log.Fatal(e)
		}
		out := filepath.Join(dir, "firmware-hub-"+time.Now().UTC().Format("20060102T150405.000000000")+".zip")
		if e = a.Backup(out); e != nil {
			log.Fatal(e)
		}
		fmt.Println("Backup saved:", out, "(contains credentials/master key; protect or encrypt offline)")
		return
	}
	web, _ := fs.Sub(assets, "web/dist")
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go a.RunNotifications(ctx)
	s := &http.Server{Addr: addr, Handler: a.Handler(web), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = s.Shutdown(c)
	}()
	log.Printf("Firmware Hub %s listening on %s", hub.Version, addr)
	if e = s.ListenAndServe(); e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
