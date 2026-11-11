package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DiagnosticStep represents a single network audit test and its auto-heal status
type DiagnosticStep struct {
	Name    string
	Passed  bool
	Details string
	FixLog  string
}

const commandTimeout = 25 * time.Second
const previousHostnameFile = ".switcher_previous_hostname"
const globalCliRecordFile = ".switcher_cli_command"

func runCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return string(output), fmt.Errorf(
			"command timed out after %s: %s",
			commandTimeout,
			name,
		)
	}

	if err != nil {
		return string(output), err
	}

	return string(output), nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func validateHostname(name string) error {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 63 {
		return errors.New("hostname must be between 1 and 63 characters")
	}

	reg := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?$`)
	if !reg.MatchString(name) {
		return errors.New("invalid hostname: alphanumeric characters and hyphens only (cannot start or end with a hyphen)")
	}
	return nil
}

func savePreviousHostname(currentName string) {
	currentName = strings.TrimSpace(currentName)
	if currentName != "" {
		_ = os.WriteFile(previousHostnameFile, []byte(currentName), 0644)
	}
}

func getPreviousHostname() string {
	data, err := os.ReadFile(previousHostnameFile)
	if err != nil {
		curr, _ := os.Hostname()
		return curr
	}
	val := strings.TrimSpace(string(data))
	if val == "" {
		curr, _ := os.Hostname()
		return curr
	}
	return val
}

func validateCliCommand(cmdName string) error {
	cmdName = strings.TrimSpace(cmdName)
	if len(cmdName) < 2 || len(cmdName) > 32 {
		return errors.New("CLI command alias must be between 2 and 32 characters")
	}
	reg := regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)
	if !reg.MatchString(cmdName) {
		return errors.New("CLI command must contain only letters, numbers, hyphens, and underscores")
	}
	return nil
}

// reservedCliCommands blocks well-known system/shell command names from
// ever being claimed as a Global CLI alias, even on a machine where that
// name doesn't currently resolve on PATH.
var reservedCliCommands = map[string]bool{
	"sudo": true, "su": true, "ssh": true, "scp": true, "sftp": true,
	"bash": true, "sh": true, "zsh": true, "fish": true, "cmd": true, "powershell": true, "pwsh": true,
	"top": true, "htop": true, "ls": true, "dir": true, "cat": true, "cp": true, "mv": true, "rm": true,
	"dd": true, "mkfs": true, "fdisk": true, "diskpart": true, "format": true,
	"systemctl": true, "service": true, "init": true, "reboot": true, "shutdown": true, "halt": true,
	"login": true, "passwd": true, "chmod": true, "chown": true, "kill": true, "killall": true, "taskkill": true,
	"curl": true, "wget": true, "nc": true, "ncat": true,
	"python": true, "python3": true, "perl": true, "ruby": true, "node": true, "npm": true,
	"git": true, "docker": true, "apt": true, "apt-get": true, "yum": true, "dnf": true, "pacman": true, "brew": true,
	"explorer": true, "regedit": true, "net": true, "netsh": true, "reg": true,
	"iptables": true, "firewall-cmd": true, "nmcli": true, "networksetup": true, "scutil": true, "hostnamectl": true,
}

func isReservedCliCommand(name string) bool {
	return reservedCliCommands[strings.ToLower(strings.TrimSpace(name))]
}

// cliShimMarker is embedded as a comment in every wrapper script / shim
// this tool writes for Global CLI Integration, so a later enable/disable
// can tell "a file we created" apart from "something that was already
// there" before overwriting or deleting it.
const cliShimMarker = "Switcher Suite Wizard Global CLI Shim"

// fileHasCliShimMarker reports whether the file at path is one this tool
// created (and is therefore safe to overwrite or remove). A missing or
// unreadable file is treated as "not ours" (false), never as an error.
func fileHasCliShimMarker(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if len(data) > 4096 {
		data = data[:4096]
	}
	return strings.Contains(string(data), cliShimMarker)
}

// legacyUnixShimTemplate and legacyWindowsShimTemplate are the exact
// fmt.Sprintf templates this tool wrote for its Global CLI shims *before*
// the shim-marker comment was introduced. "%s" marks where the executable
// path was substituted in (one or more times); everything else is fixed,
// literal template text. Kept here only so legacyShimPatterns can match
// against them -- never used to write a new shim.
const legacyUnixShimTemplate = "#!/bin/bash\nexec sudo \"%s\" \"$@\"\n"

const legacyWindowsShimTemplate = `@echo off
net session >nul 2>&1
if %errorLevel% == 0 (
    "%s" %*
) else (
    if "%~1"=="" (
        powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process -FilePath '%s' -Verb RunAs"
    ) else (
        powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process -FilePath '%s' -ArgumentList '%*' -Verb RunAs"
    )
)
`

// legacyShimTemplateToPattern turns a legacy fmt.Sprintf template (one that
// substitutes only "%s" -- always the executable path -- into otherwise
// fixed text) into a regexp that matches any rendering of that template,
// treating every "%s" position as a wildcard that can't itself contain a
// quote or newline (an executable path never legitimately does).
func legacyShimTemplateToPattern(template string) *regexp.Regexp {
	parts := strings.Split(template, "%s")
	escaped := make([]string, len(parts))
	for i, p := range parts {
		escaped[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile("^" + strings.Join(escaped, `[^'"\n]*`) + "$")
}

var legacyShimPatterns = []*regexp.Regexp{
	legacyShimTemplateToPattern(legacyUnixShimTemplate),
	legacyShimTemplateToPattern(legacyWindowsShimTemplate),
}

// isLegacyCliShimContent reports whether content is byte-for-byte what one
// of this tool's pre-marker releases would have written for some
// executable path. It deliberately does not try to recover or check that
// path -- only that the surrounding, fixed template text matches exactly.
func isLegacyCliShimContent(content string) bool {
	for _, pattern := range legacyShimPatterns {
		if pattern.MatchString(content) {
			return true
		}
	}
	return false
}

// isOwnedCliShimFile reports whether the file at path is one this tool
// created -- either a current shim (carries cliShimMarker) or a shim from
// before the marker existed (matches a legacy template exactly). A missing,
// unreadable, or merely similar-looking foreign file is never "owned".
func isOwnedCliShimFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)

	markerScanWindow := content
	if len(markerScanWindow) > 4096 {
		markerScanWindow = markerScanWindow[:4096]
	}
	if strings.Contains(markerScanWindow, cliShimMarker) {
		return true
	}

	return isLegacyCliShimContent(content)
}

