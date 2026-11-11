//go:build windows

package main

import (
	"net"
	"strings"
	"testing"
)

// buildWindowsProxyEndpoint / buildWindowsNetshProxyArgs /
// buildWindowsProxyRegistryScript are pure builders (no command execution
// or registry access), exercised directly for IPv4, hostname, and IPv6 --
// plus a full parseAndValidateProxy -> builders pipeline test confirming
// an injection attempt never reaches any of them, because
// parseAndValidateProxy rejects it first.
//
// This file is windows-only (the production code it tests lives in
// system_windows.go, which is also windows-only), so it cannot run as
// part of this repo's `go test ./... -race` gate on a non-Windows
// machine. It is cross-compile-checked instead via
// `GOOS=windows go vet ./...`, which type-checks it without executing it.

func TestBuildWindowsProxyEndpoint(t *testing.T) {
	cases := []struct {
		name, host, port, want string
	}{
		{"IPv4", "127.0.0.1", "8080", "127.0.0.1:8080"},
		{"hostname", "proxy.company.com", "3128", "proxy.company.com:3128"},
		{"IPv6", "::1", "8080", "[::1]:8080"},
		{"IPv6 full", "2001:db8::1", "3128", "[2001:db8::1]:3128"},
	}
	for _, c := range cases {
		if got := buildWindowsProxyEndpoint(c.host, c.port); got != c.want {
			t.Errorf("%s: buildWindowsProxyEndpoint(%q, %q) = %q, want %q", c.name, c.host, c.port, got, c.want)
		}
	}
}

func TestBuildWindowsNetshProxyArgs(t *testing.T) {
	cases := []struct {
		name, endpoint string
		want           []string
	}{
		{"IPv4", "127.0.0.1:8080", []string{"winhttp", "set", "proxy", "proxy-server=127.0.0.1:8080"}},
		{"IPv6", "[::1]:8080", []string{"winhttp", "set", "proxy", "proxy-server=[::1]:8080"}},
	}
	for _, c := range cases {
		got := buildWindowsNetshProxyArgs(c.endpoint)
		if len(got) != len(c.want) {
			t.Fatalf("%s: buildWindowsNetshProxyArgs(%q) = %v, want %v", c.name, c.endpoint, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: buildWindowsNetshProxyArgs(%q)[%d] = %q, want %q", c.name, c.endpoint, i, got[i], c.want[i])
			}
		}
	}
}

func TestBuildWindowsProxyRegistryScript(t *testing.T) {
	const regPath = `HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

	cases := []struct {
		name, endpoint string
	}{
		{"IPv4", "127.0.0.1:8080"},
		{"IPv6", "[::1]:8080"},
	}
	for _, c := range cases {
		script := buildWindowsProxyRegistryScript(regPath, c.endpoint)
		if !strings.Contains(script, `-Value "`+c.endpoint+`"`) {
			t.Errorf("%s: buildWindowsProxyRegistryScript(...) missing ProxyServer value %q in:\n%s", c.name, c.endpoint, script)
		}
		if !strings.Contains(script, "ProxyEnable") || !strings.Contains(script, "ProxyOverride") {
			t.Errorf("%s: buildWindowsProxyRegistryScript(...) missing expected registry keys in:\n%s", c.name, script)
		}
	}
}

// TestSetSystemProxyPipeline_Windows exercises the same
// host->endpoint->netsh-args/registry-script pipeline setSystemProxy
// runs, end to end, for every raw input a user could type -- IPv4,
// hostname, and bracketed IPv6 -- confirming the endpoint reaching every
// builder is always the correct net.JoinHostPort form, and that
// injection attempts are rejected by parseAndValidateProxy before an
// endpoint is ever built at all. netsh/powershell are never invoked
// here: this only exercises the pure parsing/building path.
func TestSetSystemProxyPipeline_Windows(t *testing.T) {
	const regPath = `HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

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
		endpoint := buildWindowsProxyEndpoint(host, port)
		if endpoint != c.wantEndpoint {
			t.Errorf("pipeline(%q) endpoint = %q, want %q", c.input, endpoint, c.wantEndpoint)
			continue
		}
		if want := net.JoinHostPort(host, port); endpoint != want {
			t.Errorf("pipeline(%q) endpoint = %q, disagrees with net.JoinHostPort = %q", c.input, endpoint, want)
		}

		args := buildWindowsNetshProxyArgs(endpoint)
		if args[len(args)-1] != "proxy-server="+endpoint {
			t.Errorf("pipeline(%q) netsh args = %v, want last arg proxy-server=%s", c.input, args, endpoint)
		}

		script := buildWindowsProxyRegistryScript(regPath, endpoint)
		if !strings.Contains(script, `-Value "`+endpoint+`"`) {
			t.Errorf("pipeline(%q) registry script missing endpoint %q:\n%s", c.input, endpoint, script)
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
			t.Errorf("parseAndValidateProxy(%q) expected error (so it never reaches any Windows proxy builder), got nil", in)
		}
	}
}

// setSystemProxy itself, getCurrentProxy, and disableSystemProxy all shell
// out to netsh/powershell with no injectable seam, and getNetworkInterfaces
// reads real interface state. Per the project's safety rules, none of
// those are exercised here -- only the pure builders above are.
