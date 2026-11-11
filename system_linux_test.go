//go:build linux

package main

import (
	"net"
	"strings"
	"testing"
)

// buildGuiSwitchScript / buildTtySwitchScript are pure string builders (no
// filesystem or command execution), so they're safe to exercise directly
// for every dm value including the empty-string fallback case.

func TestBuildGuiSwitchScript(t *testing.T) {
	cases := []struct {
		dm       string
		wantSubs []string
	}{
		{"", []string{"systemctl isolate graphical.target"}},
		{"lightdm.service", []string{"systemctl start lightdm.service"}},
		{"gdm.service", []string{"systemctl start gdm.service"}},
		{"gdm3.service", []string{"systemctl start gdm3.service"}},
		{"sddm.service", []string{"systemctl start sddm.service"}},
	}

	for _, c := range cases {
		script := buildGuiSwitchScript(c.dm)

		for _, want := range c.wantSubs {
			if !strings.Contains(script, want) {
				t.Errorf("buildGuiSwitchScript(%q) missing %q in:\n%s", c.dm, want, script)
			}
		}
		if !strings.Contains(script, "chvt 7") {
			t.Errorf("buildGuiSwitchScript(%q) missing chvt fallback chain", c.dm)
		}
		if !strings.Contains(script, switchLogFile) {
			t.Errorf("buildGuiSwitchScript(%q) does not log to %s", c.dm, switchLogFile)
		}
		if !strings.Contains(script, "switch-to-gui") {
			t.Errorf("buildGuiSwitchScript(%q) missing switch-to-gui marker", c.dm)
		}
		// Must not start whatever display manager it was NOT given.
		if c.dm != "" {
			if !strings.Contains(script, "systemctl start "+c.dm) {
				t.Errorf("buildGuiSwitchScript(%q) should target exactly the detected DM", c.dm)
			}
			if strings.Contains(script, "systemctl isolate graphical.target") {
				t.Errorf("buildGuiSwitchScript(%q) should not fall back to isolate graphical.target when a DM was detected", c.dm)
			}
		}
	}
}

func TestBuildTtySwitchScript(t *testing.T) {
	cases := []struct {
		dm       string
		wantSubs []string
	}{
		{"", []string{"systemctl isolate multi-user.target"}},
		{"lightdm.service", []string{"systemctl stop lightdm.service"}},
		{"gdm.service", []string{"systemctl stop gdm.service"}},
		{"gdm3.service", []string{"systemctl stop gdm3.service"}},
		{"sddm.service", []string{"systemctl stop sddm.service"}},
	}

	for _, c := range cases {
		script := buildTtySwitchScript(c.dm)

		for _, want := range c.wantSubs {
			if !strings.Contains(script, want) {
				t.Errorf("buildTtySwitchScript(%q) missing %q in:\n%s", c.dm, want, script)
			}
		}
		if !strings.Contains(script, "getty@tty1.service") {
			t.Errorf("buildTtySwitchScript(%q) missing getty@tty1 enable/restart", c.dm)
		}
		if !strings.Contains(script, "chvt 1") {
			t.Errorf("buildTtySwitchScript(%q) missing chvt 1 fallback", c.dm)
		}
		if !strings.Contains(script, switchLogFile) {
			t.Errorf("buildTtySwitchScript(%q) does not log to %s", c.dm, switchLogFile)
		}
		if !strings.Contains(script, "switch-to-tty") {
			t.Errorf("buildTtySwitchScript(%q) missing switch-to-tty marker", c.dm)
		}
		if c.dm != "" {
			if !strings.Contains(script, "systemctl stop "+c.dm) {
				t.Errorf("buildTtySwitchScript(%q) should target exactly the detected DM", c.dm)
			}
			if strings.Contains(script, "systemctl isolate multi-user.target") {
				t.Errorf("buildTtySwitchScript(%q) should not fall back to isolate multi-user.target when a DM was detected", c.dm)
			}
		}
	}
}

// buildLinuxProxyEnvContent is a pure string builder (no filesystem or
// command execution), exercised directly for IPv4, hostname, and IPv6
// endpoints -- plus a full parseAndValidateProxy -> net.JoinHostPort ->
// buildLinuxProxyEnvContent pipeline test confirming an injection attempt
// never reaches the builder at all, because parseAndValidateProxy rejects
// it first.