// checkCliAliasSafeToClaim decides whether cmdName is safe for Global CLI
// Integration to create or overwrite as a global alias. It rejects:
//  1. anything on the reserved system-command list, regardless of whether
//     it currently resolves on this machine;
//  2. any name that already resolves via exec.LookPath to a file that
//     isn't one this tool created (current or legacy shim);
//  3. any of the concrete install-directory paths the caller is about to
//     write to that already exist and aren't one this tool created.
//
// A name is only ever considered safe to claim if every check above either
// finds nothing, or finds only files this tool created itself.
func checkCliAliasSafeToClaim(cmdName string, candidatePaths []string) error {
	cmdName = strings.TrimSpace(cmdName)

	if isReservedCliCommand(cmdName) {
		return fmt.Errorf("%q is a reserved system command name and cannot be used as a CLI alias", cmdName)
	}

	if resolved, err := exec.LookPath(cmdName); err == nil {
		if !isOwnedCliShimFile(resolved) {
			return fmt.Errorf("a command named %q already exists on PATH (%s) and was not created by Switcher Suite Wizard; choose a different alias", cmdName, resolved)
		}
	}

	for _, p := range candidatePaths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return fmt.Errorf("%q already exists as a directory, not a file; choose a different alias", p)
		}
		if !isOwnedCliShimFile(p) {
			return fmt.Errorf("%q already exists and was not created by Switcher Suite Wizard; choose a different alias or remove it manually first", p)
		}
	}

	return nil
}

