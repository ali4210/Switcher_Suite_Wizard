//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func execCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

func isElevated() bool {
	return os.Geteuid() == 0
}

func setSystemHostname(newName string) (string, error) {
	curr, _ := os.Hostname()
	savePreviousHostname(curr)

	// Update HostName, LocalHostName, and ComputerName via Apple scutil
	if out, err := runCommand("scutil", "--set", "HostName", newName); err != nil {
		return commandError("Failed to set HostName via scutil", out, err), err
	}
	_, _ = runCommand("scutil", "--set", "LocalHostName", newName)
	_, _ = runCommand("scutil", "--set", "ComputerName", newName)

	// Live kernel update
	_ = exec.Command("hostname", newName).Run()

	// Safely synchronize /etc/hosts without destroying custom entries
	syncDarwinHostsFile(newName)

	return fmt.Sprintf("macOS HostName, LocalHostName, and ComputerName set to '%s'.", newName), nil
}

func syncDarwinHostsFile(hostname string) {
	data, err := os.ReadFile("/etc/hosts")
	if err != nil {
		hostsData := fmt.Sprintf("127.0.0.1\tlocalhost %s\n::1\t\tlocalhost ip6-localhost ip6-loopback\n", hostname)
		_ = os.WriteFile("/etc/hosts", []byte(hostsData), 0644)
		return
	}

	lines := strings.Split(string(data), "\n")
	var newLines []string
	updated := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "127.0.0.1") {
			fields := strings.Fields(trimmed)
			hasHost := false
			for _, f := range fields[1:] {
				if f == hostname {
					hasHost = true
					break
				}
			}
			if !hasHost {
				newLines = append(newLines, trimmed+" "+hostname)
			} else {
				newLines = append(newLines, line)
			}
			updated = true
		} else {
			newLines = append(newLines, line)
		}
	}

	if !updated {
		newLines = append([]string{fmt.Sprintf("127.0.0.1\tlocalhost %s", hostname)}, newLines...)
	}

	_ = os.WriteFile("/etc/hosts", []byte(strings.Join(newLines, "\n")), 0644)
}

func getCurrentDNS(iface string) string {
	service := getDarwinNetworkService(iface)
	if service != "" {
		output, err := runCommand("networksetup", "-getdnsservers", service)
		if err == nil {
			clean := strings.TrimSpace(output)
			if clean != "" && !strings.Contains(clean, "There aren't any DNS Servers") {
				return strings.ReplaceAll(clean, "\n", ", ")
			}
		}
	}

	data, err := os.ReadFile("/etc/resolv.conf")
	if err == nil {
		var servers []string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "nameserver") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					servers = append(servers, fields[1])
				}
			}
		}
		if len(servers) > 0 {
			return strings.Join(servers, ", ")
		}
	}

	return "192.168.0.1 (Router DHCP)"
}

func getNetworkInterfaces() []string {
	output, err := runCommand("ifconfig", "-l")
	if err != nil {
		return nil
	}

	interfaces := make([]string, 0)
	for _, iface := range strings.Fields(output) {
		if iface == "" || iface == "lo0" || strings.HasPrefix(iface, "bridge") || strings.HasPrefix(iface, "utun") || strings.HasPrefix(iface, "llw") || strings.HasPrefix(iface, "awdl") {
			continue
		}
		interfaces = append(interfaces, iface)
	}

	return interfaces
}

func getDarwinNetworkService(iface string) string {
	output, err := runCommand("networksetup", "-listallhardwareports")
	if err != nil {
		return "Wi-Fi"
	}

	lines := strings.Split(output, "\n")
	var currentService string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Hardware Port:") {
			currentService = strings.TrimSpace(strings.TrimPrefix(line, "Hardware Port:"))
		} else if strings.HasPrefix(line, "Device:") {
			device := strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
			if device == iface && currentService != "" {
				return currentService
			}
		}
	}
	return "Wi-Fi"
}

func getInterfaceIPAddresses(iface string) []string {
	output, err := runCommand("ifconfig", iface)
	if err != nil {
		return nil
	}

	var addrs []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				addrs = append(addrs, fields[1])
			}
		}
	}
	return addrs
}

