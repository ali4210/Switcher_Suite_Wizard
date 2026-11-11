//go:build linux

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	if err := os.WriteFile("/etc/hostname", []byte(newName+"\n"), 0644); err != nil {
		return commandError("Failed to write /etc/hostname", "", err), err
	}
	if err := exec.Command("hostname", newName).Run(); err != nil {
		return commandError("Failed to apply hostname to the running kernel", "", err), err
	}

	// Merge the new hostname into /etc/hosts instead of overwriting it
	syncLinuxHostsFile(newName)

	return fmt.Sprintf("Hostname permanently set to '%s' (updated /etc/hostname, /etc/hosts, and live kernel).", newName), nil
}

// backupHostsFile preserves the pre-change contents of /etc/hosts so a bad
// merge can be undone manually.
func backupHostsFile() {
	data, err := os.ReadFile("/etc/hosts")
	if err != nil {
		return
	}
	_ = os.WriteFile("/etc/hosts.switcher.bak", data, 0644)
}

// syncLinuxHostsFile backs up /etc/hosts, then merges the given hostname
// into its Debian-style 127.0.1.1 line instead of truncating the whole file
// (which previously destroyed any custom entries such as Docker or local
// dev domains). The localhost/::1 lines are preserved as-is.
func syncLinuxHostsFile(hostname string) {
	backupHostsFile()

	data, err := os.ReadFile("/etc/hosts")
	if err != nil {
		hostsData := fmt.Sprintf("127.0.0.1\tlocalhost\n::1\t\tlocalhost ip6-localhost ip6-loopback\n127.0.1.1\t%s\n", hostname)
		_ = os.WriteFile("/etc/hosts", []byte(hostsData), 0644)
		return
	}

	lines := strings.Split(string(data), "\n")
	var newLines []string
	wroteHostnameLine := false
	hasLocalhostLine := false
	hasIPv6LocalhostLine := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "127.0.1.1"):
			newLines = append(newLines, "127.0.1.1\t"+hostname)
			wroteHostnameLine = true
		case strings.HasPrefix(trimmed, "127.0.0.1"):
			hasLocalhostLine = true
			newLines = append(newLines, line)
		case strings.HasPrefix(trimmed, "::1"):
			hasIPv6LocalhostLine = true
			newLines = append(newLines, line)
		default:
			newLines = append(newLines, line)
		}
	}

	if !hasLocalhostLine {
		newLines = append([]string{"127.0.0.1\tlocalhost"}, newLines...)
	}
	if !hasIPv6LocalhostLine {
		newLines = append(newLines, "::1\t\tlocalhost ip6-localhost ip6-loopback")
	}
	if !wroteHostnameLine {
		newLines = append(newLines, "127.0.1.1\t"+hostname)
	}

	_ = os.WriteFile("/etc/hosts", []byte(strings.Join(newLines, "\n")), 0644)
}

// isSystemdResolvedManaged reports whether /etc/resolv.conf is owned by
// systemd-resolved, in which case writing it directly is silently reverted
// and resolvectl must be used instead.
func isSystemdResolvedManaged() bool {
	if target, err := os.Readlink("/etc/resolv.conf"); err == nil && strings.Contains(target, "systemd") {
		return true
	}
	_, err := os.Stat("/run/systemd/resolve/resolv.conf")
	return err == nil
}

