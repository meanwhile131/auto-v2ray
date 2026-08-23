package main

import (
	"log"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"encoding/base64"
	"encoding/hex"
	"github.com/xtls/xray-core/app/dispatcher"
	xlog "github.com/xtls/xray-core/app/log"
	"github.com/xtls/xray-core/app/proxyman"
	clog "github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/proxy/socks"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/grpc"
	"github.com/xtls/xray-core/transport/internet/reality"
)

func main() {
	inbound := core.InboundHandlerConfig{
		Tag: "localproxy",
		ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
			PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(9000)}},
			Listen:   net.NewIPOrDomain(net.ParseAddress("127.0.0.1")),
		}),
		ProxySettings: serial.ToTypedMessage(&socks.ServerConfig{}),
	}
	outbound, err := parseOutboundURL(os.Args[1])

	if err != nil {
		log.Fatal(err)
	}
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
		},
		Inbound:  []*core.InboundHandlerConfig{&inbound},
		Outbound: []*core.OutboundHandlerConfig{outbound},
	}
	i, err := core.New(&config)
	if err != nil {
		log.Fatal(err)
	}
	err = i.Start()
	if err != nil {
		log.Fatal(err)
	}
	defer i.Close()
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	err = i.Close()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Exiting")
}

func parseOutboundURL(u string) (*core.OutboundHandlerConfig, error) {
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
	pbk, err := base64.RawURLEncoding.DecodeString(q["pbk"][0])
	if err != nil {
		return nil, err
	}
	sid, err := hex.DecodeString(q["sid"][0])
	if err != nil {
		return nil, err
	}
	seccfg := serial.ToTypedMessage(&reality.Config{
		PublicKey:   pbk,
		Fingerprint: q["fp"][0],
		ShortId:     sid,
		ServerName:  q["sni"][0],
	})
	outbound := core.OutboundHandlerConfig{
		Tag: "out1",
		SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{
			StreamSettings: &internet.StreamConfig{
				SecurityType: seccfg.Type,
				ProtocolName: q["type"][0],
				Address:      addr,
				Port:         uport,
				SecuritySettings: []*serial.TypedMessage{seccfg},
				TransportSettings: []*internet.TransportConfig{{
					ProtocolName: q["type"][0],
					Settings: serial.ToTypedMessage(&grpc.Config{
						ServiceName: q["serviceName"][0],
						MultiMode:   q["mode"][0] == "multi",
					}),
				}},
			},
		}),
		ProxySettings: serial.ToTypedMessage(&outbound.Config{
			Vnext: &protocol.ServerEndpoint{
				Address: addr,
				Port:    uport,
				User: &protocol.User{
					Account: serial.ToTypedMessage(&vless.Account{
						Id: up.User.Username(),
					}),
				},
			},
		}),
	}
	return &outbound, nil
}