func setPermanentIP(iface, cidr, gateway string) string {
	service := getDarwinNetworkService(iface)
	ip, prefix, err := splitCIDR(cidr)
	if err != nil {
		return commandError("Invalid CIDR format", "", err)
	}

	mask, err := prefixToMask(prefix)
	if err != nil {
		return commandError("Invalid Subnet Prefix", "", err)
	}

	if strings.TrimSpace(gateway) == "" {
		gateway = "192.168.0.1"
	}

	output, err := runCommand("networksetup", "-setmanual", service, ip, mask, gateway)
	if err != nil {
		return commandError("Failed to apply permanent static IP on macOS", output, err)
	}

	return outputOrSuccess(output, fmt.Sprintf("Static IP %s configured permanently on %s (%s).", ip, service, iface))
}

func setTemporaryIP(iface, cidr string) string {
	ip, prefix, err := splitCIDR(cidr)
	if err != nil {
		return commandError("Invalid CIDR", "", err)
	}

	mask, err := prefixToMask(prefix)
	if err != nil {
		return commandError("Invalid Subnet Prefix", "", err)
	}

	output, err := runCommand("ifconfig", iface, "inet", ip, "netmask", mask, "up")
	if err != nil {
		return commandError("Failed to apply temporary IP address", output, err)
	}

	return outputOrSuccess(output, "Temporary IPv4 address "+ip+" applied to "+iface+".")
}

func addIPAlias(iface, cidr string) string {
	ip, prefix, err := splitCIDR(cidr)
	if err != nil {
		return commandError("Invalid CIDR", "", err)
	}

	mask, err := prefixToMask(prefix)
	if err != nil {
		return commandError("Invalid Subnet Prefix", "", err)
	}

	output, err := runCommand("ifconfig", iface, "alias", ip, "netmask", mask)
	if err != nil {
		return commandError("Failed to bind secondary IP alias", output, err)
	}

	return outputOrSuccess(output, "Secondary IP alias "+ip+" added to "+iface+".")
}

func deleteIPAlias(iface, cidr string) string {
	ip, _, _ := splitCIDR(cidr)
	if ip == "" {
		ip = cidr
	}

	output, err := runCommand("ifconfig", iface, "-alias", ip)
	if err != nil {
		return commandError("Failed to remove IPv4 alias", output, err)
	}

	return outputOrSuccess(output, "IPv4 alias "+ip+" removed successfully from "+iface+".")
}

func setDNS(iface string, servers []string, automatic bool) string {
	service := getDarwinNetworkService(iface)

	if automatic {
		output, err := runCommand("networksetup", "-setdnsservers", service, "Empty")
		if err != nil {
			return commandError("Failed to restore automatic DHCP DNS", output, err)
		}
		return Green + "[✓] Automatic DHCP DNS restored on " + service + "." + NC
	}

	if len(servers) == 0 {
		return commandError("No DNS servers specified", "", nil)
	}

	args := append([]string{"-setdnsservers", service}, servers...)
	output, err := runCommand("networksetup", args...)
	if err != nil {
		return commandError("Failed to update DNS configuration", output, err)
	}

	return Green + "[✓] Custom DNS servers saved on " + service + " (" + strings.Join(servers, ", ") + ")." + NC
}

func getCurrentProxy() string {
	rec := getActiveProxyRecord()
	if rec != "" {
		return rec
	}

	service := "Wi-Fi"
	interfaces := getNetworkInterfaces()
	if len(interfaces) > 0 {
		service = getDarwinNetworkService(interfaces[0])
	}

	out, err := runCommand("networksetup", "-getwebproxy", service)
	if err == nil && strings.Contains(out, "Enabled: Yes") {
		var host, port string
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "Server:") {
				host = strings.TrimSpace(strings.TrimPrefix(line, "Server:"))
			}
			if strings.HasPrefix(line, "Port:") {
				port = strings.TrimSpace(strings.TrimPrefix(line, "Port:"))
			}
		}
		if host != "" && port != "" {
			return host + ":" + port
		}
	}

	return "DIRECT (Disabled)"
}

