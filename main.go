package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"bufio"
	"encoding/base64"
	"encoding/hex"
	"net/http"

	"github.com/xtls/xray-core/app/dispatcher"
	xlog "github.com/xtls/xray-core/app/log"
	"github.com/xtls/xray-core/app/observatory/burst"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/app/router"
	clog "github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/proxy/socks"
	"github.com/xtls/xray-core/proxy/vless"
	vlessOut "github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/grpc"
	"github.com/xtls/xray-core/transport/internet/reality"
	"github.com/xtls/xray-core/transport/internet/tcp"
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
		outbound, err := parseOutboundURL(line)
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
					HttpMethod: "GET",
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

func parseOutboundURL(u string) (*core.OutboundHandlerConfig, error) {
	log.Printf("%s", u)
	up, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(up.Port(), 10, 32)
	if err != nil {
		return nil, err
	}
	q := up.Query()
	addr := net.NewIPOrDomain(net.ParseAddress(up.Hostname()))
	uport := uint32(port)

	security := ""
	if len(q["security"]) > 0 {
		security = q["security"][0]
	}
	var seccfg *serial.TypedMessage
	switch security {
	case "reality":
		pbk, err := base64.RawURLEncoding.DecodeString(q["pbk"][0])
		if err != nil {
			return nil, err
		}
		rawSid := ""
		if len(q["sid"]) > 0 {
			rawSid = q["sid"][0]
		}
		sid, err := hex.DecodeString(rawSid)
		if err != nil {
			return nil, err
		}
		fp := ""
		if len(q["fp"]) > 0 {
			fp = q["fp"][0]
		}
		seccfg = serial.ToTypedMessage(&reality.Config{
			PublicKey:   pbk,
			Fingerprint: fp,
			ShortId:     sid,
			ServerName:  q["sni"][0],
		})
	case "":
		seccfg = nil
	default:
		return nil, fmt.Errorf("Unknown security %s", security)
	}

	multiMode := len(q["mode"]) > 0 && q["mode"][0] == "multi"
	transport := "raw"
	if len(q["type"]) > 0 {
		transport = q["type"][0]
	}
	var transSettings *serial.TypedMessage
	switch transport {
	case "raw":
		transSettings = serial.ToTypedMessage(&tcp.Config{})
	case "grpc":
		cfg := grpc.Config{
			MultiMode: multiMode,
		}
		if len(q["serviceName"]) > 0 {
			cfg.ServiceName = q["serviceName"][0]
		}
		transSettings = serial.ToTypedMessage(&cfg)
	default:
		return nil, fmt.Errorf("Unknown transport %s", transport)
	}
	var proxySettings *serial.TypedMessage
	switch up.Scheme {
	case "vless":
		proxySettings = serial.ToTypedMessage(&vlessOut.Config{
			Vnext: &protocol.ServerEndpoint{
				Address: addr,
				Port:    uport,
				User: &protocol.User{
					Account: serial.ToTypedMessage(&vless.Account{
						Id: up.User.Username(),
					}),
				},
			},
		})
	default:
		return nil, fmt.Errorf("Unknown protocol %s", transport)
	}
	outbound := core.OutboundHandlerConfig{
		SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{
			StreamSettings: &internet.StreamConfig{
				SecurityType:     seccfg.Type,
				ProtocolName:     transport,
				Address:          addr,
				Port:             uport,
				SecuritySettings: []*serial.TypedMessage{seccfg},
				TransportSettings: []*internet.TransportConfig{{
					ProtocolName: transport,
					Settings:     transSettings}},
			},
		}),
		ProxySettings: proxySettings}
	return &outbound, nil
}
