package parse

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/url"
	"strconv"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/proxy/vless"
	vlessout "github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/grpc"
	"github.com/xtls/xray-core/transport/internet/reality"
	"github.com/xtls/xray-core/transport/internet/tcp"
)

func URLFile(body io.Reader) ([]*core.OutboundHandlerConfig, error) {
	scanner := bufio.NewScanner(body)
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
		outbound, err := OutboundURL(line)
		if err != nil {
			log.Printf("Failed to parse %s: %s", line, err)
			continue
		}
		outbound.Tag = fmt.Sprintf("out%d", i)
		outbounds = append(outbounds, outbound)
		i++
	}
	err := scanner.Err()
	if err != nil {
		return nil, err
	}
	return outbounds, nil
}

func OutboundURL(u string) (*core.OutboundHandlerConfig, error) {
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

	seccfg, err := createSecurityConfig(q)
	if err != nil {
		return nil, err
	}

	transport := "raw"
	if len(q["type"]) > 0 {
		transport = q["type"][0]
	}
	transSettings, err := createTransportConfig(transport, q)
	if err != nil {
		return nil, err
	}

	proxySettings, err := createProxySettings(up, addr, uport)
	if err != nil {
		return nil, err
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

func createProxySettings(up *url.URL, addr *net.IPOrDomain, uport uint32) (*serial.TypedMessage, error) {
	var cfg *serial.TypedMessage
	switch up.Scheme {
	case "vless":
		cfg = serial.ToTypedMessage(&vlessout.Config{
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
		return nil, fmt.Errorf("Unknown scheme %s", up.Scheme)
	}
	return cfg, nil
}

func createTransportConfig(transport string, q url.Values) (*serial.TypedMessage, error) {
	multiMode := len(q["mode"]) > 0 && q["mode"][0] == "multi"
	var cfg *serial.TypedMessage
	switch transport {
	case "raw":
		cfg = serial.ToTypedMessage(&tcp.Config{})
	case "grpc":
		rpc := grpc.Config{
			MultiMode: multiMode,
		}
		if len(q["serviceName"]) > 0 {
			rpc.ServiceName = q["serviceName"][0]
		}
		cfg = serial.ToTypedMessage(&rpc)
	default:
		return nil, fmt.Errorf("Unknown transport %s", transport)
	}
	return cfg, nil
}

func createSecurityConfig(q url.Values) (*serial.TypedMessage, error) {
	security := ""
	if len(q["security"]) > 0 {
		security = q["security"][0]
	}
	var cfg *serial.TypedMessage
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
		cfg = serial.ToTypedMessage(&reality.Config{
			PublicKey:   pbk,
			Fingerprint: fp,
			ShortId:     sid,
			ServerName:  q["sni"][0],
		})
	default:
		return nil, fmt.Errorf("Unknown security \"%s\"", security)
	}
	return cfg, nil
}
