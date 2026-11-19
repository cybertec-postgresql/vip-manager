package main

import (
	"context"
	"errors"
	"fmt"

	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/cybertec-postgresql/vip-manager/checker"
	"github.com/cybertec-postgresql/vip-manager/ipmanager"
	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"github.com/spf13/pflag"
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
	// the whole lifecycle lives in run() so that the shutdown path, which
	// removes the virtual IP, is always reached before the process exits
	os.Exit(run())
}

func run() int {
	conf, err := vipconfig.NewConfig()
	if errors.Is(err, pflag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	log := conf.Logger.Sugar()
	defer func() { _ = conf.Logger.Sync() }()

	lc, err := checker.NewLeaderChecker(conf)
	if err != nil {
		log.Errorf("Failed to initialize leader checker: %s", err)
		return 1
	}

	states := make(chan bool)
	manager, err := ipmanager.NewIPManager(conf, states)
	if err != nil {
		log.Errorf("Problems with generating the virtual ip manager: %s", err)
		return 1
	}

	// SIGTERM is what systemd sends on "systemctl stop|restart" and what a
	// container runtime sends on stop. Without it the process is terminated
	// right away and the virtual IP stays assigned to this machine, while
	// Patroni promotes another node. Two hosts then answer on the same address.
	mainCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-mainCtx.Done()
		log.Info("Shutting down, the virtual IP is removed if it is assigned here")
	}()

	var (
		wg sync.WaitGroup
		// written by the checker goroutine, read after wg.Wait()
		checkerErr error
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		// A checker that gives up must not take the process down on the spot:
		// os.Exit() would skip the removal of the virtual IP. Cancel the
		// context instead and let the manager shut down in an orderly fashion.
		defer cancel()
		err := lc.GetChangeNotificationStream(mainCtx, states)
		checkerErr = checkerFailure(mainCtx.Err(), err)
		if checkerErr != nil {
			log.Errorw("Leader checker failed", zap.Error(checkerErr))
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		manager.SyncStates(mainCtx, states)
	}()

	wg.Wait()
	if checkerErr != nil {
		return 1
	}
	return 0
}

// checkerFailure returns the error to exit with after the leader checker
// returned err, or nil when the return was part of a shutdown. ctxErr is the
// error of the main context at that moment. The context decides whether the
// return is a failure, not err: a cancelled gRPC call does not wrap
// context.Canceled, and a checker can return context.Canceled from a timeout of
// its own.
func checkerFailure(ctxErr, err error) error {
	if ctxErr != nil {
		return nil
	}
	// A nil error is a failure as well: Restart=on-failure does not restart a
	// service that exits with 0, and the node would stay without vip-manager.
	if err == nil {
		return errors.New("leader checker stopped before the shutdown")
	}
	return err
}