func getActiveCliCommand() string {
	data, err := os.ReadFile(globalCliRecordFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func saveActiveCliCommand(cmdName string) {
	_ = os.WriteFile(globalCliRecordFile, []byte(strings.TrimSpace(cmdName)), 0644)
}

func removeActiveCliRecord() {
	_ = os.Remove(globalCliRecordFile)
}

func stripShellProfileBlock(filePath string) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return
	}

	lines := strings.Split(string(content), "\n")
	var newLines []string
	inBlock := false

	for _, line := range lines {
		if strings.Contains(line, "# >>> SWITCHER SUITE CLI BLOCK >>>") {
			inBlock = true
			continue
		}
		if strings.Contains(line, "# <<< SWITCHER SUITE CLI BLOCK <<<") {
			inBlock = false
			continue
		}
		if !inBlock {
			newLines = append(newLines, line)
		}
	}

	_ = os.WriteFile(filePath, []byte(strings.Join(newLines, "\n")), 0644)
}

func appendShellProfileBlock(filePath, commandAlias, execPath string) {
	stripShellProfileBlock(filePath)

	block := fmt.Sprintf("\n# >>> SWITCHER SUITE CLI BLOCK >>>\nalias %s='sudo %s'\n# <<< SWITCHER SUITE CLI BLOCK <<<\n", commandAlias, execPath)

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(block)
}

func pingHost(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return "Invalid target host."
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("ping", "-n", "2", target)
	} else {
		cmd = exec.Command("ping", "-c", "2", "-W", "2", target)
	}

	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		return "Ping failed: " + err.Error()
	}
	return string(output)
}

func testDNSResolution(server, testDomain string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return "Invalid server address"
	}

	// 1. Layer 7 Query via UDP Port 53
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, "udp", net.JoinHostPort(server, "53"))
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	ips, err := r.LookupHost(ctx, testDomain)
	if err == nil && len(ips) > 0 {
		return fmt.Sprintf("DNS Query SUCCESS -> Resolved %s to [%s]", testDomain, strings.Join(ips, ", "))
	}

	// 2. CLI Tool Fallback
	if commandExists("host") {
		out, err := runCommand("host", "-W", "2", testDomain, server)
		if err == nil {
			return strings.TrimSpace(out)
		}
	} else if commandExists("nslookup") {
		out, err := runCommand("nslookup", "-timeout=2", testDomain, server)
		if err == nil {
			return strings.TrimSpace(out)
		}
	}

	return fmt.Sprintf("DNS Query FAILED: Unable to resolve %s through %s (Port 53 unreachable or blocked)", testDomain, server)
}

func getAllSystemIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return []string{"Error retrieving network interfaces: " + err.Error()}
	}

	var results []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue // Interface down
		}
		addrs, err := iface.Addrs()
		if err != nil || len(addrs) == 0 {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if ok && !ipNet.IP.IsLoopback() {
				vType := "IPv4"
				if ipNet.IP.To4() == nil {
					vType = "IPv6"
				}
				results = append(results, fmt.Sprintf("%-12s │ %-4s │ %s", iface.Name, vType, ipNet.String()))
			}
		}
	}

	if len(results) == 0 {
		return []string{"No active IP addresses found on host."}
	}
	return results
}

func validateCIDR(value string) (string, error) {
	value = strings.TrimSpace(value)

	ip, network, err := net.ParseCIDR(value)
	if err != nil {
		return "", errors.New(
			"enter a valid IPv4 address with CIDR, for example 192.168.1.50/24",
		)
	}

	ipv4 := ip.To4()
	if ipv4 == nil {
		return "", errors.New("only IPv4 addresses are currently supported")
	}

	prefix, bits := network.Mask.Size()
	if bits != 32 || prefix < 0 || prefix > 32 {
		return "", errors.New("CIDR prefix must be between /0 and /32")
	}

	return fmt.Sprintf("%s/%d", ipv4.String(), prefix), nil
}

