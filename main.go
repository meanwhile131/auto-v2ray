package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"net/http"

	"github.com/xtls/libxray/share"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	_ "github.com/xtls/xray-core/main/distro/all"
)

func main() {
	u := os.Args[1]
	resp, err := http.Get(u)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	config, err := share.ConvertShareLinksToXrayJson(string(body))
	if err != nil {
		log.Fatal(err)
	}

	for i := range config.OutboundConfigs {
		config.OutboundConfigs[i].Tag = fmt.Sprintf("out%d", i)
		config.OutboundConfigs[i].SendThrough = nil
	}
	config.OutboundConfigs = append(config.OutboundConfigs, conf.OutboundDetourConfig{
		Tag:      "freedom",
		Protocol: "freedom",
	})
	config.InboundConfigs = append(config.InboundConfigs, conf.InboundDetourConfig{
		PortList: &conf.PortList{Range: []conf.PortRange{{
			From: 9000,
			To:   9000,
		}}},
		Protocol: "socks",
		Tag:      "in",
		ListenOn: &conf.Address{Address: net.ParseAddress("127.0.0.1")}},
	)

	cfg, err := config.Build()
	if err != nil {
		log.Fatal(err)
	}
	in, err := core.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	err = in.Start()
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	log.Println("Exiting")
	err = in.Close()
	if err != nil {
		log.Fatal(err)
	}
}
