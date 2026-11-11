//go:build darwin

package main

import (
	"errors"
	"net"
	"testing"
)

// buildDarwinProxyEndpoint is a pure string builder (no filesystem or
// command execution), exercised directly for IPv4, hostname, and IPv6 --
// plus a full parseAndValidateProxy -> buildDarwinProxyEndpoint pipeline
// test confirming an injection attempt never reaches the builder at all,
// because parseAndValidateProxy rejects it first.
//
// This file is darwin-only (the production code it tests lives in
// system_darwin.go, which is also darwin-only), so it cannot run as part
// of this repo's `go test ./... -race` gate on a non-macOS machine. It is
// cross-compile-checked instead via `GOOS=darwin go vet ./...`, which
// type-checks it without executing it.

func TestBuildDarwinProxyEndpoint(t *testing.T) {
	cases := []struct {
		name, host, port, want string
	}{
		{"IPv4", "127.0.0.1", "8080", "127.0.0.1:8080"},
		{"hostname", "proxy.company.com", "3128", "proxy.company.com:3128"},
		{"IPv6", "::1", "8080", "[::1]:8080"},
		{"IPv6 full", "2001:db8::1", "3128", "[2001:db8::1]:3128"},
	}
	for _, c := range cases {
		if got := buildDarwinProxyEndpoint(c.host, c.port); got != c.want {
			t.Errorf("%s: buildDarwinProxyEndpoint(%q, %q) = %q, want %q", c.name, c.host, c.port, got, c.want)
		}
	}
}

// TestSetSystemProxyPipeline_Darwin exercises the same host->endpoint
// pipeline setSystemProxy runs, end to end, for every raw input a user
// could type -- IPv4, hostname, and bracketed IPv6 -- confirming the
// endpoint is always the correct net.JoinHostPort form, and that
// injection attempts are rejected by parseAndValidateProxy before an
// endpoint is ever built at all. networksetup itself is never invoked
// here: this only exercises the pure parsing/building path, never the
// real `networksetup` command.
func TestSetSystemProxyPipeline_Darwin(t *testing.T) {
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
		endpoint := buildDarwinProxyEndpoint(host, port)
		if endpoint != c.wantEndpoint {
			t.Errorf("pipeline(%q) endpoint = %q, want %q", c.input, endpoint, c.wantEndpoint)
		}
		// net.JoinHostPort directly, for cross-checking against the
		// builder -- they must agree.
		if want := net.JoinHostPort(host, port); endpoint != want {
			t.Errorf("pipeline(%q) endpoint = %q, disagrees with net.JoinHostPort = %q", c.input, endpoint, want)
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
			t.Errorf("parseAndValidateProxy(%q) expected error (so it never reaches buildDarwinProxyEndpoint), got nil", in)
		}
	}
}

// setSystemProxy itself, getCurrentProxy, and disableSystemProxy all shell
// out to networksetup/gsettings-equivalents with no injectable seam, and
// getNetworkInterfaces/getDarwinNetworkService read real interface state.
// Per the project's safety rules, none of those are exercised here --
// only the pure endpoint-building logic above is.

// ---------------------------------------------------------------------------
// SSH Server -- macOS status-output parsers. Each is exercised against a
// realistic sample systemsetup/launchctl/sshd_config/lsof output string.
// Nothing here invokes systemsetup, launchctl, or lsof for real.
// ---------------------------------------------------------------------------

func TestParseDarwinRemoteLoginOutput(t *testing.T) {
	cases := []struct {
		output string
		want   bool
	}{
		{"Remote Login: On\n", true},
		{"Remote Login: Off\n", false},
		{"", false},
	}
	for _, c := range cases {
		if got := parseDarwinRemoteLoginOutput(c.output); got != c.want {
			t.Errorf("parseDarwinRemoteLoginOutput(%q) = %v, want %v", c.output, got, c.want)
		}
	}
}

func TestParseDarwinLaunchctlLoadedOutput(t *testing.T) {
	cases := []struct {
		name   string
		output string
		err    error
		want   bool
	}{
		{"loaded", "system/com.openssh.sshd = {\n\tactive count = 0\n", nil, true},
		{"not found", "", errors.New("Could not find service \"com.openssh.sshd\" in domain for system"), false},
		{"empty output even with nil error", "", nil, false},
	}
	for _, c := range cases {
		if got := parseDarwinLaunchctlLoadedOutput(c.output, c.err); got != c.want {
			t.Errorf("%s: parseDarwinLaunchctlLoadedOutput(%q, %v) = %v, want %v", c.name, c.output, c.err, got, c.want)
		}
	}
}

func TestParseDarwinSSHDConfigPort(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   int
	}{
		{"default port, explicit directive", "Port 22\nAddressFamily any\n", 22},
		{"custom non-standard port", "Port 2222\n", 2222},
		{"no Port line falls back to 22", "AddressFamily any\n", defaultDarwinSSHPort},
		{"empty config falls back to 22", "", defaultDarwinSSHPort},
	}
	for _, c := range cases {
		if got := parseDarwinSSHDConfigPort(c.output); got != c.want {
			t.Errorf("%s: parseDarwinSSHDConfigPort(...) = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestParseDarwinListeningOutput(t *testing.T) {
	cases := []struct {
		output string
		want   bool
	}{
		{"sshd 123 root 3u IPv4 0x1 0t0 TCP *:ssh (LISTEN)\n", true},
		{"", false},
		{"   \n", false},
	}
	for _, c := range cases {
		if got := parseDarwinListeningOutput(c.output); got != c.want {
			t.Errorf("parseDarwinListeningOutput(%q) = %v, want %v", c.output, got, c.want)
		}
	}
}

// TestIsDarwinVirtualInterface exercises the predicate extracted from
// getNetworkInterfaces' previously-inline filter (N8's own reference
// implementation that Linux/Windows now mirror).
func TestIsDarwinVirtualInterface(t *testing.T) {
	cases := []struct {
		iface string
		want  bool
	}{
		{"lo0", true},
		{"", true},
		{"bridge0", true},
		{"bridge100", true},
		{"utun0", true},
		{"utun3", true},
		{"llw0", true},
		{"awdl0", true},
		{"en0", false},
		{"en1", false},
	}
	for _, c := range cases {
		if got := isDarwinVirtualInterface(c.iface); got != c.want {
			t.Errorf("isDarwinVirtualInterface(%q) = %v, want %v", c.iface, got, c.want)
		}
	}
}
