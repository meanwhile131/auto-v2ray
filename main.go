package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"net/http"

	"github.com/xtls/libxray/share"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/infra/conf/cfgcommon/duration"
	_ "github.com/xtls/xray-core/main/distro/all"
)

type RawFieldRule struct {
	conf.RouterRule
	Domain     *conf.StringList        `json:"domain"`
	Domains    *conf.StringList        `json:"domains"`
	IP         *conf.StringList        `json:"ip"`
	Port       *conf.PortList          `json:"port"`
	Network    *conf.NetworkList       `json:"network"`
	SourceIP   *conf.StringList        `json:"sourceIP"`
	Source     *conf.StringList        `json:"source"`
	SourcePort *conf.PortList          `json:"sourcePort"`
	User       *conf.StringList        `json:"user"`
	VlessRoute *conf.PortList          `json:"vlessRoute"`
	InboundTag *conf.StringList        `json:"inboundTag"`
	Protocols  *conf.StringList        `json:"protocol"`
	Attributes map[string]string       `json:"attrs"`
	LocalIP    *conf.StringList        `json:"localIP"`
	LocalPort  *conf.PortList          `json:"localPort"`
	Process    *conf.StringList        `json:"process"`
	Webhook    *conf.WebhookRuleConfig `json:"webhook"`
}

func main() {
	u := flag.String("url", "", "URL with config share links")
	flag.Parse()
	if *u == "" {
		log.Fatal("URL is required")
	}
	resp, err := http.Get(*u)
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

	route := RawFieldRule{
		BalancerTag: "balancer",
		InboundTag:  &conf.StringList{"in"},
	}
	routeJson, err := json.Marshal(route)
	if err != nil {
		log.Fatal(err)
	}
	config.RouterConfig = &conf.RouterConfig{
		RuleList: []json.RawMessage{routeJson},
		Balancers: []*conf.BalancingRule{{
			Tag: "balancer",
			Strategy: conf.StrategyConfig{
				Type: "leastload",
			},
			Selectors: conf.StringList{"out"},
		}},
	}
	config.Observatory = &conf.ObservatoryConfig{
		SubjectSelector:   []string{"out"},
		ProbeURL:          "https://www.google.com/generate_204",
		ProbeInterval:     duration.Duration(10 * time.Minute),
		EnableConcurrency: true,
	}

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
