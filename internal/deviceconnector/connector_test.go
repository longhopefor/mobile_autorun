package deviceconnector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func usbConfig() Config {
	return Config{Mode: "usb", ServerURL: "ws://127.0.0.1:8080/device/connect", Serial: "phone", ADBBinary: "/path with spaces/adb", Token: "private-token"}
}

type phone struct {
	online              bool
	mapping, url, state string
	configs, forwards   int
	token               string
}

func (p *phone) run(_ context.Context, args ...string) (string, error) {
	switch strings.Join(args, " ") {
	case "get-state":
		if !p.online {
			return "", errors.New("offline")
		}
		return "device", nil
	case "reverse --list":
		return p.mapping, nil
	case "shell content query --uri content://com.mobilerun.portal/cloud/status":
		b, _ := json.Marshal(map[string]any{"status": "success", "result": map[string]string{"connectionState": p.state, "reverseConnectionUrl": p.url}})
		return "Row: 0 result=" + string(b), nil
	}
	if args[0] == "reverse" {
		p.forwards++
		p.mapping = "UsbFfs " + args[1] + " " + args[2]
		return "", nil
	}
	if len(args) > 3 && args[2] == "insert" {
		p.configs++
		for _, a := range args {
			if strings.HasPrefix(a, "url_base64:s:") {
				b, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(a, "url_base64:s:"))
				p.url = string(b)
			}
			if strings.HasPrefix(a, "token_base64:s:") {
				b, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(a, "token_base64:s:"))
				p.token = string(b)
			}
		}
		p.state = "CONNECTED"
		return "", nil // HarmonyOS empty success response
	}
	return "", errors.New("unexpected command")
}

func TestUSBRecoversLostTunnelWithoutRepeatingHealthyConnection(t *testing.T) {
	p := &phone{online: true}
	m := Manager{Config: usbConfig(), Run: p.run}
	for i := 0; i < 2; i++ {
		if _, err := m.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if p.configs != 1 || p.forwards != 1 || p.token != "private-token" {
		t.Fatalf("unnecessary reconnect/config: %+v", p)
	}
	p.online = false
	p.mapping = ""
	if _, err := m.Step(context.Background()); err == nil {
		t.Fatal("disconnected phone accepted")
	}
	if p.configs != 1 {
		t.Fatal("attempted configure while disconnected")
	}
	p.online = true
	if _, err := m.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.configs != 2 || p.forwards != 2 {
		t.Fatal("tunnel not recovered after reconnect")
	}
}

func TestUSBHelperStopsWhenPhoneSwitchesToWS(t *testing.T) {
	p := &phone{online: true}
	m := Manager{Config: usbConfig(), Run: p.run}
	if _, err := m.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.url = "ws://192.168.1.2:8080/device/connect?transport=ws"
	p.mapping = ""
	if _, err := m.Step(context.Background()); err == nil || err.Error() != "portal_route_changed_stop_usb_helper" {
		t.Fatal(err)
	}
	if p.configs != 1 || p.forwards != 1 {
		t.Fatal("overrode wireless configuration")
	}
}

func TestWSProvisionDoesNotNeedTunnelAndPreservesExistingToken(t *testing.T) {
	p := &phone{online: true, token: "existing"}
	c := usbConfig()
	c.Mode = "ws"
	c.ServerURL = "wss://workbench.example/device/connect"
	c.Token = ""
	m := Manager{Config: c, Run: p.run}
	if err := m.Serve(context.Background(), false, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if p.forwards != 0 || p.token != "existing" || p.url != "wss://workbench.example/device/connect?transport=ws" {
		t.Fatalf("bad WS setup: %+v", p)
	}
}

func TestConflictingTunnelIsNotOverwritten(t *testing.T) {
	p := &phone{online: true, mapping: "UsbFfs tcp:8080 tcp:9000"}
	m := Manager{Config: usbConfig(), Run: p.run}
	if _, err := m.Step(context.Background()); err == nil || err.Error() != "usb_tunnel_port_in_use" {
		t.Fatal(err)
	}
	if p.configs != 0 || p.forwards != 0 {
		t.Fatal("overrode another tunnel")
	}
}

func TestRejectUnsafeConnectionConfiguration(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:8080/device/connect", "ws://user:password@127.0.0.1/device/connect", "ws://127.0.0.1/device/connect?token=secret", "ws://127.0.0.1/wrong", "ws://127.0.0.1:99999/device/connect", "ws://192.168.1.1:8080/device/connect"} {
		c := usbConfig()
		c.ServerURL = raw
		if _, err := c.Route(); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	c := usbConfig()
	c.Mode = "ws"
	if _, err := c.Route(); err == nil {
		t.Fatal("wireless loopback accepted")
	}
}

func TestProviderFailureNeverReportsConfigurationSuccess(t *testing.T) {
	p := &phone{online: true}
	m := Manager{Config: usbConfig(), Run: func(ctx context.Context, args ...string) (string, error) {
		if len(args) > 2 && args[2] == "insert" {
			return "Error while accessing provider: private-token", nil
		}
		return p.run(ctx, args...)
	}}
	if _, err := m.Step(context.Background()); err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatal("unsafe error", err)
	}
}

func TestInsertStatusUsesResponseCodeInsteadOfMessageWords(t *testing.T) {
	if !insertSucceeded("Inserted content://com.mobilerun.portal/result?status=success&message=url%3Dws%3A%2F%2Ferror.example") {
		t.Fatal("success misclassified by message")
	}
	if insertSucceeded("Inserted content://com.mobilerun.portal/result?status=error&message=private") {
		t.Fatal("provider error accepted")
	}
}
