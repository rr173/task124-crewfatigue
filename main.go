// Command task124-crewfatigue runs the aviation crew flight-duty & fatigue
// compliance engine either as an HTTP service or in --smoke-test mode (which
// exercises the full business loop in-process against an in-memory SQLite
// and exits).
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // embed the IANA tz database so CGO=0 binaries resolve Asia/Shanghai etc. in any container

	"task124-crewfatigue/internal/compliance"
	"task124-crewfatigue/internal/crew"
	"task124-crewfatigue/internal/httpapi"
	"task124-crewfatigue/internal/schedule"
	"task124-crewfatigue/internal/selfcheck"
	"task124-crewfatigue/internal/store"
	"task124-crewfatigue/internal/webfs"
)

// webFS is the embedded static frontend (native HTML/CSS/JS, no build step).
// The embed lives in package webfs so the selfcheck can serve the same page.
var webFS = webfs.HTTPFS()

func main() {
	smoke := flag.Bool("smoke-test", false, "run self-check and exit")
	dbPath := flag.String("db", "crewfatigue.db", "SQLite database file path")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	if *smoke {
		selfcheck.RunAndExit()
		return
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cs := crew.New(st)
	sch := schedule.New(st)
	cps := compliance.New(st, sch)
	svc := httpapi.Services{Store: st, Crew: cs, Schedule: sch, Compliance: cps}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           httpapi.NewMux(svc, webFS),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("task124-crewfatigue %s listening on %s (db=%s)", httpapi.Version, *addr, *dbPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