func setSystemProxy(host, port string) string {
	endpoint := host + ":" + port
	saveActiveProxyRecord(endpoint)

	service := "Wi-Fi"
	interfaces := getNetworkInterfaces()
	if len(interfaces) > 0 {
		service = getDarwinNetworkService(interfaces[0])
	}

	// 1. Configure networksetup for Web (HTTP) and Secure Web (HTTPS)
	_, _ = runCommand("networksetup", "-setwebproxy", service, host, port)
	output, err := runCommand("networksetup", "-setsecurewebproxy", service, host, port)
	if err != nil {
		return commandError("Failed to configure macOS system proxy", output, err)
	}

	// 2. Export environment variables for current process & CLI
	_ = os.Setenv("http_proxy", "http://"+endpoint)
	_ = os.Setenv("https_proxy", "http://"+endpoint)
	_ = os.Setenv("all_proxy", "socks5://"+endpoint)
	_ = os.Setenv("HTTP_PROXY", "http://"+endpoint)
	_ = os.Setenv("HTTPS_PROXY", "http://"+endpoint)
	_ = os.Setenv("ALL_PROXY", "socks5://"+endpoint)

	return Green + "[✓] macOS HTTP & HTTPS system proxy enabled.\n" +
		FgBright + "    => Active Endpoint: " + FgEmerald + Bold + endpoint + Reset + "\n" +
		FgMuted + "    => Applied to service: " + service + " and CLI session environment." + Reset
}

func disableSystemProxy() string {
	removeActiveProxyRecord()

	service := "Wi-Fi"
	interfaces := getNetworkInterfaces()
	if len(interfaces) > 0 {
		service = getDarwinNetworkService(interfaces[0])
	}

	_, _ = runCommand("networksetup", "-setwebproxystate", service, "off")
	output, err := runCommand("networksetup", "-setsecurewebproxystate", service, "off")
	if err != nil {
		return commandError("Failed to disable macOS system proxy", output, err)
	}

	_ = os.Unsetenv("http_proxy")
	_ = os.Unsetenv("https_proxy")
	_ = os.Unsetenv("all_proxy")
	_ = os.Unsetenv("HTTP_PROXY")
	_ = os.Unsetenv("HTTPS_PROXY")
	_ = os.Unsetenv("ALL_PROXY")

	return outputOrSuccess(output, "macOS system proxy disabled successfully. DIRECT routing restored.")
}

func restartNetworkManagerService() string {
	output, err := runCommand("killall", "configd")
	if err != nil {
		return commandError("Failed to refresh macOS network configuration daemon", output, err)
	}
	return outputOrSuccess(output, "macOS network configuration daemon (configd) refreshed successfully.")
}

func runNetworkTroubleshooter() []DiagnosticStep {
	var steps []DiagnosticStep

	// 1. Hostname & hosts check
	hostname, _ := os.Hostname()
	hostsData, _ := os.ReadFile("/etc/hosts")
	step1 := DiagnosticStep{Name: "macOS Hostname & /etc/hosts Integrity"}
	if !strings.Contains(string(hostsData), hostname) {
		step1.Passed = false
		step1.Details = fmt.Sprintf("Hostname '%s' missing from /etc/hosts.", hostname)
		syncDarwinHostsFile(hostname)
		step1.FixLog = "Injected hostname '" + hostname + "' into /etc/hosts."
	} else {
		step1.Passed = true
		step1.Details = "Hostname '" + hostname + "' correctly mapped in /etc/hosts."
	}
	steps = append(steps, step1)

	// 2. Hardware Interfaces
	interfaces := getNetworkInterfaces()
	step2 := DiagnosticStep{Name: "macOS Network Adapters"}
	if len(interfaces) == 0 {
		step2.Passed = false
		step2.Details = "No active network adapters found."
	} else {
		step2.Passed = true
		step2.Details = fmt.Sprintf("Detected %d active adapter(s). Primary: %s", len(interfaces), interfaces[0])
	}
	steps = append(steps, step2)

	// 3. Routing Table (Fixed: captured both return values)
	routeOut, _ := runCommand("netstat", "-rn")
	step3 := DiagnosticStep{Name: "Default Gateway Routing"}
	if !strings.Contains(routeOut, "default") {
		step3.Passed = false
		step3.Details = "Default route missing from macOS routing table."
		if len(interfaces) > 0 {
			service := getDarwinNetworkService(interfaces[0])
			_, _ = runCommand("networksetup", "-setdhcp", service)
			step3.FixLog = "Refreshed DHCP lease on service " + service + "."
		}
	} else {
		step3.Passed = true
		step3.Details = "Default gateway route active."
	}
	steps = append(steps, step3)

	// 4. DNS Query
	step4 := DiagnosticStep{Name: "DNS Query Resolution"}
	dnsRes := testDNSResolution("1.1.1.1", "google.com")
	if strings.Contains(dnsRes, "SUCCESS") {
		step4.Passed = true
		step4.Details = "Port 53 resolver operational."
	} else {
		step4.Passed = false
		step4.Details = "DNS lookup failed."
		if len(interfaces) > 0 {
			service := getDarwinNetworkService(interfaces[0])
			_, _ = runCommand("networksetup", "-setdnsservers", service, "1.1.1.1", "8.8.8.8")
			step4.FixLog = "Configured fallback DNS (1.1.1.1, 8.8.8.8) on " + service + "."
		}
	}
	steps = append(steps, step4)

	// 5. Internet Ping (Fixed: captured output properly)
	step5 := DiagnosticStep{Name: "Internet Reachability (ICMP)"}
	pingOut := pingHost("1.1.1.1")
	if strings.Contains(pingOut, "bytes from") || strings.Contains(pingOut, "ttl=") {
		step5.Passed = true
		step5.Details = "Internet connectivity nominal."
	} else {
		step5.Passed = false
		step5.Details = "ICMP ping failed."
		step5.FixLog = "Refreshed configd network daemon."
		_ = restartNetworkManagerService()
	}
	steps = append(steps, step5)

	return steps
}

