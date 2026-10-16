// Package deviceconnector provisions Portal routes and maintains local USB tunnels.
package deviceconnector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Mode      string `json:"mode"`
	ServerURL string `json:"server_url"`
	Token     string `json:"token,omitempty"`
	ADBBinary string `json:"adb_binary"`
	Serial    string `json:"serial"`
	PhonePort int    `json:"phone_port,omitempty"`
}

type Route struct {
	PhoneURL            string
	PhonePort, HostPort int
}

func (c Config) Route() (Route, error) {
	u, err := url.Parse(c.ServerURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "ws" && u.Scheme != "wss") || u.User != nil || u.Fragment != "" || u.Path != "/device/connect" {
		return Route{}, errors.New("valid_ws_device_connect_url_required")
	}
	if c.Mode != "usb" && c.Mode != "ws" {
		return Route{}, errors.New("mode_must_be_usb_or_ws")
	}
	if c.ADBBinary == "" || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`).MatchString(c.Serial) {
		return Route{}, errors.New("adb_binary_and_fixed_serial_required")
	}
	if strings.ContainsAny(c.Token, "\r\n") {
		return Route{}, errors.New("invalid_token")
	}
	if u.Port() != "" {
		port, e := strconv.Atoi(u.Port())
		if e != nil || port < 1 || port > 65535 {
			return Route{}, errors.New("invalid_server_port")
		}
	}
	q := u.Query()
	// Credentials belong in the private token field, never in a connection URL.
	for k := range q {
		if k != "transport" {
			return Route{}, errors.New("only_transport_query_allowed")
		}
	}
	r := Route{}
	if c.Mode == "usb" {
		if u.Scheme != "ws" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
			return r, errors.New("usb_requires_local_loopback_ws_server")
		}
		r.HostPort = 80
		if u.Port() != "" {
			r.HostPort, err = strconv.Atoi(u.Port())
		}
		r.PhonePort = c.PhonePort
		if r.PhonePort == 0 {
			r.PhonePort = r.HostPort
		}
		if err != nil || r.HostPort < 1 || r.HostPort > 65535 || r.PhonePort < 1 || r.PhonePort > 65535 {
			return r, errors.New("invalid_tunnel_port")
		}
		u.Host = "127.0.0.1:" + strconv.Itoa(r.PhonePort)
		q.Set("transport", "usb_reverse")
	} else {
		if u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1" {
			return r, errors.New("ws_requires_phone_reachable_server_address")
		}
		q.Set("transport", "ws")
	}
	u.RawQuery = q.Encode()
	r.PhoneURL = u.String()
	return r, nil
}

type Runner func(context.Context, ...string) (string, error)

func ADBRunner(c Config) Runner {
	return func(ctx context.Context, args ...string) (string, error) {
		// exec arguments only; no host shell. Remote shell arguments below are fixed or base64.
		cmd := exec.CommandContext(ctx, c.ADBBinary, append([]string{"-s", c.Serial}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", errors.New("adb_command_failed")
		}
		if len(out) > 128<<10 {
			return "", errors.New("adb_output_too_large")
		}
		return string(out), nil
	}
}

type Manager struct {
	Config        Config
	Run           Runner
	configured    bool
	lastReconnect time.Time
}

func (m *Manager) configure(ctx context.Context, route Route) error {
	args := []string{"shell", "content", "insert", "--uri", "content://com.mobilerun.portal/configure_reverse_connection", "--bind", "url_base64:s:" + base64.StdEncoding.EncodeToString([]byte(route.PhoneURL)), "--bind", "enabled:b:true"}
	if m.Config.Token != "" {
		args = append(args, "--bind", "token_base64:s:"+base64.StdEncoding.EncodeToString([]byte(m.Config.Token)))
	}
	out, err := m.Run(ctx, args...)
	// HarmonyOS can return an empty stdout on success. Never print provider output (credentials).
	if err != nil || !insertSucceeded(out) {
		return errors.New("portal_configuration_failed")
	}
	m.configured = true
	m.lastReconnect = time.Now()
	return nil
}

func insertSucceeded(out string) bool {
	if strings.TrimSpace(out) == "" {
		return true
	}
	match := regexp.MustCompile(`content://com\.mobilerun\.portal/result[^\s]*`).FindString(out)
	u, err := url.Parse(match)
	return err == nil && match != "" && u.Query().Get("status") == "success"
}

