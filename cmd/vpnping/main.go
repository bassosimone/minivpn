package main

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/apex/log"
	"github.com/ooni/minivpn/extras/ping"
	"github.com/ooni/minivpn/internal/model"
	"github.com/ooni/minivpn/internal/networkio"
	"github.com/ooni/minivpn/internal/tun"
)

func timeoutSecondsFromCount(count int) time.Duration {
	waitOnLastOne := 3 * time.Second
	return time.Duration(count)*time.Second + waitOnLastOne
}

func main() {
	log.SetLevel(log.DebugLevel)

	// parse the configuration file
	options, err := model.ReadConfigFile(os.Args[1])
	if err != nil {
		log.WithError(err).Fatal("NewOptionsFromFilePath")
	}
	log.Infof("parsed options: %s", options.ServerOptionsString())

	// TODO(ainghazal): move the initialization step to an early phase and keep a ref in the muxer
	if !options.HasAuthInfo() {
		log.Fatal("options are missing auth info")
	}
	// connect to the server
	dialer := networkio.NewDialer(log.Log, &net.Dialer{})
	ctx := context.Background()
	endpoint := net.JoinHostPort(options.Remote, options.Port)
	conn, err := dialer.DialContext(ctx, options.Proto.String(), endpoint)
	if err != nil {
		log.WithError(err).Fatal("dialer.DialContext")
	}

	// create a tun Device
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tunnel, err := tun.StartTUN(ctx, conn, options)
	if err != nil {
		log.WithError(err).Fatal("init error")
	}

	pinger := ping.New("8.8.8.8", tunnel)
	count := 5
	pinger.Count = count

	err = pinger.Run(context.Background())
	if err != nil {
		pinger.PrintStats()
		log.WithError(err).Fatal("ping error")
	}
	pinger.PrintStats()

	// wait for workers to terminate
	// workers.WaitWorkersShutdown()
}
