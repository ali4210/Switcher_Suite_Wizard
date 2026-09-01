//go:build linux

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

	// Update hostname
	if commandExists("hostnamectl") {
		_, _ = runCommand("hostnamectl", "set-hostname", newName)
	}
	_ = os.WriteFile("/etc/hostname", []byte(newName+"\n"), 0644)
	_ = exec.Command("hostname", newName).Run()

	// Automatically patch /etc/hosts with new hostname
	hostsData := fmt.Sprintf("127.0.0.1\tlocalhost %s\n::1\t\tlocalhost ip6-localhost ip6-loopback\n127.0.1.1\t%s\n", newName, newName)
	_ = os.WriteFile("/etc/hosts", []byte(hostsData), 0644)

	return fmt.Sprintf("Hostname permanently set to '%s' (updated /etc/hostname, /etc/hosts, and live kernel).", newName), nil
}

func getInterfaceIPAddresses(iface string) []string {
	output, err := runCommand("ip", "-4", "-o", "addr", "show", "dev", iface)
	if err != nil {
		return nil
	}

	var addrs []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		for i, field := range fields {
			if field == "inet" && i+1 < len(fields) {
				addrs = append(addrs, fields[i+1])
			}
		}
	}
	return addrs
}

func deleteIPAlias(iface, cidr string) string {
	output, err := runCommand("ip", "addr", "del", cidr, "dev", iface)
	if err != nil {
		return commandError("Failed to remove IPv4 alias", output, err)
	}
	return outputOrSuccess(output, "IPv4 alias "+cidr+" removed successfully from "+iface+".")
}