func TestBuildLinuxProxyEnvContent(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		wantSubs []string
	}{
		{
			name:     "IPv4",
			endpoint: "127.0.0.1:8080",
			wantSubs: []string{
				`http_proxy="http://127.0.0.1:8080/"`,
				`https_proxy="http://127.0.0.1:8080/"`,
				`all_proxy="socks5://127.0.0.1:8080/"`,
				`HTTP_PROXY="http://127.0.0.1:8080/"`,
				`HTTPS_PROXY="http://127.0.0.1:8080/"`,
				`ALL_PROXY="socks5://127.0.0.1:8080/"`,
			},
		},
		{
			name:     "hostname",
			endpoint: "proxy.company.com:3128",
			wantSubs: []string{
				`http_proxy="http://proxy.company.com:3128/"`,
				`all_proxy="socks5://proxy.company.com:3128/"`,
			},
		},
		{
			name:     "IPv6",
			endpoint: "[::1]:8080",
			wantSubs: []string{
				`http_proxy="http://[::1]:8080/"`,
				`https_proxy="http://[::1]:8080/"`,
				`all_proxy="socks5://[::1]:8080/"`,
				`HTTP_PROXY="http://[::1]:8080/"`,
				`HTTPS_PROXY="http://[::1]:8080/"`,
				`ALL_PROXY="socks5://[::1]:8080/"`,
			},
		},
	}

	for _, c := range cases {
		content := buildLinuxProxyEnvContent(c.endpoint)
		for _, want := range c.wantSubs {
			if !strings.Contains(content, want) {
				t.Errorf("%s: buildLinuxProxyEnvContent(%q) missing %q in:\n%s", c.name, c.endpoint, want, content)
			}
		}
		if !strings.Contains(content, `no_proxy="localhost,127.0.0.1,localaddress,.localdomain.com"`) {
			t.Errorf("%s: buildLinuxProxyEnvContent(%q) missing no_proxy line", c.name, c.endpoint)
		}
	}
}

// TestSetSystemProxyPipeline_Linux exercises the same host->endpoint->env
// pipeline setSystemProxy runs, end to end, for every raw input a user
// could type -- IPv4, hostname, and bracketed IPv6 -- confirming the
// endpoint reaching buildLinuxProxyEnvContent is always the correct
// net.JoinHostPort form, and that injection attempts are rejected by
// parseAndValidateProxy before an endpoint is ever built at all.
func TestSetSystemProxyPipeline_Linux(t *testing.T) {
	valid := []struct {
		input, wantEndpoint string
	}{
		{"127.0.0.1:8080", "127.0.0.1:8080"},
		{"proxy.company.com:3128", "proxy.company.com:3128"},
		{"[::1]:8080", "[::1]:8080"},
		{"[2001:db8::1]:3128", "[2001:db8::1]:3128"},
	}
	for _, c := range valid {
		host, port, err := parseAndValidateProxy(c.input)
		if err != nil {
			t.Errorf("parseAndValidateProxy(%q) unexpected error: %v", c.input, err)
			continue
		}
		endpoint := net.JoinHostPort(host, port)
		if endpoint != c.wantEndpoint {
			t.Errorf("pipeline(%q) endpoint = %q, want %q", c.input, endpoint, c.wantEndpoint)
			continue
		}
		content := buildLinuxProxyEnvContent(endpoint)
		if !strings.Contains(content, `http_proxy="http://`+endpoint+`/"`) {
			t.Errorf("pipeline(%q) env content missing endpoint %q:\n%s", c.input, endpoint, content)
		}
	}

	injection := []string{
		"[::1; rm -rf /]:8080",
		"[$(id)::1]:8080",
		"[`id`::1]:8080",
		"a;id:8080",
		"$(id):8080",
	}
	for _, in := range injection {
		if _, _, err := parseAndValidateProxy(in); err == nil {
			t.Errorf("parseAndValidateProxy(%q) expected error (so it never reaches buildLinuxProxyEnvContent), got nil", in)
		}
	}
}

// launchDetached / runDetachedModeSwitch / detectDisplayManager /
// syncLinuxHostsFile / isSystemdResolvedManaged all either hardcode real
// system paths (/etc/X11/default-display-manager, /etc/hosts,
// /etc/resolv.conf, /run/systemd/resolve/...) or invoke systemd
// (systemctl/systemd-run/setsid) directly with no injectable seams. Per the
// audit's safety rules, they are intentionally NOT exercised here -- see
// FINAL_AUDIT_REPORT.md's testability-gap findings instead of adding a test
// that would read or write real system state.