type Status struct {
	ConnectionState string `json:"connectionState"`
	URL             string `json:"reverseConnectionUrl"`
}

func parseStatus(out string) (Status, error) {
	var status Status
	start := strings.Index(out, "result=")
	if start < 0 {
		return status, errors.New("portal_status_unavailable")
	}
	var response struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out[start+7:])), &response) != nil || response.Status != "success" {
		return status, errors.New("portal_status_unavailable")
	}
	var nested string
	if json.Unmarshal(response.Result, &nested) == nil {
		response.Result = json.RawMessage(nested)
	}
	if json.Unmarshal(response.Result, &status) != nil || status.URL == "" || status.ConnectionState == "" {
		return status, errors.New("portal_status_unavailable")
	}
	return status, nil
}

// Step repairs the tunnel only; it never submits or replays device actions.
func (m *Manager) Step(ctx context.Context) (string, error) {
	route, err := m.Config.Route()
	if err != nil {
		return "", err
	}
	state, err := m.Run(ctx, "get-state")
	if err != nil || strings.TrimSpace(state) != "device" {
		return "", errors.New("usb_device_offline_or_unauthorized")
	}
	missing := false
	if m.Config.Mode == "usb" {
		out, e := m.Run(ctx, "reverse", "--list")
		if e != nil {
			return "", e
		}
		missing = true
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[1] == fmt.Sprintf("tcp:%d", route.PhonePort) {
				if fields[2] != fmt.Sprintf("tcp:%d", route.HostPort) {
					return "", errors.New("usb_tunnel_port_in_use")
				}
				missing = false
			}
		}
	}
	// After the initial setup, a user changing Portal to WS must not be overridden.
	var status Status
	if m.configured {
		out, e := m.Run(ctx, "shell", "content", "query", "--uri", "content://com.mobilerun.portal/cloud/status")
		if e != nil {
			return "", e
		}
		status, e = parseStatus(out)
		if e != nil {
			return "", e
		}
		if status.URL != route.PhoneURL {
			return "", errors.New("portal_route_changed_stop_usb_helper")
		}
		if status.ConnectionState == "UNAUTHORIZED" || status.ConnectionState == "BAD_REQUEST" || status.ConnectionState == "LIMIT_EXCEEDED" {
			return "", errors.New("portal_connection_rejected")
		}
	}
	if missing {
		if _, err = m.Run(ctx, "reverse", fmt.Sprintf("tcp:%d", route.PhonePort), fmt.Sprintf("tcp:%d", route.HostPort)); err != nil {
			return "", err
		}
	}
	if !m.configured || missing || ((status.ConnectionState == "DISCONNECTED" || status.ConnectionState == "ERROR") && time.Since(m.lastReconnect) >= 30*time.Second) {
		if err = m.configure(ctx, route); err != nil {
			return "", err
		}
		return "Portal 接入已配置，等待工作台确认在线", nil
	}
	if status.ConnectionState == "CONNECTED" {
		return "Portal 已连接", nil
	}
	return "Portal 正在连接", nil
}

func (m *Manager) Serve(ctx context.Context, once bool, report func(string)) error {
	if _, err := m.Config.Route(); err != nil {
		return err
	}
	if m.Run == nil {
		m.Run = ADBRunner(m.Config)
	}
	previous := ""
	for {
		job, cancel := context.WithTimeout(ctx, 15*time.Second)
		status, err := m.Step(job)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			status = err.Error()
			if once || status == "portal_route_changed_stop_usb_helper" || status == "portal_connection_rejected" || status == "usb_tunnel_port_in_use" {
				return err
			}
		}
		if status != previous {
			report(status)
			previous = status
		}
		if once || m.Config.Mode == "ws" {
			return err
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
