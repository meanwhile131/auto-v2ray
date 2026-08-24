package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bufio"
	"net/http"

	"github.com/meanwhile131/auto-v2ray/parse"
	"github.com/xtls/xray-core/app/dispatcher"
	xlog "github.com/xtls/xray-core/app/log"
	"github.com/xtls/xray-core/app/observatory/burst"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/app/router"
	clog "github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/proxy/socks"
)

func main() {
	u := os.Args[1]
	resp, err := http.Get(u)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	outbounds := make([]*core.OutboundHandlerConfig, 0)
	i := 0
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) == 0 {
			continue
		}
		if line[0] == '#' {
			continue
		}
		outbound, err := parse.OutboundURL(line)
		if err != nil {
			log.Printf("Failed to parse %s: %s", line, err)
			continue
		}
		outbound.Tag = fmt.Sprintf("out%d", i)
		outbounds = append(outbounds, outbound)
		i++
	}
	log.Printf("Collected %d outbounds", len(outbounds))
	err = scanner.Err()
	if err != nil {
		log.Fatal(err)
	}

	inbound := core.InboundHandlerConfig{
		Tag: "in",
		ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
			PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(9000)}},
			Listen:   net.NewIPOrDomain(net.ParseAddress("127.0.0.1")),
		}),
		ProxySettings: serial.ToTypedMessage(&socks.ServerConfig{}),
	}
	freedom := core.OutboundHandlerConfig{
		Tag:            "freedom",
		SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{}),
		ProxySettings:  serial.ToTypedMessage(&freedom.Config{}),
	}
	outbounds = append(outbounds, &freedom)
	config := core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&xlog.Config{
				ErrorLogLevel: clog.Severity_Info,
				ErrorLogType:  xlog.LogType_Console,
				AccessLogType: xlog.LogType_Console,
			}),
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&router.Config{
				BalancingRule: []*router.BalancingRule{{
					Tag:              "router",
					OutboundSelector: []string{"out"},
					Strategy:         "leastload",
					StrategySettings: serial.ToTypedMessage(&router.StrategyLeastLoadConfig{}),
					FallbackTag:      "freedom",
				}},
				Rule: []*router.RoutingRule{{
					InboundTag: []string{"in"},
					TargetTag: &router.RoutingRule_BalancingTag{
						BalancingTag: "router",
					},
				}},
			}),
			serial.ToTypedMessage(&burst.Config{
				SubjectSelector: []string{"out"},
				PingConfig: &burst.HealthPingConfig{
					Interval:   int64(10 * time.Minute),
				},
			}),
		},
		Inbound:  []*core.InboundHandlerConfig{&inbound},
		Outbound: outbounds,
	}
	in, err := core.New(&config)
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
	err = in.Close()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Exiting")
}
