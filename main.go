package main

import (
	"context"
	"fmt"

	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/cybertec-postgresql/vip-manager/checker"
	"github.com/cybertec-postgresql/vip-manager/ipmanager"
	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"go.uber.org/zap"
)

var (
	// vip-manager version definition
	version = "master"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if (len(os.Args) > 1) && (os.Args[1] == "--version") {
		fmt.Printf("version: %s\n", version)
		fmt.Printf("commit:  %s\n", commit)
		fmt.Printf("date:    %s\n", date)
		return
	}

	conf, err := vipconfig.NewConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log := conf.Logger.Sugar()
	defer func() { _ = conf.Logger.Sync() }()

	lc, err := checker.NewLeaderChecker(conf)
	if err != nil {
		log.Fatalf("Failed to initialize leader checker: %s", err)
	}

	states := make(chan bool)
	manager, err := ipmanager.NewIPManager(conf, states)
	if err != nil {
		log.Fatalf("Problems with generating the virtual ip manager: %s", err)
	}

	mainCtx, cancel := context.WithCancel(context.Background())

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGHUP)
		for sig := range c {
			if sig == syscall.SIGHUP {
				// Reopen the log file so that logrotate (via "systemctl kill
				// -s HUP") can move the current file aside. No-op on stdout.
				log.Info("Received SIGHUP, reopening log file")
				if err := conf.ReopenLog(); err != nil {
					log.Errorf("Failed to reopen log file: %s", err)
				}
				continue
			}
			log.Info("Received exit signal")
			cancel()
			return
		}
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		err := lc.GetChangeNotificationStream(mainCtx, states)
		if err != nil && err != context.Canceled {
			log.Fatal("Leader checker returned the following error: %s", zap.Error(err))
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		manager.SyncStates(mainCtx, states)
		wg.Done()
	}()

	wg.Wait()
}
