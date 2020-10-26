package main

import (
	"context"
	"fmt"

	// "flag"

	"log"
	"os"
	"os/signal"
	"sync"

	"github.com/cybertec-postgresql/vip-manager/checker"
	"github.com/cybertec-postgresql/vip-manager/ipmanager"
	"github.com/cybertec-postgresql/vip-manager/vipconfig"
)

var (
	// vip-manager version definition
	version string = "1.0"
)

func main() {
	if (len(os.Args) > 1) && (os.Args[1] == "--version") {
		//			log.Print("version " + version)
		//			return nil, nil
		//		}
		fmt.Printf("version: %s\n", version)
		return
	}

	conf, err := vipconfig.NewConfig()
	if err != nil {
		log.Fatal(err)
	}

	lc, err := checker.NewLeaderChecker(conf)
	if err != nil {
		log.Fatalf("Failed to initialize leader checker: %s", err)
	}

	states := make(chan bool)
	manager, err := ipmanager.NewIPManager(
		conf,
		states,
	)
	if err != nil {
		log.Fatalf("Problems with generating the virtual ip manager: %s", err)
	}

	mainCtx, cancel := context.WithCancel(context.Background())

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		log.Print("Received exit signal")
		cancel()
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		err := lc.GetChangeNotificationStream(mainCtx, states)
		if err != nil && err != context.Canceled {
			log.Fatalf("Leader checker returned the following error: %s", err)
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
