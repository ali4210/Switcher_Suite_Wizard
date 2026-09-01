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

func parseAndValidateProxy(input string) (string, string, error) {
	input = strings.TrimSpace(input)
	input = strings.TrimPrefix(input, "http://")
	input = strings.TrimPrefix(input, "https://")

	if input == "" {
		return "", "", errors.New("proxy endpoint cannot be blank")
	}

	parts := strings.Split(input, ":")
	if len(parts) != 2 {
		return "", "", errors.New("format must be host:port (e.g. 127.0.0.1:8080 or proxy.company.com:3128)")
	}

	host := strings.TrimSpace(parts[0])
	port := strings.TrimSpace(parts[1])

	if host == "" {
		return "", "", errors.New("proxy host cannot be blank")
	}

	if strings.ContainsAny(host, " \t\r\n/\\") {
		return "", "", errors.New("proxy host cannot contain whitespace or slashes")
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