func normalizeAddress(ipValue, maskValue string) (string, error) {
	ipValue = strings.TrimSpace(ipValue)
	maskValue = strings.TrimSpace(maskValue)

	ip := net.ParseIP(ipValue)
	if ip == nil || ip.To4() == nil {
		return "", errors.New("enter a valid IPv4 address")
	}

	prefix, err := parsePrefix(maskValue)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/%d", ip.To4().String(), prefix), nil
}

func parsePrefix(maskOrPrefix string) (int, error) {
	maskOrPrefix = strings.TrimSpace(maskOrPrefix)

	if strings.HasPrefix(maskOrPrefix, "/") {
		prefix, err := strconv.Atoi(strings.TrimPrefix(maskOrPrefix, "/"))
		if err != nil || prefix < 0 || prefix > 32 {
			return 0, errors.New("CIDR prefix must be between /0 and /32")
		}

		return prefix, nil
	}

	ip := net.ParseIP(maskOrPrefix)
	if ip == nil || ip.To4() == nil {
		return 0, errors.New(
			"enter a subnet mask such as 255.255.255.0 or CIDR prefix such as /24",
		)
	}

	mask := net.IPMask(ip.To4())
	prefix, bits := mask.Size()

	if bits != 32 || prefix < 0 {
		return 0, errors.New(
			"subnet mask must be contiguous, for example 255.255.255.0",
		)
	}

	return prefix, nil
}

func prefixToMask(prefix int) (string, error) {
	if prefix < 0 || prefix > 32 {
		return "", errors.New("CIDR prefix must be between 0 and 32")
	}

	mask := net.CIDRMask(prefix, 32)
	return net.IP(mask).String(), nil
}

func splitCIDR(cidr string) (string, int, error) {
	normalized, err := validateCIDR(cidr)
	if err != nil {
		return "", 0, err
	}

	ip, network, err := net.ParseCIDR(normalized)
	if err != nil {
		return "", 0, err
	}

	prefix, _ := network.Mask.Size()

	return ip.String(), prefix, nil
}

func validateGateway(gateway string) error {
	gateway = strings.TrimSpace(gateway)

	if gateway == "" {
		return nil
	}

	ip := net.ParseIP(gateway)
	if ip == nil || ip.To4() == nil {
		return errors.New(
			"default gateway must be a valid IPv4 address or left blank",
		)
	}

	return nil
}

func splitDNSInput(value string) []string {
	rawServers := strings.Split(value, ",")
	servers := make([]string, 0, len(rawServers))

	for _, rawServer := range rawServers {
		server := strings.TrimSpace(rawServer)

		if server != "" {
			servers = append(servers, server)
		}
	}

	return servers
}

func validateDNSServers(servers []string) error {
	if len(servers) == 0 {
		return errors.New("enter at least one DNS server")
	}

	for _, server := range servers {
		ip := net.ParseIP(strings.TrimSpace(server))

		if ip == nil {
			return fmt.Errorf("%q is not a valid IP address", server)
		}
	}

	return nil
}