func applyResolvedDNS(iface string, servers []string) (string, error) {
	args := append([]string{"dns", iface}, servers...)
	return runCommand("resolvectl", args...)
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

	activationResult := reactivateNetworkManagerConnection(connection)

	var resolvedWarning string
	if !automatic && len(servers) > 0 {
		if isSystemdResolvedManaged() {
			if out, rerr := applyResolvedDNS(iface, servers); rerr != nil {
				resolvedWarning = "\n\n" + commandError(
					"DNS was saved in NetworkManager, but resolvectl failed to apply it to the live resolver",
					out,
					rerr,
				)
			}
		} else {
			var buf strings.Builder
			for _, s := range servers {
				buf.WriteString("nameserver " + s + "\n")
			}
			if werr := os.WriteFile("/etc/resolv.conf", []byte(buf.String()), 0644); werr != nil {
				resolvedWarning = "\n\n" + commandError(
					"DNS was saved in NetworkManager, but writing /etc/resolv.conf failed",
					"",
					werr,
				)
			}
		}
	}

	return Green + "[✓] " + successMessage + NC + "\n\n" + activationResult + resolvedWarning
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
				return net.JoinHostPort(host, port)
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

// buildLinuxProxyEnvContent renders the exact /etc/environment /
// /etc/environment.d / profile.d content for a given endpoint. Pure and
// side-effect-free so it can be unit tested directly: endpoint is expected
// to already be the net.JoinHostPort form (bracketed for IPv6), which is
// also exactly what a URL needs for its host:port component.
func buildLinuxProxyEnvContent(endpoint string) string {
	return fmt.Sprintf(
		"http_proxy=\"http://%s/\"\nhttps_proxy=\"http://%s/\"\nall_proxy=\"socks5://%s/\"\nHTTP_PROXY=\"http://%s/\"\nHTTPS_PROXY=\"http://%s/\"\nALL_PROXY=\"socks5://%s/\"\nno_proxy=\"localhost,127.0.0.1,localaddress,.localdomain.com\"\n",
		endpoint, endpoint, endpoint, endpoint, endpoint, endpoint,
	)
}

func setSystemProxy(host, port string) string {
	endpoint := net.JoinHostPort(host, port)
	saveActiveProxyRecord(endpoint)

	// 1. GNOME / XFCE Desktop Proxy Settings -- host and port stay separate
	// arguments here, matching the gsettings schema's own host/port keys.
	if commandExists("gsettings") {
		_, _ = runCommand("gsettings", "set", "org.gnome.system.proxy", "mode", "manual")
		_, _ = runCommand("gsettings", "set", "org.gnome.system.proxy.http", "host", host)
		_, _ = runCommand("gsettings", "set", "org.gnome.system.proxy.http", "port", port)
		_, _ = runCommand("gsettings", "set", "org.gnome.system.proxy.https", "host", host)
		_, _ = runCommand("gsettings", "set", "org.gnome.system.proxy.https", "port", port)
	}

	// 2. Global Linux Environment Configuration
	envContent := buildLinuxProxyEnvContent(endpoint)
	if err := os.WriteFile("/etc/environment", []byte(envContent), 0644); err != nil {
		return commandError("Failed to write /etc/environment", "", err)
	}

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

	if err := os.WriteFile("/etc/environment", []byte(""), 0644); err != nil {
		return commandError("Failed to clear /etc/environment", "", err)
	}
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

const switchLogFile = "/tmp/switcher-switch.log"

// detectDisplayManager identifies the display manager actually configured on
// this host instead of assuming lightdm/gdm3/sddm are all present. Returns ""
// if none can be identified, so callers can fall back to graphical.target.
func detectDisplayManager() string {
	if data, err := os.ReadFile("/etc/X11/default-display-manager"); err == nil {
		dm := strings.TrimSpace(string(data))
		dm = filepath.Base(dm)
		if dm != "" {
			if !strings.HasSuffix(dm, ".service") {
				dm += ".service"
			}
			return dm
		}
	}

	for _, svc := range []string{"lightdm.service", "gdm.service", "gdm3.service", "sddm.service"} {
		if out, err := runCommand("systemctl", "list-unit-files", svc); err == nil && strings.Contains(out, svc) {
			return svc
		}
	}

	return ""
}

// runDetachedModeSwitch launches script so it survives the current session
// being torn down by the switch itself (e.g. stopping the display manager
// that owns the invoking session's cgroup). It prefers a transient systemd
// scope, falling back to setsid, and finally to a plain background process.
// launchDetached starts name/args as its own session (SysProcAttr.Setsid),
// with stdio pointed at /dev/null so it has no controlling terminal to lose,
// and releases Go's handle on the process so it survives independently of
// this process -- including a SIGHUP when the invoking terminal closes.
func launchDetached(devNull *os.File, name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull

	if err := cmd.Start(); err != nil {
		return false
	}
	_ = cmd.Process.Release()
	return true
}

func runDetachedModeSwitch(script string) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		devNull = nil
	} else {
		defer devNull.Close()
	}

	if commandExists("systemd-run") {
		if launchDetached(devNull, "systemd-run", "--scope", "setsid", "bash", "-c", script) {
			return
		}
	}

	if commandExists("setsid") {
		if launchDetached(devNull, "setsid", "bash", "-c", script) {
			return
		}
	}

	launchDetached(devNull, "bash", "-c", script)
}