func getCurrentDNS(iface string) string {
	if commandExists("nmcli") {
		output, err := runCommand("nmcli", "-g", "IP4.DNS", "device", "show", iface)
		if err == nil {
			clean := strings.TrimSpace(output)
			clean = strings.ReplaceAll(clean, "\n", ", ")
			clean = strings.ReplaceAll(clean, " | ", ", ")
			if clean != "" {
				return clean
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
	output, err := runCommand("ip", "-o", "link", "show")
	if err != nil {
		return nil
	}

	interfaces := make([]string, 0)

	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 2 {
			continue
		}

		iface := strings.TrimSpace(parts[1])
		iface = strings.Split(iface, "@")[0]

		if iface == "" || iface == "lo" {
			continue
		}

		interfaces = append(interfaces, iface)
	}

	return interfaces
}

func getActiveNetworkManagerConnection(iface string) (string, string) {
	if !commandExists("nmcli") {
		return "", Yellow +
			"[!] NetworkManager (nmcli) was not found.\n" +
			"[i] Permanent IP and DNS profile changes require NetworkManager." +
			NC
	}

	output, err := runCommand(
		"nmcli",
		"-g",
		"GENERAL.CONNECTION",
		"device",
		"show",
		iface,
	)
	if err != nil {
		return "", commandError(
			"Could not identify the active NetworkManager connection",
			output,
			err,
		)
	}

	connection := strings.TrimSpace(output)
	if connection == "" || connection == "--" {
		return "", Red +
			"[!] No active NetworkManager connection profile was found for " +
			iface +
			"." +
			NC
	}

	return connection, ""
}

func reactivateNetworkManagerConnection(connection string) string {
	output, err := runCommand(
		"nmcli",
		"connection",
		"up",
		"id",
		connection,
	)
	if err != nil {
		return commandError(
			"Settings were saved but the connection could not be reactivated",
			output,
			err,
		)
	}

	return outputOrSuccess(
		output,
		"NetworkManager connection reactivated.",
	)
}

func setPermanentIP(iface, cidr, gateway string) string {
	connection, errorMessage := getActiveNetworkManagerConnection(iface)
	if errorMessage != "" {
		return errorMessage
	}

	args := []string{
		"connection",
		"modify",
		connection,
		"ipv4.method",
		"manual",
		"ipv4.addresses",
		cidr,
	}

	if strings.TrimSpace(gateway) == "" {
		args = append(args, "ipv4.gateway", "")
	} else {
		args = append(args, "ipv4.gateway", gateway)
	}

	output, err := runCommand("nmcli", args...)
	if err != nil {
		return commandError(
			"Failed to update permanent IPv4 configuration",
			output,
			err,
		)
	}

	activationResult := reactivateNetworkManagerConnection(connection)
	return Green +
		"[✓] Permanent static IPv4 configuration saved." +
		NC +
		"\n\n" +
		activationResult
}

func setTemporaryIP(iface, cidr string) string {
	output, err := runCommand(
		"ip",
		"address",
		"replace",
		cidr,
		"dev",
		iface,
	)
	if err != nil {
		return commandError("Failed to apply temporary IP address", output, err)
	}

	return outputOrSuccess(
		output,
		"Temporary IPv4 address applied to "+iface+".",
	)
}

func addIPAlias(iface, cidr string) string {
	output, err := runCommand(
		"ip",
		"address",
		"add",
		cidr,
		"dev",
		iface,
	)
	if err != nil {
		return commandError("Failed to add secondary IPv4 address", output, err)
	}

	return outputOrSuccess(
		output,
		"Secondary IPv4 address added to "+iface+".",
	)
}

func setDNS(iface string, servers []string, automatic bool) string {
	connection, errorMessage := getActiveNetworkManagerConnection(iface)
	if errorMessage != "" {
		return errorMessage
	}

	var args []string
	var successMessage string

	if automatic {
		args = []string{
			"connection",
			"modify",
			connection,
			"ipv4.ignore-auto-dns",
			"no",
			"ipv4.dns",
			"",
		}
		successMessage = "Automatic DHCP DNS restored (Default router gateway active)."
	} else {
		args = []string{
			"connection",
			"modify",
			connection,
			"ipv4.ignore-auto-dns",
			"yes",
			"ipv4.dns",
			strings.Join(servers, " "),
		}
		successMessage = "Custom DNS servers saved through NetworkManager."
	}

	output, err := runCommand("nmcli", args...)
	if err != nil {
		return commandError("Failed to update DNS configuration", output, err)
	}

	_ = reactivateNetworkManagerConnection(connection)

	if !automatic && len(servers) > 0 {
		var buf strings.Builder
		for _, s := range servers {
			buf.WriteString("nameserver " + s + "\n")
		}
		_ = os.WriteFile("/etc/resolv.conf", []byte(buf.String()), 0644)
	}

	return Green + "[✓] " + successMessage + NC
}

func getCurrentProxy() string {
	rec := getActiveProxyRecord()
	if rec != "" {
		return rec
	}

	if commandExists("gsettings") {
		modeOut, err := runCommand("gsettings", "get", "org.gnome.system.proxy", "mode")
		if err == nil && strings.Contains(modeOut, "manual") {
			hostOut, _ := runCommand("gsettings", "get", "org.gnome.system.proxy.http", "host")
			portOut, _ := runCommand("gsettings", "get", "org.gnome.system.proxy.http", "port")
			host := strings.Trim(strings.TrimSpace(hostOut), "'\"")
			port := strings.TrimSpace(portOut)
			if host != "" && port != "0" {
				return host + ":" + port
			}
		}
	}

	envData, err := os.ReadFile("/etc/environment")
	if err == nil {
		for _, line := range strings.Split(string(envData), "\n") {
			if strings.HasPrefix(line, "http_proxy=") || strings.HasPrefix(line, "HTTP_PROXY=") {
				parts := strings.Split(line, "=")
				if len(parts) >= 2 {
					val := strings.Trim(strings.TrimSpace(parts[1]), "'\"")
					val = strings.TrimPrefix(val, "http://")
					val = strings.TrimSuffix(val, "/")
					if val != "" {
						return val
					}
				}
			}
		}
	}

	if httpProxy := os.Getenv("http_proxy"); httpProxy != "" {
		return httpProxy
	}

	return "DIRECT (Disabled)"
}

func setSystemProxy(host, port string) string {
	endpoint := host + ":" + port
	saveActiveProxyRecord(endpoint)

	// 1. GNOME / XFCE Desktop Proxy Settings
	if commandExists("gsettings") {
		_, _ = runCommand("gsettings", "set", "org.gnome.system.proxy", "mode", "manual")
		_, _ = runCommand("gsettings", "set", "org.gnome.system.proxy.http", "host", host)
		_, _ = runCommand("gsettings", "set", "org.gnome.system.proxy.http", "port", port)
		_, _ = runCommand("gsettings", "set", "org.gnome.system.proxy.https", "host", host)
		_, _ = runCommand("gsettings", "set", "org.gnome.system.proxy.https", "port", port)
	}

	// 2. Global Linux Environment Configuration
	envContent := fmt.Sprintf("http_proxy=\"http://%s:%s/\"\nhttps_proxy=\"http://%s:%s/\"\nall_proxy=\"socks5://%s:%s/\"\nHTTP_PROXY=\"http://%s:%s/\"\nHTTPS_PROXY=\"http://%s:%s/\"\nALL_PROXY=\"socks5://%s:%s/\"\nno_proxy=\"localhost,127.0.0.1,localaddress,.localdomain.com\"\n", host, port, host, port, host, port, host, port, host, port, host, port)
	_ = os.WriteFile("/etc/environment", []byte(envContent), 0644)

	// 3. Persistent Profile Script for Shell Sessions
	_ = os.MkdirAll("/etc/environment.d", 0755)
	_ = os.WriteFile("/etc/environment.d/99-switcher-proxy.conf", []byte(envContent), 0644)
	_ = os.WriteFile("/etc/profile.d/switcher_proxy.sh", []byte("export "+strings.ReplaceAll(envContent, "\n", " ")+"\n"), 0755)

	// 4. Runtime process exports
	_ = os.Setenv("http_proxy", "http://"+endpoint+"/")
	_ = os.Setenv("https_proxy", "http://"+endpoint+"/")
	_ = os.Setenv("all_proxy", "socks5://"+endpoint+"/")
	_ = os.Setenv("HTTP_PROXY", "http://"+endpoint+"/")
	_ = os.Setenv("HTTPS_PROXY", "http://"+endpoint+"/")
	_ = os.Setenv("ALL_PROXY", "socks5://"+endpoint+"/")

	return Green + "[✓] System-wide HTTP, HTTPS & SOCKS Proxy activated.\n" +
		FgBright + "    => Active Endpoint: " + FgEmerald + Bold + endpoint + Reset + "\n" +
		FgMuted + "    => Applied to: GNOME/XFCE desktop, /etc/environment, /etc/profile.d/, & live sessions." + Reset
}

func disableSystemProxy() string {
	removeActiveProxyRecord()

	if commandExists("gsettings") {
		_, _ = runCommand("gsettings", "set", "org.gnome.system.proxy", "mode", "none")
	}

	_ = os.WriteFile("/etc/environment", []byte(""), 0644)
	_ = os.Remove("/etc/environment.d/99-switcher-proxy.conf")
	_ = os.Remove("/etc/profile.d/switcher_proxy.sh")

	_ = os.Unsetenv("http_proxy")
	_ = os.Unsetenv("https_proxy")
	_ = os.Unsetenv("all_proxy")
	_ = os.Unsetenv("HTTP_PROXY")
	_ = os.Unsetenv("HTTPS_PROXY")
	_ = os.Unsetenv("ALL_PROXY")

	return Green + "[✓] System-wide Proxy completely disabled.\n" +
		FgBright + "    => Traffic mode set to DIRECT connection." + Reset
}

func showBootModeMenu() {
	for {
		choice := SelectFromOptions(
			"SYSTEM MODE SWITCHER",
			"Use UP/DOWN to choose an action, then press ENTER:",
			[]string{
				"Switch GUI / TTY mode immediately (Live Session)",
				"Set Startup Target (GUI Desktop / CLI TTY) for next boot",
				"Return to the main menu",
			},
		)

		switch choice {
		case 0:
			handleImmediateSwitch()
		case 1:
			handleNextBootSwitch()
		default:
			return
		}
	}
}

func handleImmediateSwitch() {
	for {
		choice := SelectFromOptions(
			"SWITCH MODE IMMEDIATELY",
			"Choose the mode to activate now:",
			[]string{
				"Switch to GUI Desktop immediately (Start Display Manager)",
				"Switch to TTY / CLI Mode immediately (Stop Graphical Desktop)",
				"Return to Previous Menu",
			},
		)

		switch choice {
		case 0:
			showHeader("CONFIRM SWITCH TO GUI")
			printBoxRow(" "+FgCyan+"[i] Activating graphical desktop (LightDM/GDM/SDDM)..."+Reset, "\n")
			printBoxBlankRow("\n")

			if !confirmAction("Start graphical desktop environment now?") {
				showHeader("ACTION CANCELLED")
				printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
				pauseAction()
				continue
			}

			script := `
				(
					systemctl start lightdm.service >/dev/null 2>&1 || systemctl start gdm3.service >/dev/null 2>&1 || systemctl start sddm.service >/dev/null 2>&1 || systemctl isolate graphical.target >/dev/null 2>&1
					sleep 1
					chvt 7 || chvt 2
				) >/dev/null 2>&1 &
			`

			cmd := exec.Command("bash", "-c", script)
			_ = cmd.Start()
			os.Exit(0)

		case 1:
			showHeader("CONFIRM SWITCH TO TTY / CLI")
			printBoxRow(" "+FgRose+Bold+"[!] NOTICE:"+Reset+" Graphical desktop will stop immediately.", "\n")
			printBoxRow("     Your display will switch directly to the TTY1 login console.", "\n")
			printBoxBlankRow("\n")

			if !confirmAction("Switch immediately to TTY / CLI mode?") {
				showHeader("ACTION CANCELLED")
				printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
				pauseAction()
				continue
			}

			script := `
				(
					systemctl enable --now getty@tty1.service >/dev/null 2>&1
					systemctl restart getty@tty1.service >/dev/null 2>&1
					systemctl start getty@tty2.service >/dev/null 2>&1
					systemctl stop lightdm.service >/dev/null 2>&1 || systemctl stop gdm3.service >/dev/null 2>&1 || systemctl stop sddm.service >/dev/null 2>&1
					sleep 0.5
					chvt 1 || openvt -c 1 -- /bin/login
				) >/dev/null 2>&1 &
			`

			cmd := exec.Command("bash", "-c", script)
			_ = cmd.Start()
			os.Exit(0)

		default:
			return
		}
	}
}

func handleNextBootSwitch() {
	for {
		choice := SelectFromOptions(
			"SET STARTUP TARGET FOR NEXT BOOT",
			"Choose how your machine should start after its next restart:",
			[]string{
				"Switch to GUI Desktop after next reboot (graphical.target)",
				"Switch to CLI / TTY Terminal Mode after next reboot (multi-user.target)",
				"Return to Previous Menu",
			},
		)

		var target string
		var modeName string

		switch choice {
		case 0:
			target = "graphical.target"
			modeName = "Graphical Desktop GUI (graphical.target)"
		case 1:
			target = "multi-user.target"
			modeName = "Headless CLI / TTY Terminal Mode (multi-user.target)"
		default:
			return
		}

		showHeader("CONFIRM STARTUP TARGET")
		printBoxRow(" "+FgMuted+"Target Target: "+FgEmerald+Bold+modeName+Reset, "\n")
		printBoxRow(" "+FgAmber+"[i] Updates systemd default target for subsequent boot cycles."+Reset, "\n")
		printBoxBlankRow("\n")

		if !confirmAction("Set this as default startup target for next boot?") {
			showHeader("ACTION CANCELLED")
			printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
			pauseAction()
			continue
		}

		output, err := runCommand("systemctl", "set-default", target)
		if err != nil {
			showHeader("SYSTEM ERROR")
			printBoxRow(" "+FgRose+"[!] Failed to set default target: "+err.Error()+Reset, "\n")
			if output != "" {
				printBoxRow("     "+output, "\n")
			}
			pauseAction()
			continue
		}

		showHeader("STARTUP TARGET CONFIGURED")
		printBoxRow(" "+FgEmerald+"[✓] Default startup target successfully set to:"+Reset, "\n")
		printBoxRow("     "+FgBright+Bold+modeName+Reset, "\n")
		printBoxBlankRow("\n")
		printBoxRow(" "+FgCyan+"[i] Target unit: "+target+Reset, "\n")
		printBoxBlankRow("\n")

		// Instant Reboot Option for Linux
		if confirmAction("Reboot Linux system immediately to enter this mode now?") {
			showHeader("SYSTEM REBOOT INITIATED")
			printBoxRow(" "+FgAmber+Bold+"[!] Restarting Linux operating system now..."+Reset, "\n")
			_, _ = runCommand("systemctl", "reboot")
			os.Exit(0)
		} else {
			showHeader("CONFIGURATION SAVED")
			printBoxRow(" "+FgEmerald+"[✓] Configuration active. Mode will apply upon your next restart."+Reset, "\n")
			pauseAction()
			return
		}
	}
}

func enableGlobalCLI(cmdName string) (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not determine executable path: %w", err)
	}
	execPath, _ = filepath.EvalSymlinks(execPath)

	wrapperScript := fmt.Sprintf("#!/bin/bash\nexec sudo \"%s\" \"$@\"\n", execPath)

	symlinkPaths := []string{
		filepath.Join("/usr/local/bin", cmdName),
		filepath.Join("/usr/bin", cmdName),
	}

	wrapperCreated := false
	for _, p := range symlinkPaths {
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
		filepath.Join(homeDir, ".bashrc"),
		filepath.Join(homeDir, ".zshrc"),
		filepath.Join(homeDir, ".profile"),
		"/etc/bash.bashrc",
		"/etc/zsh/zshrc",
	}

	for _, profile := range profiles {
		if _, err := os.Stat(profile); err == nil {
			appendShellProfileBlock(profile, cmdName, execPath)
		}
	}

	saveActiveCliCommand(cmdName)

	if wrapperCreated {
		return fmt.Sprintf("Global CLI enabled with AUTO-ROOT privileges!\nTyping '%s' anywhere in terminal will run with full ROOT access.", cmdName), nil
	}
	return fmt.Sprintf("Global CLI configured for command '%s'.", cmdName), nil
}

func disableGlobalCLI() (string, error) {
	cmdName := getActiveCliCommand()
	if cmdName == "" {
		cmdName = "switcher-wizard"
	}

	_ = os.Remove(filepath.Join("/usr/local/bin", cmdName))
	_ = os.Remove(filepath.Join("/usr/bin", cmdName))

	homeDir, _ := os.UserHomeDir()
	profiles := []string{
		filepath.Join(homeDir, ".bashrc"),
		filepath.Join(homeDir, ".zshrc"),
		filepath.Join(homeDir, ".profile"),
		"/etc/bash.bashrc",
		"/etc/zsh/zshrc",
	}

	for _, profile := range profiles {
		if _, err := os.Stat(profile); err == nil {
			stripShellProfileBlock(profile)
		}
	}

	removeActiveCliRecord()
	return fmt.Sprintf("Global CLI '%s' disabled and removed from system paths and shell configs.", cmdName), nil
}

func restartNetworkManagerService() string {
	output, err := runCommand("systemctl", "restart", "NetworkManager")
	if err != nil {
		return commandError("Failed to restart NetworkManager service", output, err)
	}
	return outputOrSuccess(output, "NetworkManager daemon service restarted successfully.")
}

func runNetworkTroubleshooter() []DiagnosticStep {
	var steps []DiagnosticStep

	// ── Phase 1: Local /etc/hosts & Hostname Consistency ───────────
	hostname, _ := os.Hostname()
	hostsData, err := os.ReadFile("/etc/hosts")
	hostsContent := string(hostsData)

	step1 := DiagnosticStep{Name: "Hostname & Local Host Mapping (/etc/hosts)"}
	if err != nil || !strings.Contains(hostsContent, hostname) {
		step1.Passed = false
		step1.Details = fmt.Sprintf("Hostname '%s' missing from /etc/hosts (causes sudo lag and lookup errors).", hostname)

		fixedHosts := fmt.Sprintf("127.0.0.1\tlocalhost %s\n::1\t\tlocalhost ip6-localhost ip6-loopback\n127.0.1.1\t%s\n", hostname, hostname)
		_ = os.WriteFile("/etc/hosts", []byte(fixedHosts), 0644)
		step1.FixLog = "Injected active hostname '" + hostname + "' into /etc/hosts."
	} else {
		step1.Passed = true
		step1.Details = "Hostname '" + hostname + "' correctly mapped to 127.0.0.1."
	}
	steps = append(steps, step1)

	// ── Phase 2: Physical Network Interface State ──────────────────
	interfaces := getNetworkInterfaces()
	step2 := DiagnosticStep{Name: "Hardware Network Interfaces"}
	if len(interfaces) == 0 {
		step2.Passed = false
		step2.Details = "No active physical network interfaces detected."
		step2.FixLog = "Ensure virtual machine network adapter is enabled in VM settings."
		steps = append(steps, step2)
		return steps
	}

	primaryIface := interfaces[0]
	step2.Passed = true
	step2.Details = fmt.Sprintf("Detected %d active interface(s). Primary: %s", len(interfaces), primaryIface)
	steps = append(steps, step2)

	// ── Phase 3: Default Gateway & Kernel Routing Table ────────────
	routeOutput, _ := runCommand("ip", "route", "show")
	hasDefaultRoute := strings.Contains(routeOutput, "default via")

	step3 := DiagnosticStep{Name: "Default Gateway Routing (0.0.0.0/0)"}
	if !hasDefaultRoute {
		step3.Passed = false
		step3.Details = "Kernel routing table has NO default gateway route. External internet unreachable."

		_, _ = runCommand("nmcli", "device", "reapply", primaryIface)
		_, _ = runCommand("nmcli", "connection", "up", "id", primaryIface)

		newRoute, _ := runCommand("ip", "route", "show")
		if !strings.Contains(newRoute, "default via") {
			_, _ = runCommand("ip", "route", "add", "default", "via", "192.168.0.1", "dev", primaryIface)
			step3.FixLog = "Injected default gateway route (via 192.168.0.1 dev " + primaryIface + ")."
		} else {
			step3.FixLog = "Re-initialized NetworkManager route tables."
		}
	} else {
		step3.Passed = true
		lines := strings.Split(routeOutput, "\n")
		for _, l := range lines {
			if strings.HasPrefix(l, "default") {
				step3.Details = strings.TrimSpace(l)
				break
			}
		}
	}
	steps = append(steps, step3)

	// ── Phase 4: DNS Resolvers & Configuration ─────────────────────
	resolvData, _ := os.ReadFile("/etc/resolv.conf")
	resolvContent := string(resolvData)

	hasNameserver := strings.Contains(resolvContent, "nameserver")
	dnsResolutionTest := testDNSResolution("1.1.1.1", "google.com")
	canResolve := strings.Contains(dnsResolutionTest, "SUCCESS")

	if !hasNameserver || !canResolve {
		step4 := DiagnosticStep{Name: "DNS Configuration (/etc/resolv.conf)"}
		step4.Passed = false
		step4.Details = "Resolvers missing or port 53 queries failing."

		fallbackResolv := "nameserver 1.1.1.1\nnameserver 8.8.8.8\nnameserver 192.168.0.1\n"
		_ = os.WriteFile("/etc/resolv.conf", []byte(fallbackResolv), 0644)
		step4.FixLog = "Wrote fallback public & router DNS servers to /etc/resolv.conf."
		steps = append(steps, step4)
	} else {
		step4 := DiagnosticStep{Name: "DNS Configuration (/etc/resolv.conf)"}
		step4.Passed = true
		step4.Details = "Valid DNS servers active and resolving queries."
		steps = append(steps, step4)
	}

	// ── Phase 5: End-to-End Internet Connectivity Audit ────────────
	step5 := DiagnosticStep{Name: "End-to-End Internet ICMP & Socket Ping"}
	pingOut := pingHost("1.1.1.1")
	if strings.Contains(pingOut, "bytes from") {
		step5.Passed = true
		step5.Details = "Internet connection fully active. Latency nominal."
	} else {
		step5.Passed = false
		step5.Details = "ICMP ping to 1.1.1.1 failed (network unreachable or firewall drop)."

		conn, _ := getActiveNetworkManagerConnection(primaryIface)
		if conn != "" {
			_, _ = runCommand("nmcli", "connection", "modify", conn, "ipv4.method", "auto", "ipv4.gateway", "", "ipv4.dns", "", "ipv4.ignore-auto-dns", "no")
			_, _ = runCommand("nmcli", "connection", "down", conn)
			_, _ = runCommand("nmcli", "connection", "up", conn)
			step5.FixLog = "Refreshed full DHCP lease and reset connection profile '" + conn + "'."
		}
	}
	steps = append(steps, step5)

	return steps
}

func handleDirectBootCliShell() {
	// Not required on Linux (Linux uses systemd multi-user.target / TTY framebuffers)
}
func isWindowsUTF8Ready() bool {
	return true // Always true on Linux and macOS POSIX shells
}