// validProxyHostPattern allowlists the characters a hostname or IPv4 address
// can legally contain. Anything else (quotes, backticks, $(), ;, |, &, etc.)
// is rejected before the value ever reaches a shell-sourced profile script
// or a PowerShell -Command string.
var validProxyHostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,253}[A-Za-z0-9])?$`)

func parseAndValidateProxy(input string) (string, string, error) {
	input = strings.TrimSpace(input)
	input = strings.TrimPrefix(input, "http://")
	input = strings.TrimPrefix(input, "https://")

	if input == "" {
		return "", "", errors.New("proxy endpoint cannot be blank")
	}

	// net.SplitHostPort (rather than a bare strings.Split on ":") understands
	// bracketed IPv6 literals like [::1]:8080, and correctly rejects an
	// unbracketed address with more than one colon as ambiguous.
	host, port, err := net.SplitHostPort(input)
	if err != nil {
		return "", "", errors.New("format must be host:port (e.g. 127.0.0.1:8080, proxy.company.com:3128, or [::1]:8080 for IPv6)")
	}

	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)

	if host == "" {
		return "", "", errors.New("proxy host cannot be blank")
	}

	// net.ParseIP is a strict, non-regex parser: a string it accepts can only
	// be a well-formed IPv4/IPv6 literal, never shell/PowerShell metacharacters,
	// so accepting it here (needed for IPv6, which contains ':' and therefore
	// never matches validProxyHostPattern) introduces no injection risk.
	if net.ParseIP(host) == nil && !validProxyHostPattern.MatchString(host) {
		return "", "", errors.New("proxy host may only contain letters, numbers, dots, and hyphens (no quotes, spaces, or shell characters), or be a valid IPv6 literal")
	}

	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return "", "", fmt.Errorf("invalid port %q: must be a number between 1 and 65535", port)
	}

	return host, port, nil
}

func outputOrSuccess(output, successMessage string) string {
	output = strings.TrimSpace(output)

	if output == "" {
		return Green + "[✓] " + successMessage + NC
	}

	return Green + "[✓] " + successMessage + NC + "\n\n" + output
}

func commandError(operation, output string, err error) string {
	output = strings.TrimSpace(output)

	if output == "" {
		return Red + "[!] " + operation + ": " + err.Error() + NC
	}

	return Red + "[!] " + operation + ": " + err.Error() + "\n\n" + output + NC
}

const proxyRecordFile = ".switcher_proxy_config"

func saveActiveProxyRecord(endpoint string) {
	_ = os.WriteFile(proxyRecordFile, []byte(strings.TrimSpace(endpoint)), 0644)
}

func getActiveProxyRecord() string {
	data, err := os.ReadFile(proxyRecordFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func removeActiveProxyRecord() {
	_ = os.Remove(proxyRecordFile)
}

// probeProxyEndpoint attempts a real-time TCP socket handshake with the proxy target
func probeProxyEndpoint(host, port string) (bool, string) {
	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	target := net.JoinHostPort(host, port)

	timeout := 2500 * time.Millisecond
	conn, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			return false, fmt.Sprintf("Port %s is CLOSED (no active proxy listener or daemon running on %s).", port, host)
		}
		if strings.Contains(err.Error(), "i/o timeout") {
			return false, fmt.Sprintf("Connection TIMED OUT after 2.5s (host %s unreachable or firewall dropped packet).", host)
		}
		return false, fmt.Sprintf("Socket error: %s", err.Error())
	}
	_ = conn.Close()

	return true, fmt.Sprintf("Socket probe SUCCESS: Active listener detected on %s.", target)
}

// windowsCliShimAllowedArg is the only argument the Windows Global CLI shim
// ever forwards across the UAC elevation hop, mirroring allowedElevationArgs
// in main.go. It's baked into the shim as a fixed literal rather than ever
// being derived from whatever the caller typed (cmd.exe's "%*"), so the
// PowerShell command string built below never contains unvalidated input.
const windowsCliShimAllowedArg = "--cli-shell"

// buildWindowsCliShim renders the .cmd/.bat shim Global CLI Integration
// installs on Windows. It's kept OS-build-tag-free (pure string templating,
// no Windows API calls) specifically so it can be unit tested on any
// platform.
//
// The already-elevated branch ("%s" %*) forwards all arguments directly to
// the trusted executable as plain argv -- cmd.exe doesn't reinterpret that
// as a further command, so it isn't a shell-injection vector. The
// not-yet-elevated branch used to splice the raw "%*" into a PowerShell
// -ArgumentList string literal, which was: a value containing a single
// quote could break out of it and run arbitrary PowerShell during the UAC
// hop. Instead, the rendered batch script itself only ever compares the
// first argument against windowsCliShimAllowedArg with a plain string
// equality test, and uses that *fixed Go-string-literal* flag -- never the
// compared value -- as the forwarded argument; any other input (no args,
// extra args, or anything else) elevates with no arguments at all.
func buildWindowsCliShim(execPath string) string {
	quotedPath := powerShellSingleQuote(execPath)

	return fmt.Sprintf(`@echo off
