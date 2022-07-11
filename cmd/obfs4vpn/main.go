package main

// Shows an example of how to start a VPN Client over an obfuscated transport.

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/ooni/minivpn/obfs4"
	"github.com/ooni/minivpn/vpn"
)

func main() {
	provider := os.Getenv("PROVIDER")
	if provider == "" {
		log.Fatal("Export the PROVIDER variable")
	}
	opts, err := vpn.ParseConfigFile("data/" + provider + "/config")
	if err != nil {
		panic(err)
	}
	if opts.ProxyOBFS4 == "" {
		log.Fatal("ERROR: missing proto-obfs4 entry in config")
	}

	node, err := obfs4.NewNodeFromURI(opts.ProxyOBFS4)
	if err != nil {
		log.Fatal(err)
	}

	err = obfs4.Obfs4ClientInit(node)
	if err != nil {
		log.Fatal(err)
	}
	dialer := vpn.NewTunDialerFromOptions(opts)

	var obfs4Dialer vpn.DialerContext = obfs4.NewDialer(node)
	dialer.Dialer = obfs4Dialer

	client := http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
	}
	if len(os.Args) != 2 {
		log.Println("Usage: get <https://foobar>")
		os.Exit(1)
	}
	uri := os.Args[1]
	resp, err := client.Get(uri)
	if err != nil {
		log.Panic(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Panic(err)
	}
	fmt.Println(string(body))
}