func showBootModeMenu() {
	showHeader("SYSTEM MODE SWITCHER")
	printBoxRow(" "+FgAmber+"[!] Mode switching (TTY/GUI) is only available on Linux systemd systems."+Reset, "\n")
	pauseAction()
}

func enableGlobalCLI(cmdName string) (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not determine executable path: %w", err)
	}
	execPath, _ = filepath.EvalSymlinks(execPath)

	wrapperScript := fmt.Sprintf("#!/bin/bash\nexec sudo \"%s\" \"$@\"\n", execPath)

	targetBins := []string{
		filepath.Join("/usr/local/bin", cmdName),
		filepath.Join("/opt/homebrew/bin", cmdName),
	}

	wrapperCreated := false
	for _, p := range targetBins {
		dir := filepath.Dir(p)
		if _, err := os.Stat(dir); err == nil {
			_ = os.Remove(p)
			if err := os.WriteFile(p, []byte(wrapperScript), 0755); err == nil {
				wrapperCreated = true
				break
			}
		}
	}

	homeDir, _ := os.UserHomeDir()
	profiles := []string{
		filepath.Join(homeDir, ".zshrc"),
		filepath.Join(homeDir, ".bash_profile"),
		filepath.Join(homeDir, ".profile"),
	}

	for _, profile := range profiles {
		if _, err := os.Stat(profile); err == nil {
			appendShellProfileBlock(profile, cmdName, execPath)
		}
	}

	saveActiveCliCommand(cmdName)

	if wrapperCreated {
		return fmt.Sprintf("Global CLI enabled on macOS!\nTyping '%s' in Terminal will run with full ROOT/Admin access.", cmdName), nil
	}
	return fmt.Sprintf("Global CLI configured for command '%s'.", cmdName), nil
}

func disableGlobalCLI() (string, error) {
	cmdName := getActiveCliCommand()
	if cmdName == "" {
		cmdName = "switcher-wizard"
	}

	_ = os.Remove(filepath.Join("/usr/local/bin", cmdName))
	_ = os.Remove(filepath.Join("/opt/homebrew/bin", cmdName))

	homeDir, _ := os.UserHomeDir()
	profiles := []string{
		filepath.Join(homeDir, ".zshrc"),
		filepath.Join(homeDir, ".bash_profile"),
		filepath.Join(homeDir, ".profile"),
	}

	for _, profile := range profiles {
		if _, err := os.Stat(profile); err == nil {
			stripShellProfileBlock(profile)
		}
	}

	removeActiveCliRecord()
	return fmt.Sprintf("Global CLI '%s' disabled and removed from macOS.", cmdName), nil
}

func handleDirectBootCliShell() {
	// Not required on macOS
}

func isWindowsUTF8Ready() bool {
	return true
}