func buildGuiSwitchScript(dm string) string {
	startCmd := "systemctl isolate graphical.target"
	if dm != "" {
		startCmd = "systemctl start " + dm
	}

	return fmt.Sprintf(`{
echo "=== $(date '+%%Y-%%m-%%d %%H:%%M:%%S') switch-to-gui ==="
timeout 10 %s || echo "[switcher] display manager start failed or timed out"
sleep 1
chvt 7 || chvt 2 || echo "[switcher] chvt failed"
echo "[switcher] switch-to-gui complete"
} >>%s 2>&1
`, startCmd, switchLogFile)
}

func buildTtySwitchScript(dm string) string {
	stopCmd := "systemctl isolate multi-user.target"
	if dm != "" {
		stopCmd = "systemctl stop " + dm
	}

	return fmt.Sprintf(`{
echo "=== $(date '+%%Y-%%m-%%d %%H:%%M:%%S') switch-to-tty ==="
systemctl enable --now getty@tty1.service || echo "[switcher] failed to enable getty@tty1"
systemctl restart getty@tty1.service || echo "[switcher] failed to restart getty@tty1"
systemctl start getty@tty2.service || true
timeout 10 %s || echo "[switcher] display manager stop failed or timed out"
chvt 1 || openvt -c 1 -- /bin/login || echo "[switcher] chvt failed"
echo "[switcher] switch-to-tty complete"
} >>%s 2>&1
`, stopCmd, switchLogFile)
}

func printSwitchRecoveryHint(target string) {
	printBoxBlankRow("\n")
	printBoxRow(" "+FgCyan+"[i] Switch to "+target+" dispatched in the background."+Reset, "\n")
	printBoxRow("     If the screen does not change within ~10 seconds:", "\n")
	printBoxRow("     - Try Ctrl+Alt+F1 through F7 to find the active VT manually.", "\n")
	printBoxRow("     - Check "+FgBright+switchLogFile+Reset+" for what the switch actually did.", "\n")
	printBoxRow("     - Re-run this wizard and choose the mode again if needed.", "\n")
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
			dm := detectDisplayManager()
			if dm == "" {
				printBoxRow(" "+FgAmber+"[!] No known display manager (lightdm/gdm/gdm3/sddm) was detected."+Reset, "\n")
				printBoxRow("     Falling back to 'systemctl isolate graphical.target'.", "\n")
			} else {
				printBoxRow(" "+FgCyan+"[i] Activating graphical desktop ("+dm+")..."+Reset, "\n")
			}
			printBoxBlankRow("\n")

			if !confirmAction("Start graphical desktop environment now?") {
				showHeader("ACTION CANCELLED")
				printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
				pauseAction()
				continue
			}

			runDetachedModeSwitch(buildGuiSwitchScript(dm))
			showHeader("GUI SWITCH DISPATCHED")
			printSwitchRecoveryHint("GUI desktop")
			pauseAction()
			return

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

			dm := detectDisplayManager()
			runDetachedModeSwitch(buildTtySwitchScript(dm))
			showHeader("TTY SWITCH DISPATCHED")
			printSwitchRecoveryHint("TTY / CLI console")
			pauseAction()
			return

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
			restoreNormalScreen()
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

	symlinkPaths := []string{
		filepath.Join("/usr/local/bin", cmdName),
		filepath.Join("/usr/bin", cmdName),
	}

	if err := checkCliAliasSafeToClaim(cmdName, symlinkPaths); err != nil {
		return "", err
	}

	wrapperScript := fmt.Sprintf("#!/bin/bash\n# %s\nexec sudo \"%s\" \"$@\"\n", cliShimMarker, execPath)

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

	for _, p := range []string{
		filepath.Join("/usr/local/bin", cmdName),
		filepath.Join("/usr/bin", cmdName),
	} {
		if isOwnedCliShimFile(p) {
			_ = os.Remove(p)
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

		syncLinuxHostsFile(hostname)
		step1.FixLog = "Merged active hostname '" + hostname + "' into /etc/hosts (backup at /etc/hosts.switcher.bak)."
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

		if isSystemdResolvedManaged() {
			_, _ = applyResolvedDNS(primaryIface, []string{"1.1.1.1", "8.8.8.8", "192.168.0.1"})
			step4.FixLog = "Applied fallback DNS via resolvectl (systemd-resolved detected)."
		} else {
			fallbackResolv := "nameserver 1.1.1.1\nnameserver 8.8.8.8\nnameserver 192.168.0.1\n"
			_ = os.WriteFile("/etc/resolv.conf", []byte(fallbackResolv), 0644)
			step4.FixLog = "Wrote fallback public & router DNS servers to /etc/resolv.conf."
		}
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
