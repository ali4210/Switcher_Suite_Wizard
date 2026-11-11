//go:build linux

package main

import (
	"errors"
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

// ---------------------------------------------------------------------------
// SSH Server -- Linux pure helpers: package-manager detection, service-name
// selection, status-output parsing, and command argv builders. Each one
// below takes real-world-shaped sample output as a literal string, or a
// fake lookup/listUnitFiles closure in place of commandExists/runCommand,
// so nothing here executes a real command or touches real system state.
// ---------------------------------------------------------------------------

func TestDetectLinuxPackageManager(t *testing.T) {
	fakeExists := func(present ...string) func(string) bool {
		set := map[string]bool{}
		for _, p := range present {
			set[p] = true
		}
		return func(name string) bool { return set[name] }
	}

	cases := []struct {
		name    string
		present []string
		want    string
	}{
		{"apt available (Debian/Ubuntu/Kali)", []string{"apt", "dpkg"}, "apt"},
		{"dnf available (Fedora)", []string{"dnf", "rpm"}, "dnf"},
		{"yum available (older RHEL/CentOS)", []string{"yum"}, "yum"},
		{"pacman available (Arch)", []string{"pacman"}, "pacman"},
		{"zypper available (openSUSE)", []string{"zypper"}, "zypper"},
		{"apk available (Alpine)", []string{"apk"}, "apk"},
		{"apt preferred over others when multiple present", []string{"yum", "apt", "pacman"}, "apt"},
		{"none present", []string{}, ""},
	}
	for _, c := range cases {
		if got := detectLinuxPackageManager(fakeExists(c.present...)); got != c.want {
			t.Errorf("%s: detectLinuxPackageManager(...) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBuildSSHInstallArgs(t *testing.T) {
	cases := []struct {
		pm   string
		want []string
	}{
		{"apt", []string{"apt-get", "install", "-y", "openssh-server"}},
		{"dnf", []string{"dnf", "install", "-y", "openssh-server"}},
		{"yum", []string{"yum", "install", "-y", "openssh-server"}},
		{"pacman", []string{"pacman", "-S", "--noconfirm", "--needed", "openssh"}},
		{"zypper", []string{"zypper", "--non-interactive", "install", "openssh"}},
		{"apk", []string{"apk", "add", "openssh"}},
		{"unknown-pm", nil},
	}
	for _, c := range cases {
		got := buildSSHInstallArgs(c.pm)
		if len(got) != len(c.want) {
			t.Fatalf("buildSSHInstallArgs(%q) = %v, want %v", c.pm, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("buildSSHInstallArgs(%q)[%d] = %q, want %q", c.pm, i, got[i], c.want[i])
			}
		}
	}
}

func TestPickLinuxSSHServiceName(t *testing.T) {
	fakeListUnitFiles := func(installed ...string) func(string) (string, error) {
		set := map[string]bool{}
		for _, u := range installed {
			set[u] = true
		}
		return func(unit string) (string, error) {
			if set[unit] {
				// Real `systemctl list-unit-files <unit>` prints a
				// header + a row containing the unit name when found.
				return "UNIT FILE STATE\n" + unit + " enabled\n\n1 unit files listed.\n", nil
			}
			// Real systemctl exits non-zero with "0 unit files listed."
			// when the unit doesn't exist.
			return "0 unit files listed.\n", errors.New("exit status 1")
		}
	}

	cases := []struct {
		name      string
		installed []string
		want      string
	}{
		{"Debian/Ubuntu/Kali: ssh.service", []string{"ssh.service"}, "ssh.service"},
		{"RHEL/Fedora/Arch/openSUSE: sshd.service", []string{"sshd.service"}, "sshd.service"},
		{"ssh.service preferred when both installed", []string{"ssh.service", "sshd.service"}, "ssh.service"},
		{"neither installed", []string{}, ""},
	}
	for _, c := range cases {
		if got := pickLinuxSSHServiceName(fakeListUnitFiles(c.installed...)); got != c.want {
			t.Errorf("%s: pickLinuxSSHServiceName(...) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestParseSystemctlIsActiveOutput(t *testing.T) {
	cases := []struct {
		output string
		want   bool
	}{
		{"active\n", true},
		{"inactive\n", false},
		{"failed\n", false},
		{"activating\n", false},
		{"", false},
	}
	for _, c := range cases {
		if got := parseSystemctlIsActiveOutput(c.output); got != c.want {
			t.Errorf("parseSystemctlIsActiveOutput(%q) = %v, want %v", c.output, got, c.want)
		}
	}
}

func TestParseSystemctlIsEnabledOutput(t *testing.T) {
	cases := []struct {
		output string
		want   bool
	}{
		{"enabled\n", true},
		{"disabled\n", false},
		{"static\n", false},
		{"masked\n", false},
		{"", false},
	}
	for _, c := range cases {
		if got := parseSystemctlIsEnabledOutput(c.output); got != c.want {
			t.Errorf("parseSystemctlIsEnabledOutput(%q) = %v, want %v", c.output, got, c.want)
		}
	}
}

func TestParseSSHDConfigPorts(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   []int
	}{
		{
			name:   "default single port",
			output: "port 22\naddressfamily any\nlistenaddress 0.0.0.0:22\nlistenaddress [::]:22\n",
			want:   []int{22},
		},
		{
			name:   "custom non-standard port",
			output: "port 2222\naddressfamily any\n",
			want:   []int{2222},
		},
		{
			name:   "multiple port directives",
			output: "port 22\nport 2222\n",
			want:   []int{22, 2222},
		},
		{
			name:   "no port line",
			output: "addressfamily any\n",
			want:   nil,
		},
	}
	for _, c := range cases {
		got := parseSSHDConfigPorts(c.output)
		if len(got) != len(c.want) {
			t.Fatalf("%s: parseSSHDConfigPorts(...) = %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: parseSSHDConfigPorts(...)[%d] = %d, want %d", c.name, i, got[i], c.want[i])
			}
		}
	}
}

func TestParseListeningPorts(t *testing.T) {
	// Real `ss -H -ltn` output: State Recv-Q Send-Q LocalAddr:Port PeerAddr:Port
	output := "LISTEN 0 128 0.0.0.0:22 0.0.0.0:*\n" +
		"LISTEN 0 128 [::]:22 [::]:*\n" +
		"LISTEN 0 4096 127.0.0.1:631 0.0.0.0:*\n"

	got := parseListeningPorts(output)

	for _, port := range []int{22, 631} {
		if !got[port] {
			t.Errorf("parseListeningPorts(...) missing port %d, got %v", port, got)
		}
	}
	if got[2222] {
		t.Errorf("parseListeningPorts(...) unexpectedly reports port 2222 listening")
	}
}

func TestDetectLinuxFirewallTool(t *testing.T) {
	fakeExists := func(present ...string) func(string) bool {
		set := map[string]bool{}
		for _, p := range present {
			set[p] = true
		}
		return func(name string) bool { return set[name] }
	}

	cases := []struct {
		name    string
		present []string
		want    string
	}{
		{"ufw present (Debian/Ubuntu/Kali default)", []string{"ufw"}, "ufw"},
		{"firewalld present (RHEL/Fedora default)", []string{"firewall-cmd"}, "firewalld"},
		{"ufw preferred when both present", []string{"ufw", "firewall-cmd"}, "ufw"},
		{"neither present", []string{}, ""},
	}
	for _, c := range cases {
		if got := detectLinuxFirewallTool(fakeExists(c.present...)); got != c.want {
			t.Errorf("%s: detectLinuxFirewallTool(...) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestParseUfwStatusOutput(t *testing.T) {
	cases := []struct {
		name          string
		output        string
		port          int
		wantActive    bool
		wantAllowsSSH bool
	}{
		{
			name:          "inactive",
			output:        "Status: inactive\n",
			port:          22,
			wantActive:    false,
			wantAllowsSSH: false,
		},
		{
			name: "active, OpenSSH already allowed, checking the standard port",
			output: "Status: active\n\n" +
				"To                         Action      From\n" +
				"--                         ------      ----\n" +
				"OpenSSH                    ALLOW       Anywhere\n",
			port:          22,
			wantActive:    true,
			wantAllowsSSH: true,
		},
		{
			name: "active, literal 22/tcp rule, checking the standard port",
			output: "Status: active\n\n" +
				"To                         Action      From\n" +
				"--                         ------      ----\n" +
				"22/tcp                     ALLOW       Anywhere\n",
			port:          22,
			wantActive:    true,
			wantAllowsSSH: true,
		},
		{
			name: "active, SSH not allowed",
			output: "Status: active\n\n" +
				"To                         Action      From\n" +
				"--                         ------      ----\n" +
				"80/tcp                     ALLOW       Anywhere\n",
			port:          22,
			wantActive:    true,
			wantAllowsSSH: false,
		},
		{
			name: "active, custom port 2222 allowed",
			output: "Status: active\n\n" +
				"To                         Action      From\n" +
				"--                         ------      ----\n" +
				"2222/tcp                   ALLOW       Anywhere\n",
			port:          2222,
			wantActive:    true,
			wantAllowsSSH: true,
		},
		{
			name: "active, OpenSSH profile present but checking a custom port -- must NOT count as allowed",
			output: "Status: active\n\n" +
				"To                         Action      From\n" +
				"--                         ------      ----\n" +
				"OpenSSH                    ALLOW       Anywhere\n",
			port:          2222,
			wantActive:    true,
			wantAllowsSSH: false,
		},
		{
			// Regression test: "2222/tcp" contains "22/tcp" as a plain
			// substring. A naive strings.Contains check would wrongly
			// report port 22 as allowed here.
			name: "active, 2222/tcp rule present -- must NOT count as port 22 (substring false-positive)",
			output: "Status: active\n\n" +
				"To                         Action      From\n" +
				"--                         ------      ----\n" +
				"2222/tcp                   ALLOW       Anywhere\n",
			port:          22,
			wantActive:    true,
			wantAllowsSSH: false,
		},
		{
			// Same substring trap from the other side: "122/tcp" also
			// contains "22/tcp".
			name: "active, 122/tcp rule present -- must NOT count as port 22 (substring false-positive)",
			output: "Status: active\n\n" +
				"To                         Action      From\n" +
				"--                         ------      ----\n" +
				"122/tcp                    ALLOW       Anywhere\n",
			port:          22,
			wantActive:    true,
			wantAllowsSSH: false,
		},
		{
			// Checking port 2222 must not be fooled by 22/tcp or 122/tcp
			// rules either -- confirms the match is symmetric, not just
			// a one-sided "don't match 22 inside a longer number" fix.
			name: "active, 22/tcp and 122/tcp rules present -- must NOT count as port 2222",
			output: "Status: active\n\n" +
				"To                         Action      From\n" +
				"--                         ------      ----\n" +
				"22/tcp                     ALLOW       Anywhere\n" +
				"122/tcp                    ALLOW       Anywhere\n",
			port:          2222,
			wantActive:    true,
			wantAllowsSSH: false,
		},
		{
			name: "active, IPv6 variant of the literal port rule (ufw's '(v6)' suffix)",
			output: "Status: active\n\n" +
				"To                         Action      From\n" +
				"--                         ------      ----\n" +
				"22/tcp (v6)                ALLOW       Anywhere (v6)\n",
			port:          22,
			wantActive:    true,
			wantAllowsSSH: true,
		},
		{
			name: "active, IPv6 variant of the named OpenSSH profile",
			output: "Status: active\n\n" +
				"To                         Action      From\n" +
				"--                         ------      ----\n" +
				"OpenSSH (v6)               ALLOW       Anywhere (v6)\n",
			port:          22,
			wantActive:    true,
			wantAllowsSSH: true,
		},
	}
	for _, c := range cases {
		active, allowsSSH := parseUfwStatusOutput(c.output, c.port)
		if active != c.wantActive || allowsSSH != c.wantAllowsSSH {
			t.Errorf("%s: parseUfwStatusOutput(..., %d) = (active=%v, allowsSSH=%v), want (%v, %v)",
				c.name, c.port, active, allowsSSH, c.wantActive, c.wantAllowsSSH)
		}
	}
}

func TestParseFirewalldStateOutput(t *testing.T) {
	cases := []struct {
		output string
		want   bool
	}{
		{"running\n", true},
		{"not running\n", false},
		{"", false},
	}
	for _, c := range cases {
		if got := parseFirewalldStateOutput(c.output); got != c.want {
			t.Errorf("parseFirewalldStateOutput(%q) = %v, want %v", c.output, got, c.want)
		}
	}
}

func TestParseFirewalldServicesOutput(t *testing.T) {
	cases := []struct {
		output string
		want   bool
	}{
		{"dhcpv6-client ssh\n", true},
		{"dhcpv6-client\n", false},
		{"ssh\n", true},
		{"", false},
	}
	for _, c := range cases {
		if got := parseFirewalldServicesOutput(c.output); got != c.want {
			t.Errorf("parseFirewalldServicesOutput(%q) = %v, want %v", c.output, got, c.want)
		}
	}
}

func TestBuildFirewallAllowCommands(t *testing.T) {
	ufw := buildFirewallAllowCommands("ufw", 22)
	if len(ufw) != 1 || strings.Join(ufw[0], " ") != "ufw allow OpenSSH" {
		t.Errorf("buildFirewallAllowCommands(ufw, 22) = %v, want [[ufw allow OpenSSH]]", ufw)
	}

	ufwCustom := buildFirewallAllowCommands("ufw", 2222)
	if len(ufwCustom) != 1 || strings.Join(ufwCustom[0], " ") != "ufw allow 2222/tcp" {
		t.Errorf("buildFirewallAllowCommands(ufw, 2222) = %v, want [[ufw allow 2222/tcp]]", ufwCustom)
	}

	firewalld := buildFirewallAllowCommands("firewalld", 22)
	if len(firewalld) != 2 {
		t.Fatalf("buildFirewallAllowCommands(firewalld, 22) = %v, want 2 commands (add-service + reload)", firewalld)
	}
	if strings.Join(firewalld[0], " ") != "firewall-cmd --permanent --add-service=ssh" {
		t.Errorf("buildFirewallAllowCommands(firewalld, 22)[0] = %v", firewalld[0])
	}
	if strings.Join(firewalld[1], " ") != "firewall-cmd --reload" {
		t.Errorf("buildFirewallAllowCommands(firewalld, 22)[1] = %v", firewalld[1])
	}

	firewalldCustom := buildFirewallAllowCommands("firewalld", 2222)
	if len(firewalldCustom) != 2 || strings.Join(firewalldCustom[0], " ") != "firewall-cmd --permanent --add-port=2222/tcp" {
		t.Errorf("buildFirewallAllowCommands(firewalld, 2222) = %v, want --add-port=2222/tcp as the first command", firewalldCustom)
	}

	if got := buildFirewallAllowCommands("unknown", 22); got != nil {
		t.Errorf("buildFirewallAllowCommands(unknown, 22) = %v, want nil", got)
	}
}

func TestBuildFirewallRemoveCommands(t *testing.T) {
	ufw := buildFirewallRemoveCommands("ufw", 22)
	if len(ufw) != 1 || strings.Join(ufw[0], " ") != "ufw delete allow OpenSSH" {
		t.Errorf("buildFirewallRemoveCommands(ufw, 22) = %v, want [[ufw delete allow OpenSSH]]", ufw)
	}

	ufwCustom := buildFirewallRemoveCommands("ufw", 2222)
	if len(ufwCustom) != 1 || strings.Join(ufwCustom[0], " ") != "ufw delete allow 2222/tcp" {
		t.Errorf("buildFirewallRemoveCommands(ufw, 2222) = %v, want [[ufw delete allow 2222/tcp]]", ufwCustom)
	}

	firewalld := buildFirewallRemoveCommands("firewalld", 22)
	if len(firewalld) != 2 || strings.Join(firewalld[0], " ") != "firewall-cmd --permanent --remove-service=ssh" {
		t.Errorf("buildFirewallRemoveCommands(firewalld, 22) = %v, want --remove-service=ssh as the first command", firewalld)
	}

	firewalldCustom := buildFirewallRemoveCommands("firewalld", 2222)
	if len(firewalldCustom) != 2 || strings.Join(firewalldCustom[0], " ") != "firewall-cmd --permanent --remove-port=2222/tcp" {
		t.Errorf("buildFirewallRemoveCommands(firewalld, 2222) = %v, want --remove-port=2222/tcp as the first command", firewalldCustom)
	}

	if got := buildFirewallRemoveCommands("unknown", 22); got != nil {
		t.Errorf("buildFirewallRemoveCommands(unknown, 22) = %v, want nil", got)
	}
}

func TestParseFirewalldPortsOutput(t *testing.T) {
	cases := []struct {
		name   string
		output string
		port   int
		want   bool
	}{
		{"exact match", "2222/tcp 8080/tcp\n", 2222, true},
		{"no match", "8080/tcp\n", 2222, false},
		{"empty output", "", 2222, false},
		{"does not match the standard port by itself", "2222/tcp\n", 22, false},
	}
	for _, c := range cases {
		if got := parseFirewalldPortsOutput(c.output, c.port); got != c.want {
			t.Errorf("%s: parseFirewalldPortsOutput(%q, %d) = %v, want %v", c.name, c.output, c.port, got, c.want)
		}
	}
}

// getSSHServerStatus / enableSSHServer / disableSSHServer /
// ensureFirewallAllowsSSH themselves call runCommand/commandExists
// directly (systemctl, sshd, ssh-keygen, ss, ufw/firewall-cmd, and real
// package managers) with no injectable seam, matching this file's
// existing testability convention for launchDetached /
// runDetachedModeSwitch / detectDisplayManager / syncLinuxHostsFile /
// isSystemdResolvedManaged below. Per the project's safety rules, none of
// those five are exercised here or anywhere in this test suite -- only
// the pure parsing/building/selection logic above is, which is exactly
// what those five delegate every real decision to.

// launchDetached / runDetachedModeSwitch / detectDisplayManager /
// syncLinuxHostsFile / isSystemdResolvedManaged all either hardcode real
// system paths (/etc/X11/default-display-manager, /etc/hosts,
// /etc/resolv.conf, /run/systemd/resolve/...) or invoke systemd
// (systemctl/systemd-run/setsid) directly with no injectable seams. Per the
// audit's safety rules, they are intentionally NOT exercised here -- see
// FINAL_AUDIT_REPORT.md's testability-gap findings instead of adding a test
// that would read or write real system state.