REM %s
net session >nul 2>&1
if %%errorLevel%% == 0 (
    "%s" %%*
) else (
    if "%%~1"=="%s" (
        if "%%~2"=="" (
            powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process -FilePath %s -ArgumentList '%s' -Verb RunAs"
        ) else (
            powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process -FilePath %s -Verb RunAs"
        )
    ) else (
        powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process -FilePath %s -Verb RunAs"
    )
)
`, cliShimMarker, execPath, windowsCliShimAllowedArg, quotedPath, windowsCliShimAllowedArg, quotedPath, quotedPath)
}

// ---------------------------------------------------------------------------
// SSH Server (Install / Enable) -- shared status type and menu-support
// helpers. Each system_<os>.go file defines the same four functions
// (getSSHServerStatus, enableSSHServer, disableSSHServer,
// ensureFirewallAllowsSSH) for its own OS; the menu flow that calls them
// lives in sshServerMenu below, in a later commit, once all three OS
// implementations exist.
// ---------------------------------------------------------------------------

// SSHServerStatus is the cross-platform snapshot getSSHServerStatus returns.
// ListeningPort is 0 when not currently listening. ServiceName is the
// systemd unit name on Linux ("ssh.service"/"sshd.service"), the Windows
// service name ("sshd"), or empty where the concept doesn't apply (macOS).
// FirewallTool is empty when no separate firewall step is needed for that
// OS (e.g. macOS), in which case the menu never offers the firewall prompt.
type SSHServerStatus struct {
	Installed         bool
	Enabled           bool
	Running           bool
	ListeningPort     int
	ServiceName       string
	FirewallTool      string
	FirewallActive    bool
	FirewallAllowsSSH bool
	Detail            string
}

// sshServerNeedsSetup reports whether enableSSHServer actually has to
// change anything, versus the server already being installed, enabled,
// and running -- i.e. whether the "ensure enabled" path is a real no-op.
// Pure and OS-agnostic, so it's the one piece of the idempotency decision
// that's directly unit-testable without executing anything.
func sshServerNeedsSetup(status SSHServerStatus) bool {
	return !status.Installed || !status.Enabled || !status.Running
}

// sshServerNeedsFirewallPrompt reports whether the menu should ask the
// separate firewall-allow confirmation: only when that OS has a firewall
// concept for this feature (FirewallTool != "") and it's active but
// doesn't already allow SSH. Never true when FirewallTool is empty
// (e.g. macOS, where ensureFirewallAllowsSSH is a no-op).
func sshServerNeedsFirewallPrompt(status SSHServerStatus) bool {
	return status.FirewallTool != "" && status.FirewallActive && !status.FirewallAllowsSSH
}

// sshPortOrFallback validates port as a real TCP port (1-65535); if it's
// not (typically 0, meaning the real listening port could not be
// confirmed), returns fallback (each OS's own standard SSH port
// constant) and true, so callers can tell the user a fallback was used
// instead of silently acting on an unconfirmed port.
func sshPortOrFallback(port, fallback int) (validPort int, usedFallback bool) {
	if port >= 1 && port <= 65535 {
		return port, false
	}
	return fallback, true
}

// sshFirewallRuleRecordFile remembers which firewall rule (if any) THIS
// tool created for the SSH server, so disabling it later can remove
// exactly that rule and never a rule that already existed before this
// tool touched the firewall (e.g. one created by the OS itself, or by
// the user). Written only on a successful NEW rule creation -- never
// when ensureFirewallAllowsSSH finds a matching rule already in place.
const sshFirewallRuleRecordFile = ".switcher_ssh_firewall_rule"

func saveSSHFirewallRuleRecord(tool string, port int) {
	_ = os.WriteFile(sshFirewallRuleRecordFile, []byte(fmt.Sprintf("%s %d", tool, port)), 0644)
}

// getSSHFirewallRuleRecord reads back the record saved by
// saveSSHFirewallRuleRecord. ok is false if no record exists, or if its
// content doesn't parse as "<tool> <port 1-65535>" -- a corrupt/foreign
// record is treated as "we don't know we created anything", never as
// license to remove an arbitrary rule.
func getSSHFirewallRuleRecord() (tool string, port int, ok bool) {
	data, err := os.ReadFile(sshFirewallRuleRecordFile)
	if err != nil {
		return "", 0, false
	}
	fields := strings.Fields(strings.TrimSpace(string(data)))
	if len(fields) != 2 {
		return "", 0, false
	}
	p, err := strconv.Atoi(fields[1])
	if err != nil || p < 1 || p > 65535 {
		return "", 0, false
	}
	return fields[0], p, true
}

func removeSSHFirewallRuleRecord() {
	_ = os.Remove(sshFirewallRuleRecordFile)
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// formatSSHServerStatus renders the status report shown at the top of
// sshServerMenu, shared across all three OSes since SSHServerStatus
// itself is OS-agnostic.
func formatSSHServerStatus(s SSHServerStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Installed: %s\n", yesNo(s.Installed))
	fmt.Fprintf(&b, "Enabled:   %s\n", yesNo(s.Enabled))
	fmt.Fprintf(&b, "Running:   %s\n", yesNo(s.Running))
	if s.ListeningPort > 0 {
		fmt.Fprintf(&b, "Listening: port %d\n", s.ListeningPort)
	} else {
		b.WriteString("Listening: not confirmed\n")
	}
	if s.FirewallTool != "" {
		fmt.Fprintf(&b, "Firewall:  %s (active: %s, allows SSH: %s)\n", s.FirewallTool, yesNo(s.FirewallActive), yesNo(s.FirewallAllowsSSH))
	}
	if s.Detail != "" {
		fmt.Fprintf(&b, "Note: %s\n", s.Detail)
	}
	return strings.TrimRight(b.String(), "\n")
}

// sshServerMenu is the shared entry point wired to the main menu's
// "SSH Server (Install / Enable)" entry. It calls only the four
// functions every system_<os>.go file defines identically
// (getSSHServerStatus/enableSSHServer/disableSSHServer/
// ensureFirewallAllowsSSH) -- no platform picker, the OS the binary was
// built for is the only one in play.
func sshServerMenu() {
	for {
		status, err := getSSHServerStatus()
		if err != nil {
			showHeader("SSH SERVER")
			printBoxRow(" "+FgRose+"[!] Failed to read SSH server status: "+err.Error()+Reset, "\n")
			pauseAction()
			return
		}

		enableLabel := "Install / Enable SSH Server"
		if !sshServerNeedsSetup(status) {
			enableLabel = "Re-Confirm SSH Server Is Enabled"
		}

		choice := SelectFromOptions(
			"SSH SERVER (INSTALL / ENABLE)",
			formatSSHServerStatus(status)+"\nSelect Action:",
			[]string{enableLabel, "Disable SSH Server", "Return to Main Menu"},
		)

		switch choice {
		case 0:
			sshServerEnableFlow(status)
		case 1:
			sshServerDisableFlow(status)
		default:
			return
		}
	}
}

func sshServerEnableFlow(status SSHServerStatus) {
	showHeader("SSH SERVER")

	if !sshServerNeedsSetup(status) {
		printBoxRow(" "+FgEmerald+"[✓] SSH server is already installed, enabled, and running."+Reset, "\n")
		if status.ListeningPort > 0 {
			printBoxRow(" "+FgCyan+fmt.Sprintf("[i] Listening on port %d.", status.ListeningPort)+Reset, "\n")
		}
		printBoxBlankRow("\n")
		if !confirmAction("Re-run the enable steps anyway, to make sure everything is still correctly configured?") {
			pauseAction()
			return
		}
	} else {
		printBoxRow(" "+FgAmber+"[i] This will:"+Reset, "\n")
		if !status.Installed {
			printBoxRow("     - Install the OpenSSH server", "\n")
		}
		printBoxRow("     - Validate the SSH daemon configuration", "\n")
		printBoxRow("     - Enable and start the SSH service", "\n")
		printBoxBlankRow("\n")
		if !confirmAction("Proceed with installing/enabling the SSH server?") {
			showHeader("ACTION CANCELLED")
			printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
			pauseAction()
			return
		}
	}

	if !requirePrivilege() {
		return
	}

	showHeader("CONFIGURING SSH SERVER")
	res, err := enableSSHServer()
	if err != nil {
		printBoxRow(" "+FgRose+"[!] Error: "+err.Error()+Reset, "\n")
		pauseAction()
		return
	}
	printBoxRow(" "+FgEmerald+"[✓] "+res+Reset, "\n")

	newStatus, _ := getSSHServerStatus()

	if sshServerNeedsFirewallPrompt(newStatus) {
		printBoxBlankRow("\n")
		printBoxRow(" "+FgAmber+Bold+"[!] "+newStatus.FirewallTool+" is active and does not currently allow SSH."+Reset, "\n")
		if confirmAction("Allow SSH through " + newStatus.FirewallTool + " now?") {
			fwRes, fwErr := ensureFirewallAllowsSSH(newStatus.ListeningPort)
			if fwErr != nil {
				printBoxRow(" "+FgRose+"[!] Firewall error: "+fwErr.Error()+Reset, "\n")
			} else {
				printBoxRow(" "+FgEmerald+"[✓] "+fwRes+Reset, "\n")
			}
		} else {
			printBoxRow(" "+FgAmber+"[i] Firewall left unchanged -- SSH may not be reachable from other hosts until you allow it."+Reset, "\n")
		}
	}

	printBoxBlankRow("\n")
	printBoxRow(" "+FgCyan+Bold+"[i] Next step:"+Reset, "\n")
	printBoxRow("     ssh "+getCurrentUser()+"@<this machine's IP, listed below>", "\n")
	for _, ip := range getAllSystemIPs() {
		printBoxRow("     "+ip, "\n")
	}
	pauseAction()
}

func sshServerDisableFlow(status SSHServerStatus) {
	showHeader("DISABLE SSH SERVER")

	if !status.Installed && !status.Enabled && !status.Running {
		printBoxRow(" "+FgAmber+"[i] SSH server is not installed or enabled -- nothing to disable."+Reset, "\n")
		pauseAction()
		return
	}

	if sshConn := os.Getenv("SSH_CONNECTION"); sshConn != "" {
		printBoxRow(" "+FgRose+Bold+"[!] WARNING: this session appears to be connected over SSH right now"+Reset, "\n")
		printBoxRow("     ("+FgMuted+"SSH_CONNECTION="+sshConn+Reset+").", "\n")
		printBoxRow("     Disabling the SSH server now could lock you out if you have no other", "\n")
		printBoxRow("     way to reach this machine.", "\n")
		printBoxBlankRow("\n")
		if !confirmAction("Disable SSH anyway, even though this session is connected over SSH right now?") {
			showHeader("ACTION CANCELLED")
			printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
			pauseAction()
			return
		}
	} else if !confirmAction("Disable the SSH server? It will stop and no longer start automatically.") {
		showHeader("ACTION CANCELLED")
		printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	showHeader("DISABLING SSH SERVER")
	res, err := disableSSHServer()
	if err != nil {
		printBoxRow(" "+FgRose+"[!] Error: "+err.Error()+Reset, "\n")
		pauseAction()
		return
	}
	printBoxRow(" "+FgEmerald+"[✓] "+res+Reset, "\n")

	fwRes, fwErr := removeSSHFirewallRule()
	if fwErr != nil {
		printBoxRow(" "+FgRose+"[!] Firewall cleanup error: "+fwErr.Error()+Reset, "\n")
	} else {
		printBoxRow(" "+FgCyan+"[i] "+fwRes+Reset, "\n")
	}
	pauseAction()
}
