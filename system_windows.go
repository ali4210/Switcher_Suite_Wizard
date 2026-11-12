//go:build windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// Win32 Console API handles for ANSI Virtual Terminal Processing, CodePage & Viewport Control
var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetStdHandle               = kernel32.NewProc("GetStdHandle")
	procGetConsoleMode             = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procSetConsoleScreenBufferSize = kernel32.NewProc("SetConsoleScreenBufferSize")
	procSetConsoleWindowInfo       = kernel32.NewProc("SetConsoleWindowInfo")
	procSetConsoleOutputCP         = kernel32.NewProc("SetConsoleOutputCP")
	procSetConsoleCP               = kernel32.NewProc("SetConsoleCP")
)

const (
	stdOutputHandle                 = uint32(0xFFFFFFF5) // STD_OUTPUT_HANDLE (-11)
	enableVirtualTerminalProcessing = uint32(0x0004)
)

type coord struct {
	X int16
	Y int16
}

type smallRect struct {
	Left   int16
	Top    int16
	Right  int16
	Bottom int16
}

func init() {
	enableWindowsANSI()
}
func enableWindowsANSI() {
	// 1. Force UTF-8 (CodePage 65001) for crisp Unicode & ASCII rendering
	_, _, _ = procSetConsoleOutputCP.Call(uintptr(65001))
	_, _, _ = procSetConsoleCP.Call(uintptr(65001))

	handle, _, _ := procGetStdHandle.Call(uintptr(stdOutputHandle))
	if handle == uintptr(syscall.InvalidHandle) || handle == 0 {
		return
	}

	var mode uint32
	ret, _, _ := procGetConsoleMode.Call(handle, uintptr(unsafe.Pointer(&mode)))
	if ret != 0 {
		mode |= enableVirtualTerminalProcessing
		_, _, _ = procSetConsoleMode.Call(handle, uintptr(mode))
	}

	// 2. Lock buffer size and window size to prevent scrolling jumping
	// Buffer and Window match exactly (92 cols x 46 rows)
	windowRect := smallRect{Left: 0, Top: 0, Right: 91, Bottom: 45}
	bufferSize := coord{X: 92, Y: 46}

	_, _, _ = procSetConsoleScreenBufferSize.Call(handle, uintptr(*(*int32)(unsafe.Pointer(&bufferSize))))
	_, _, _ = procSetConsoleWindowInfo.Call(handle, 1, uintptr(unsafe.Pointer(&windowRect)))
}
func execCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

func isElevated() bool {
	output, err := runCommand("net", "session")
	if err == nil {
		return true
	}

	lowerOutput := strings.ToLower(output)
	if strings.Contains(lowerOutput, "access is denied") {
		return false
	}

	return false
}

func setSystemHostname(newName string) (string, error) {
	curr, _ := os.Hostname()
	savePreviousHostname(curr)

	newName = strings.TrimSpace(newName)

	// 1. Update Win32 Registry ComputerName entries directly
	output, err := runCommand("reg", "add", "HKLM\\SYSTEM\\CurrentControlSet\\Control\\ComputerName\\ComputerName", "/v", "ComputerName", "/t", "REG_SZ", "/d", newName, "/f")
	if err != nil {
		return commandError("Failed to update ComputerName registry key", output, err), err
	}
	_, _ = runCommand("reg", "add", "HKLM\\SYSTEM\\CurrentControlSet\\Control\\ComputerName\\ActiveComputerName", "/v", "ComputerName", "/t", "REG_SZ", "/d", newName, "/f")
	_, _ = runCommand("reg", "add", "HKLM\\SYSTEM\\CurrentControlSet\\Services\\Tcpip\\Parameters", "/v", "NV Hostname", "/t", "REG_SZ", "/d", newName, "/f")
	_, _ = runCommand("reg", "add", "HKLM\\SYSTEM\\CurrentControlSet\\Services\\Tcpip\\Parameters", "/v", "Hostname", "/t", "REG_SZ", "/d", newName, "/f")

	// 2. Execute WMI / PowerShell rename as supplementary sync
	psScript := fmt.Sprintf(`
		try {
			Rename-Computer -NewName '%s' -Force -ErrorAction SilentlyContinue
		} catch {}
	`, newName)
	_, _ = runCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)

	return fmt.Sprintf("Windows Computer Name successfully set to '%s'.\n     (Registry updated permanently; will take full effect across all active network sockets on next reboot)", newName), nil
}

func getCurrentDNS(iface string) string {
	psCmd := fmt.Sprintf(`(Get-DnsClientServerAddress -InterfaceAlias "%s" -AddressFamily IPv4).ServerAddresses -join ", "`, iface)
	out, err := runCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
	clean := strings.TrimSpace(out)
	if err == nil && clean != "" {
		return clean
	}

	output, err := runCommand("netsh", "interface", "ipv4", "show", "dnsservers", "name="+iface)
	if err == nil {
		var dnsList []string
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "DNS servers configured through DHCP:") || strings.HasPrefix(line, "Statically Configured DNS Servers:") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					server := strings.TrimSpace(parts[1])
					if server != "" && server != "None" {
						dnsList = append(dnsList, server)
					}
				}
			} else if strings.Count(line, ".") == 3 {
				fields := strings.Fields(line)
				if len(fields) > 0 && strings.Count(fields[0], ".") == 3 {
					dnsList = append(dnsList, fields[0])
				}
			}
		}
		if len(dnsList) > 0 {
			return strings.Join(dnsList, ", ")
		}
	}
	return "192.168.0.1 (Router DHCP)"
}

func getCurrentProxy() string {
	rec := getActiveProxyRecord()
	if rec != "" {
		return rec
	}

	// 1. Check WinINET Registry (Browsers)
	regPath := `HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	psCheck := fmt.Sprintf(`(Get-ItemProperty -Path "%s" -Name ProxyEnable -ErrorAction SilentlyContinue).ProxyEnable`, regPath)
	out, err := runCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", psCheck)
	if err == nil && strings.TrimSpace(out) == "1" {
		psServer := fmt.Sprintf(`(Get-ItemProperty -Path "%s" -Name ProxyServer -ErrorAction SilentlyContinue).ProxyServer`, regPath)
		serverOut, serverErr := runCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", psServer)
		if serverErr == nil && strings.TrimSpace(serverOut) != "" {
			return strings.TrimSpace(serverOut)
		}
	}

	// 2. Check WinHTTP Subsystem (System)
	outWinHTTP, errWinHTTP := runCommand("netsh", "winhttp", "show", "proxy")
	if errWinHTTP == nil {
		if value := parseWinHTTPProxyOutput(outWinHTTP); value != "" {
			return value
		}
	}

	return "DIRECT (Disabled)"
}

// parseWinHTTPProxyOutput extracts the "Proxy Server(s)" value from
// `netsh winhttp show proxy` output, e.g. a line like
// "    Proxy Server(s) :  127.0.0.1:8080". Previously this was read by
// naively splitting the whole line on every colon and taking the second
// field -- which silently dropped the port, since a line like that
// contains two colons, not one (N12). Slicing past the known marker
// text instead of splitting on ":" fixes that regardless of how many
// colons the value itself contains. If the value parses as a
// host:port (via net.SplitHostPort, which understands bracketed IPv6),
// it's re-rendered via net.JoinHostPort for a canonical bracketed form;
// otherwise the trimmed raw value is returned as-is rather than
// erroring out, since this is a read/display-only path.
func parseWinHTTPProxyOutput(output string) string {
	const marker = "Proxy Server(s) :"
	for _, line := range strings.Split(output, "\n") {
		idx := strings.Index(line, marker)
		if idx == -1 {
			continue
		}
		value := strings.TrimSpace(line[idx+len(marker):])
		if value == "" {
			return ""
		}
		if host, port, err := net.SplitHostPort(value); err == nil {
			return net.JoinHostPort(host, port)
		}
		return value
	}
	return ""
}

// windowsVirtualInterfaceSubstrings lists common virtual-adapter name
// patterns `netsh interface show interface` can list that don't make
// sense to offer in this tool's static-IP picker: Hyper-V/WSL virtual
// switches (vEthernet) and a few of the most common VPN client/loopback
// adapter names. Necessarily best-effort and non-exhaustive -- VPN
// vendors name their adapters differently -- mirroring macOS's own
// filtering (isDarwinVirtualInterface) as closely as Windows's much
// less standardized adapter naming allows; anything not on this list is
// still shown, just not filtered.
var windowsVirtualInterfaceSubstrings = []string{
	"vEthernet",
	"TAP-Windows",
	"Npcap Loopback",
	"WireGuard",
}

// isWindowsVirtualInterface reports whether name is the loopback
// pseudo-interface or a common virtual/VPN adapter this tool's IP
// picker should not offer.
func isWindowsVirtualInterface(name string) bool {
	if strings.EqualFold(name, "Loopback Pseudo-Interface 1") {
		return true
	}
	for _, s := range windowsVirtualInterfaceSubstrings {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}

func getNetworkInterfaces() []string {
	output, err := runCommand(
		"netsh",
		"interface",
		"show",
		"interface",
	)
	if err != nil {
		return nil
	}

	interfaces := make([]string, 0)

	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)

		if line == "" ||
			strings.HasPrefix(line, "Admin State") ||
			strings.HasPrefix(line, "---") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		iface := strings.Join(fields[3:], " ")
		if isWindowsVirtualInterface(iface) {
			continue
		}

		interfaces = append(interfaces, iface)
	}

	return interfaces
}

func setPermanentIP(iface, cidr, gateway string) string {
	ip, prefix, err := splitCIDR(cidr)
	if err != nil {
		return Red + "[!] " + err.Error() + NC
	}

	mask, err := prefixToMask(prefix)
	if err != nil {
		return Red + "[!] " + err.Error() + NC
	}

	args := []string{
		"interface",
		"ipv4",
		"set",
		"address",
		"name=" + iface,
		"source=static",
		"address=" + ip,
		"mask=" + mask,
	}

	if strings.TrimSpace(gateway) != "" {
		args = append(args, "gateway="+gateway)
	}

	output, err := runCommand("netsh", args...)
	if err != nil {
		return commandError(
			"Failed to apply permanent static IPv4 configuration",
			output,
			err,
		)
	}

	return outputOrSuccess(
		output,
		"Permanent static IPv4 configuration applied to "+iface+".",
	)
}

func setTemporaryIP(iface, cidr string) string {
	ip, prefix, err := splitCIDR(cidr)
	if err != nil {
		return Red + "[!] " + err.Error() + NC
	}

	mask, err := prefixToMask(prefix)
	if err != nil {
		return Red + "[!] " + err.Error() + NC
	}

	output, err := runCommand(
		"netsh",
		"interface",
		"ipv4",
		"add",
		"address",
		"name="+iface,
		"address="+ip,
		"mask="+mask,
	)
	if err != nil {
		return commandError(
			"Failed to add secondary IPv4 address",
			output,
			err,
		)
	}

	return Yellow +
		"[i] Windows does not provide ephemeral IP behavior.\n\n" +
		Green +
		"[✓] Address added as a secondary IPv4 address on " +
		iface +
		"." +
		NC
}

func addIPAlias(iface, cidr string) string {
	return setTemporaryIP(iface, cidr)
}

func setDNS(iface string, servers []string, automatic bool) string {
	if automatic {
		psCmd := fmt.Sprintf(`Set-DnsClientServerAddress -InterfaceAlias "%s" -ResetServerAddresses`, iface)
		output, err := runCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
		if err != nil {
			_, _ = runCommand("netsh", "interface", "ipv4", "set", "dnsservers", "name="+iface, "source=dhcp")
		}
		return outputOrSuccess(output, "Automatic DHCP DNS restored for "+iface+".")
	}

	if len(servers) == 0 {
		return Red + "[!] At least one DNS server is required." + NC
	}

	var formattedServers []string
	for _, s := range servers {
		formattedServers = append(formattedServers, fmt.Sprintf(`"%s"`, s))
	}
	serverListStr := strings.Join(formattedServers, ",")

	psCmd := fmt.Sprintf(`Set-DnsClientServerAddress -InterfaceAlias "%s" -ServerAddresses @(%s)`, iface, serverListStr)
	output, err := runCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
	if err != nil {
		// Fallback to netsh
		_, _ = runCommand("netsh", "interface", "ipv4", "set", "dnsservers", "name="+iface, "source=static", "address="+servers[0], "validate=no")
		for idx, s := range servers[1:] {
			_, _ = runCommand("netsh", "interface", "ipv4", "add", "dnsservers", "name="+iface, "address="+s, "index="+strconv.Itoa(idx+2), "validate=no")
		}
	}

	return outputOrSuccess(output, "Custom DNS servers ("+strings.Join(servers, ", ")+") applied to "+iface+".")
}

// buildWindowsProxyEndpoint joins host and port into the single string
// used for the saved proxy record, the netsh proxy-server value, the
// WinINET registry value, and the display text. Pure and side-effect-free
// so it can be unit tested directly; net.JoinHostPort brackets IPv6
// literals (net.JoinHostPort("::1", "8080") == "[::1]:8080"), matching the
// bracket notation Windows proxy configuration already expects for IPv6.
func buildWindowsProxyEndpoint(host, port string) string {
	return net.JoinHostPort(host, port)
}

// buildWindowsNetshProxyArgs returns the exact argv passed to netsh to set
// the WinHTTP system proxy to endpoint.
func buildWindowsNetshProxyArgs(endpoint string) []string {
	return []string{"winhttp", "set", "proxy", "proxy-server=" + endpoint}
}

// buildWindowsProxyRegistryScript returns the PowerShell script that
// writes endpoint into the WinINET registry key at regPath.
func buildWindowsProxyRegistryScript(regPath, endpoint string) string {
	return fmt.Sprintf(`
		Set-ItemProperty -Path "%s" -Name ProxyEnable -Value 1
		Set-ItemProperty -Path "%s" -Name ProxyServer -Value "%s"
		Set-ItemProperty -Path "%s" -Name ProxyOverride -Value "<local>;localhost;127.0.0.1"
	`, regPath, regPath, endpoint, regPath)
}

func setSystemProxy(host, port string) string {
	endpoint := buildWindowsProxyEndpoint(host, port)
	saveActiveProxyRecord(endpoint)

	// 1. Configure System-Level WinHTTP (Services, CLI, PowerShell, Background Updates)
	output, err := runCommand("netsh", buildWindowsNetshProxyArgs(endpoint)...)
	if err != nil {
		return commandError("Failed to configure WinHTTP system proxy", output, err)
	}

	// 2. Configure User Profile & Browser Layer (WinINET -> Chrome, Edge, Brave)
	regPath := `HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	psScript := buildWindowsProxyRegistryScript(regPath, endpoint)
	_, _ = runCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)

	return Green +
		"[✓] Dual-Layer Windows Proxy Configured Successfully.\n" +
		"[i] Target Endpoint: " + endpoint + "\n" +
		"[i] Applied to: WinHTTP (System/CLI) & WinINET (Chrome/Edge/Desktop Apps)." +
		NC
}

func disableSystemProxy() string {
	removeActiveProxyRecord()

	// 1. Reset WinHTTP
	output, err := runCommand("netsh", "winhttp", "reset", "proxy")
	if err != nil {
		return commandError("Failed to reset WinHTTP system proxy", output, err)
	}

	// 2. Disable WinINET
	regPath := `HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	psScript := fmt.Sprintf(`
		Set-ItemProperty -Path "%s" -Name ProxyEnable -Value 0
		Set-ItemProperty -Path "%s" -Name ProxyServer -Value ""
	`, regPath, regPath)
	_, _ = runCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)

	return Green + "[✓] System and browser proxy disabled. DIRECT routing restored." + NC
}

func showBootModeMenu() {
	for {
		choice := SelectFromOptions(
			"WINDOWS SYSTEM MODE SWITCHER",
			"Use UP/DOWN to choose an action, then press ENTER:",
			[]string{
				"Switch GUI / PowerShell CLI mode immediately (Live Session)",
				"Set Startup Shell (GUI Desktop / CLI PowerShell) for next boot",
				"Return to the main menu",
			},
		)

		switch choice {
		case 0:
			handleWindowsImmediateSwitch()
		case 1:
			handleWindowsNextBootSwitch()
		default:
			return
		}
	}
}

func handleWindowsImmediateSwitch() {
	for {
		choice := SelectFromOptions(
			"SWITCH SYSTEM MODE IMMEDIATELY",
			"Choose the operational shell to activate now:",
			[]string{
				"Switch to GUI Desktop immediately (Launch Windows Explorer)",
				"Switch to PowerShell CLI Mode immediately (Stop Windows Explorer)",
				"Return to Previous Menu",
			},
		)

		switch choice {
		case 0:
			showHeader("CONFIRM SWITCH TO GUI DESKTOP")
			printBoxRow(" "+FgCyan+"[i] Launching Windows Explorer desktop shell..."+Reset, "\n")
			printBoxRow("     This will restore the Taskbar, Desktop icons, and Start menu.", "\n")
			printBoxBlankRow("\n")

			if !confirmAction("Start Windows Explorer desktop now?") {
				showHeader("ACTION CANCELLED")
				printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
				pauseAction()
				continue
			}

			cmd := exec.Command("cmd", "/c", "start", "explorer.exe")
			_ = cmd.Start()

			showHeader("GUI MODE RESTORED")
			printBoxRow(" "+FgEmerald+"[✓] Windows Explorer desktop and taskbar restarted successfully."+Reset, "\n")
			pauseAction()
			return

		case 1:
			showHeader("CLI MODE GUIDELINES & CONFIRMATION")
			printBoxRow(" "+FgAmber+Bold+"── OPERATIONAL NOTICE "+BorderColor+"────────────────────────────────────────"+Reset, "\n")
			printBoxRow("  "+FgRose+"[!] Windows Explorer will terminate immediately."+Reset, "\n")
			printBoxRow("      The Taskbar, Desktop icons, and Start menu will close.", "\n")
			printBoxRow("      This terminal window transforms into your interactive CLI shell.", "\n")
			printBoxBlankRow("\n")
			printBoxRow(" "+FgCyan+Bold+"── HOW TO RECOVER DESKTOP GUI ANYTIME "+BorderColor+"───────────────────────"+Reset, "\n")
			printBoxRow("  "+FgEmerald+"Method 1:"+Reset+" Type "+FgBright+Bold+"gui"+Reset+" or "+FgBright+Bold+"explorer"+Reset+" inside this CLI and press ENTER.", "\n")
			printBoxRow("  "+FgEmerald+"Method 2 (Hardware Failsafe):"+Reset+"", "\n")
			printBoxRow("      1. Press "+FgBright+Bold+"Ctrl + Shift + Esc"+Reset+" to open Windows Task Manager.", "\n")
			printBoxRow("      2. Click "+FgBright+Bold+"File"+Reset+" -> "+FgBright+Bold+"Run new task"+Reset+".", "\n")
			printBoxRow("      3. Type "+FgEmerald+Bold+"explorer.exe"+Reset+" and click OK to restore GUI.", "\n")
			printBoxBlankRow("\n")

			if !confirmAction("Switch immediately to PowerShell CLI mode?") {
				showHeader("ACTION CANCELLED")
				printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
				pauseAction()
				continue
			}

			// 1. Terminate Explorer desktop
			_, _ = runCommand("taskkill", "/f", "/im", "explorer.exe")

			// 2. Launch Interactive CLI Shell
			runWindowsInteractiveCLIShell()
			return

		default:
			return
		}
	}
}

func runWindowsInteractiveCLIShell() {
	clearScreen()
	fmt.Println(FgEmerald + Bold + "==============================================================================" + Reset)
	fmt.Println(FgBright + Bold + "               WINDOWS PURE CLI SHELL ENVIRONMENT (HEADLESS MODE)" + Reset)
	fmt.Println(FgEmerald + Bold + "==============================================================================" + Reset)
	fmt.Println(FgMuted + " [i] Windows Explorer GUI is currently STOPPED (RAM & CPU optimized)." + Reset)
	fmt.Println(FgCyan + " [i] Built-in Commands:" + Reset)
	fmt.Println(FgBright + "     • " + FgEmerald + "gui" + Reset + " or " + FgEmerald + "explorer" + Reset + "  : Restart Windows Explorer Desktop GUI")
	fmt.Println(FgBright + "     • " + FgEmerald + "wizard" + Reset + " or " + FgEmerald + "menu" + Reset + "   : Re-open Switcher Suite Wizard")
	fmt.Println(FgBright + "     • " + FgEmerald + "switcher-wizard" + Reset + "      : Launch Switcher Suite with root privileges")
	fmt.Println(FgBright + "     • " + FgEmerald + "cls" + Reset + "               : Clear terminal screen")
	fmt.Println(FgBright + "     • " + FgEmerald + "exit" + Reset + "              : Exit CLI session")
	fmt.Println(FgMuted + " [i] You can also run any standard PowerShell / CMD commands directly." + Reset)
	fmt.Println(FgEmerald + Bold + "==============================================================================" + Reset)
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)

	for {
		pwd, _ := os.Getwd()
		fmt.Printf("%sPS (CLI Mode)%s %s%s%s> ", FgEmerald+Bold, Reset, FgCyan, pwd, Reset)

		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		lower := strings.ToLower(input)

		if lower == "gui" || lower == "explorer" {
			fmt.Println(FgCyan + "[+] Starting Windows Explorer Desktop GUI..." + Reset)
			cmd := exec.Command("cmd", "/c", "start", "explorer.exe")
			_ = cmd.Start()
			fmt.Println(FgEmerald + "[✓] Desktop GUI restored successfully." + Reset)
			return
		}

		if lower == "wizard" || lower == "menu" || lower == "switcher" || lower == "switcher-wizard" {
			return
		}

		if lower == "cls" || lower == "clear" {
			clearScreen()
			continue
		}

		if lower == "exit" || lower == "quit" {
			fmt.Println(FgAmber + "[!] Exiting CLI mode shell." + Reset)
			return
		}

		cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", input)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
		fmt.Println()
	}
}

func handleWindowsNextBootSwitch() {
	for {
		choice := SelectFromOptions(
			"SET STARTUP SHELL FOR NEXT BOOT",
			"Choose what Windows loads after your next login/restart:",
			[]string{
				"Switch to GUI Desktop after next reboot (Windows Explorer)",
				"Switch to PowerShell CLI Mode after next reboot",
				"Return to Previous Menu",
			},
		)

		var targetShell string
		var modeName string

		execPath, err := os.Executable()
		if err != nil {
			execPath = "powershell.exe"
		}

		switch choice {
		case 0:
			// Direct switch to GUI (No guidelines needed)
			targetShell = "explorer.exe"
			modeName = "Standard Windows Explorer Desktop (GUI)"

			showHeader("CONFIRM STARTUP SHELL CONFIGURATION")
			printBoxRow(" "+FgMuted+"Target Shell: "+FgEmerald+Bold+modeName+Reset, "\n")
			printBoxBlankRow("\n")
			printBoxRow(" "+FgCyan+"[i] Restores default Windows Explorer (Desktop, Start Menu, Taskbar)."+Reset, "\n")
			printBoxBlankRow("\n")

		case 1:
			targetShell = fmt.Sprintf("\"%s\" --cli-shell", execPath)
			modeName = "PowerShell CLI Headless Shell"

			showHeader("STARTUP MODE GUIDELINES & CONFIRMATION")
			printBoxRow(" "+FgMuted+"Target Shell: "+FgEmerald+Bold+modeName+Reset, "\n")
			printBoxBlankRow("\n")
			printBoxRow(" "+FgCyan+Bold+"── RECOVERY GUIDELINES FOR NEXT BOOT ─────────────────────────"+Reset, "\n")
			printBoxRow("  "+FgBright+"1. Windows will boot directly into this CLI shell without Desktop GUI."+Reset, "\n")
			printBoxRow("  "+FgBright+"2. If you open Task Manager (Ctrl+Shift+Esc) -> File -> Run new task"+Reset, "\n")
			printBoxRow("     and type 'explorer.exe', it opens File Explorer folders, NOT Desktop GUI."+Reset, "\n")
			printBoxRow("  "+FgBright+"3. To permanently restore Desktop GUI from CLI mode:"+Reset, "\n")
			printBoxRow("     • "+FgEmerald+"Option A (Fastest):"+Reset+FgBright+" Type "+FgEmerald+Bold+"switcher-wizard"+Reset+FgBright+" directly in the terminal."+Reset, "\n")
			printBoxRow("     • "+FgEmerald+"Option B (Manual Flow):"+Reset, "\n")
			printBoxRow("       a) In File Explorer, navigate to the tool's downloaded folder.", "\n")
			printBoxRow("       b) Run "+FgBright+Bold+"autorun.bat"+Reset+" (or 'Switcher_Suite_Wizard.exe').", "\n")
			printBoxRow("       c) Select "+FgCyan+Bold+"System Mode Switcher"+Reset+".", "\n")
			printBoxRow("       d) Select "+FgCyan+Bold+"Set Startup Shell (GUI Desktop / CLI PowerShell) for next boot"+Reset+".", "\n")
			printBoxRow("       e) Choose "+FgEmerald+Bold+"Switch to GUI Desktop after next reboot"+Reset+".", "\n")
			printBoxRow("       f) Confirm immediate reboot to restore Desktop GUI.", "\n")
			printBoxBlankRow("\n")
			printBoxRow(" "+FgAmber+"[i] Tip: Enable Global CLI to call 'switcher-wizard' from anywhere."+Reset, "\n")
			printBoxBlankRow("\n")

		default:
			return
		}

		if !confirmAction("Apply this configuration for next boot?") {
			showHeader("ACTION CANCELLED")
			printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
			pauseAction()
			continue
		}

		if !requirePrivilege() {
			return
		}

		// Use native reg.exe to bypass PowerShell string parsing errors with spaces
		output, err := runCommand("reg", "add", "HKLM\\SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion\\Winlogon", "/v", "Shell", "/t", "REG_SZ", "/d", targetShell, "/f")

		if err != nil {
			showHeader("SYSTEM ERROR")
			printBoxRow(" "+FgRose+"[!] Failed to update Winlogon shell: "+err.Error()+Reset, "\n")
			if output != "" {
				printBoxRow("     "+output, "\n")
			}
			pauseAction()
			continue
		}

		showHeader("STARTUP SHELL CONFIGURED")
		printBoxRow(" "+FgEmerald+"[✓] Default startup shell successfully set to:"+Reset, "\n")
		printBoxRow("     "+FgBright+Bold+modeName+Reset, "\n")
		printBoxBlankRow("\n")
		printBoxRow(" "+FgCyan+"[i] Value written to: HKLM\\SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion\\Winlogon\\Shell"+Reset, "\n")
		printBoxBlankRow("\n")

		if confirmAction("Reboot Windows immediately to enter this mode now?") {
			showHeader("SYSTEM REBOOT INITIATED")
			printBoxRow(" "+FgAmber+Bold+"[!] Restarting Windows system now..."+Reset, "\n")
			_, _ = runCommand("shutdown.exe", "/r", "/t", "0")
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
	execDir := filepath.Dir(execPath)

	cmdShimPath := filepath.Join("C:\\Windows\\System32", cmdName+".cmd")
	batShimPath := filepath.Join("C:\\Windows\\System32", cmdName+".bat")

	if err := checkCliAliasSafeToClaim(cmdName, []string{cmdShimPath, batShimPath}); err != nil {
		return "", err
	}

	psScript := fmt.Sprintf(`
		$dir = '%s'
		$path = [Environment]::GetEnvironmentVariable('Path', 'Machine')
		if ($path -notlike "*$dir*") {
			[Environment]::SetEnvironmentVariable('Path', "$path;$dir", 'Machine')
		}
		$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
		if ($userPath -notlike "*$dir*") {
			[Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
		}
	`, execDir)

	_, _ = runCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)

	// Safe UAC auto-elevating shim: only ever forwards the fixed,
	// whitelisted --cli-shell flag across the elevation hop, never the raw
	// "%*" the caller typed -- see buildWindowsCliShim for why.
	shimContent := buildWindowsCliShim(execPath)

	_ = os.WriteFile(cmdShimPath, []byte(shimContent), 0644)
	_ = os.WriteFile(batShimPath, []byte(shimContent), 0644)

	saveActiveCliCommand(cmdName)
	return fmt.Sprintf("Global Windows CLI enabled for '%s'. Auto-root UAC prompt configured.", cmdName), nil
}

func disableGlobalCLI() (string, error) {
	cmdName := getActiveCliCommand()
	if cmdName == "" {
		cmdName = "switcher-wizard"
	}

	for _, p := range []string{
		filepath.Join("C:\\Windows\\System32", cmdName+".cmd"),
		filepath.Join("C:\\Windows\\System32", cmdName+".bat"),
	} {
		if isOwnedCliShimFile(p) {
			_ = os.Remove(p)
		}
	}

	removeActiveCliRecord()
	return fmt.Sprintf("Global Windows CLI for '%s' removed.", cmdName), nil
}

func getInterfaceIPAddresses(iface string) []string {
	output, err := runCommand("netsh", "interface", "ipv4", "show", "addresses", "name="+iface)
	if err != nil {
		return nil
	}

	var addrs []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "IP Address:") || strings.HasPrefix(line, "IP-Adresse:") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				ip := strings.TrimSpace(parts[1])
				if ip != "" {
					addrs = append(addrs, ip)
				}
			}
		}
	}
	return addrs
}

func deleteIPAlias(iface, cidr string) string {
	ip, _, _ := splitCIDR(cidr)
	if ip == "" {
		ip = cidr
	}
	output, err := runCommand("netsh", "interface", "ipv4", "delete", "address", "name="+iface, "address="+ip)
	if err != nil {
		return commandError("Failed to delete secondary IP address", output, err)
	}
	return outputOrSuccess(output, "Secondary IP address "+ip+" removed from "+iface+".")
}

func restartNetworkManagerService() string {
	interfaces := getNetworkInterfaces()
	if len(interfaces) > 0 {
		for _, iface := range interfaces {
			_, _ = runCommand("netsh", "interface", "set", "interface", "name="+iface, "admin=disabled")
			_, _ = runCommand("netsh", "interface", "set", "interface", "name="+iface, "admin=enabled")
		}
		return Green + "[✓] Windows network adapters cycled and refreshed successfully." + NC
	}
	return Yellow + "[!] No active Windows network adapters found to cycle." + NC
}

func runNetworkTroubleshooter() []DiagnosticStep {
	var steps []DiagnosticStep

	// 1. Hostname Integrity
	hostname, _ := os.Hostname()
	step1 := DiagnosticStep{
		Name:    "Windows Computer Name & NetBIOS Registration",
		Passed:  true,
		Details: fmt.Sprintf("Active Computer Name: %s (Host registered in Windows domain/workgroup).", hostname),
	}
	steps = append(steps, step1)

	// 2. Hardware Interfaces
	interfaces := getNetworkInterfaces()
	step2 := DiagnosticStep{Name: "Windows Network Adapters"}
	if len(interfaces) == 0 {
		step2.Passed = false
		step2.Details = "No active network adapters detected in netsh interface tables."
	} else {
		step2.Passed = true
		step2.Details = fmt.Sprintf("Detected %d active adapter(s). Primary: %s", len(interfaces), interfaces[0])
	}
	steps = append(steps, step2)

	// 3. Routing Table
	routeOut, _ := runCommand("route", "print", "0.0.0.0")
	step3 := DiagnosticStep{Name: "Default Gateway Routing (0.0.0.0/0)"}
	if !strings.Contains(routeOut, "0.0.0.0") {
		step3.Passed = false
		step3.Details = "Default gateway route missing from Windows routing table."
		if len(interfaces) > 0 {
			_, _ = runCommand("netsh", "interface", "ipv4", "set", "address", "name="+interfaces[0], "source=dhcp")
			step3.FixLog = "Reset adapter '" + interfaces[0] + "' to automatic DHCP to re-acquire gateway."
		}
	} else {
		step3.Passed = true
		step3.Details = "Default gateway route active in Windows kernel routing table."
	}
	steps = append(steps, step3)

	// 4. DNS Query
	step4 := DiagnosticStep{Name: "DNS Query Resolution (Port 53)"}
	dnsRes := testDNSResolution("1.1.1.1", "google.com")
	if strings.Contains(dnsRes, "SUCCESS") {
		step4.Passed = true
		step4.Details = "DNS resolver operational and resolving external domains."
	} else {
		step4.Passed = false
		step4.Details = "DNS lookup query failed."
		if len(interfaces) > 0 {
			_ = setDNS(interfaces[0], []string{"1.1.1.1", "8.8.8.8"}, false)
			step4.FixLog = "Configured fallback DNS (1.1.1.1, 8.8.8.8) on '" + interfaces[0] + "'."
		}
	}
	steps = append(steps, step4)

	// 5. Internet Reachability
	step5 := DiagnosticStep{Name: "Internet Reachability (ICMP Socket)"}
	pingOut := pingHost("1.1.1.1")
	if strings.Contains(pingOut, "Reply from") || strings.Contains(pingOut, "bytes from") {
		step5.Passed = true
		step5.Details = "Internet connection active. Ping latency nominal."
	} else {
		step5.Passed = false
		step5.Details = "ICMP ping to 1.1.1.1 failed."
		step5.FixLog = "Cycled network adapters and flushed DNS cache via ipconfig /flushdns."
		_, _ = runCommand("ipconfig", "/flushdns")
		_ = restartNetworkManagerService()
	}
	steps = append(steps, step5)

	return steps
}

func handleDirectBootCliShell() {
	runWindowsInteractiveCLIShell()
}

// ---------------------------------------------------------------------------
// SSH Server (Install / Enable) -- Windows
//
// Every PowerShell script run here is a fixed literal with zero
// interpolated/user-supplied string data (this feature takes no free-form
// input at all, only confirmations) -- the one exception,
// buildWindowsListeningPortCheckScript, interpolates a strictly-typed int
// that was itself parsed from sshd_config via strconv.Atoi, never a raw
// string, so there is nothing to inject. Every script is invoked as a
// single argv element passed to `powershell -NoProfile -NonInteractive
// -Command <script>`, never built by concatenating untrusted data into a
// shell command line.
// ---------------------------------------------------------------------------

const (
	windowsSSHCapabilityName = "OpenSSH.Server~~~~0.0.1.0"
	windowsSSHServiceName    = "sshd"
	windowsSSHFirewallRule   = "OpenSSH-Server-In-TCP"
	windowsSSHDConfigPath    = `C:\ProgramData\ssh\sshd_config`
	defaultWindowsSSHPort    = 22
)

func runWindowsPowerShell(script string) (string, error) {
	return runCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
}

func buildWindowsSSHCapabilityCheckScript() string {
	return `(Get-WindowsCapability -Online -Name ` + windowsSSHCapabilityName + `).State`
}

func buildWindowsSSHCapabilityInstallScript() string {
	return `Add-WindowsCapability -Online -Name ` + windowsSSHCapabilityName
}

func buildWindowsSetServiceAutomaticScript() string {
	return `Set-Service -Name ` + windowsSSHServiceName + ` -StartupType Automatic`
}

func buildWindowsStartServiceScript() string {
	return `Start-Service -Name ` + windowsSSHServiceName
}

func buildWindowsStopServiceScript() string {
	return `Stop-Service -Name ` + windowsSSHServiceName + ` -ErrorAction SilentlyContinue`
}

func buildWindowsDisableServiceScript() string {
	return `Set-Service -Name ` + windowsSSHServiceName + ` -StartupType Disabled`
}

func buildWindowsServiceStatusScript() string {
	return `(Get-Service -Name ` + windowsSSHServiceName + ` -ErrorAction SilentlyContinue).Status`
}

func buildWindowsServiceStartTypeScript() string {
	return `(Get-Service -Name ` + windowsSSHServiceName + ` -ErrorAction SilentlyContinue).StartType`
}

func buildWindowsFirewallEnabledScript() string {
	return `if ((Get-NetFirewallProfile -Profile Private).Enabled -eq $true -or (Get-NetFirewallProfile -Profile Domain).Enabled -eq $true) { "True" } else { "False" }`
}

func buildWindowsFirewallRuleExistsScript() string {
	return `if (Get-NetFirewallRule -Name ` + windowsSSHFirewallRule + ` -ErrorAction SilentlyContinue) { "True" } else { "False" }`
}

// buildWindowsFirewallRulePortScript returns the existing rule's
// configured LocalPort, if any -- used to confirm a pre-existing rule
// (one this tool didn't create) actually covers the real SSH port,
// rather than assuming any rule with the right Name is good enough.
func buildWindowsFirewallRulePortScript() string {
	return `(Get-NetFirewallPortFilter -AssociatedNetFirewallRule (Get-NetFirewallRule -Name ` +
		windowsSSHFirewallRule + ` -ErrorAction SilentlyContinue) -ErrorAction SilentlyContinue).LocalPort`
}

// buildWindowsFirewallRuleCreateScript takes a strictly-typed int (never
// a raw string), same reasoning as buildWindowsListeningPortCheckScript.
func buildWindowsFirewallRuleCreateScript(port int) string {
	return fmt.Sprintf(
		`New-NetFirewallRule -Name %s -DisplayName "OpenSSH Server (sshd)" -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort %d -Profile Private,Domain`,
		windowsSSHFirewallRule, port,
	)
}

func buildWindowsFirewallRuleRemoveScript() string {
	return `Remove-NetFirewallRule -Name ` + windowsSSHFirewallRule + ` -ErrorAction SilentlyContinue`
}

// buildWindowsListeningPortCheckScript takes a strictly-typed int (never a
// raw string -- callers only ever pass a value already parsed via
// strconv.Atoi), so there is no injection surface despite the
// interpolation.
func buildWindowsListeningPortCheckScript(port int) string {
	return fmt.Sprintf(`if (Get-NetTCPConnection -LocalPort %d -State Listen -ErrorAction SilentlyContinue) { "True" } else { "False" }`, port)
}

// parseWindowsCapabilityOutput reports whether `Get-WindowsCapability`'s
// .State property output is "Installed" (vs. "NotPresent" or any other
// value).
func parseWindowsCapabilityOutput(output string) bool {
	return strings.TrimSpace(output) == "Installed"
}

// parseWindowsServiceStatusOutput reports whether a Get-Service .Status
// property output is "Running".
func parseWindowsServiceStatusOutput(output string) bool {
	return strings.TrimSpace(output) == "Running"
}

// parseWindowsServiceStartTypeOutput reports whether a Get-Service
// .StartType property output is "Automatic".
func parseWindowsServiceStartTypeOutput(output string) bool {
	return strings.TrimSpace(output) == "Automatic"
}

// parseWindowsBoolOutput parses the "True"/"False" text this file's own
// `if (...) { "True" } else { "False" }`-style scripts always print.
func parseWindowsBoolOutput(output string) bool {
	return strings.TrimSpace(output) == "True"
}

// parseWindowsFirewallRulePortOutput parses
// buildWindowsFirewallRulePortScript's LocalPort output. ok is false if
// the rule doesn't exist (empty output) or LocalPort isn't a plain
// integer (e.g. a port range or "Any", which this tool never creates
// itself, so a pre-existing rule like that is correctly treated as not
// confirmed to match).
func parseWindowsFirewallRulePortOutput(output string) (port int, ok bool) {
	p, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return 0, false
	}
	return p, true
}

// parseWindowsSSHDConfigPort extracts the effective "Port" directive from
// sshd_config content, defaulting to OpenSSH's own documented default
// (defaultWindowsSSHPort) if no Port line is present or the file could
// not be read.
func parseWindowsSSHDConfigPort(configContent string) int {
	for _, line := range strings.Split(configContent, "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.EqualFold(fields[0], "Port") {
			if p, err := strconv.Atoi(fields[1]); err == nil {
				return p
			}
		}
	}
	return defaultWindowsSSHPort
}

func getSSHServerStatus() (SSHServerStatus, error) {
	var status SSHServerStatus
	status.ServiceName = windowsSSHServiceName

	if out, err := runWindowsPowerShell(buildWindowsSSHCapabilityCheckScript()); err == nil {
		status.Installed = parseWindowsCapabilityOutput(out)
	}

	if status.Installed {
		if out, err := runWindowsPowerShell(buildWindowsServiceStatusScript()); err == nil {
			status.Running = parseWindowsServiceStatusOutput(out)
		}
		if out, err := runWindowsPowerShell(buildWindowsServiceStartTypeScript()); err == nil {
			status.Enabled = parseWindowsServiceStartTypeOutput(out)
		}

		port := defaultWindowsSSHPort
		if content, err := os.ReadFile(windowsSSHDConfigPath); err == nil {
			port = parseWindowsSSHDConfigPort(string(content))
		} else {
			status.Detail = "Could not read " + windowsSSHDConfigPath + " to confirm the configured port; assuming the OpenSSH default of 22."
		}
		if out, err := runWindowsPowerShell(buildWindowsListeningPortCheckScript(port)); err == nil && parseWindowsBoolOutput(out) {
			status.ListeningPort = port
		}
	}

	// The firewall check below must confirm the rule covers the SSH
	// server's real port, never just assume any rule with the right
	// Name is good enough -- fall back to 22 only when the real port
	// genuinely couldn't be confirmed above, and say so -- but only
	// once we know the firewall is active (i.e. a port check will
	// actually happen below), so the note is never shown when the
	// firewall is off and no such check occurs at all.
	checkPort, usedFallback := sshPortOrFallback(status.ListeningPort, defaultWindowsSSHPort)

	status.FirewallTool = "Windows Defender Firewall"
	if out, err := runWindowsPowerShell(buildWindowsFirewallEnabledScript()); err == nil {
		status.FirewallActive = parseWindowsBoolOutput(out)
	}
	if status.FirewallActive {
		if usedFallback {
			note := fmt.Sprintf("Could not confirm the real listening port; firewall status below is checked against the default port %d.", defaultWindowsSSHPort)
			if status.Detail == "" {
				status.Detail = note
			} else {
				status.Detail += " " + note
			}
		}
		if existsOut, err := runWindowsPowerShell(buildWindowsFirewallRuleExistsScript()); err == nil && parseWindowsBoolOutput(existsOut) {
			if portOut, err := runWindowsPowerShell(buildWindowsFirewallRulePortScript()); err == nil {
				if rulePort, ok := parseWindowsFirewallRulePortOutput(portOut); ok {
					status.FirewallAllowsSSH = rulePort == checkPort
				}
			}
		}
	}

	return status, nil
}

func enableSSHServer() (string, error) {
	capOut, capErr := runWindowsPowerShell(buildWindowsSSHCapabilityCheckScript())
	if capErr != nil {
		return "", fmt.Errorf("failed to query the OpenSSH Server Windows capability: %w\n%s", capErr, capOut)
	}

	if !parseWindowsCapabilityOutput(capOut) {
		installOut, installErr := runWindowsPowerShell(buildWindowsSSHCapabilityInstallScript())
		if installErr != nil {
			return "", fmt.Errorf(
				"failed to install the OpenSSH Server capability via Windows Update/Features on Demand: %w\n%s\n"+
					"Manual alternative: Settings -> Apps -> Optional Features -> Add a feature -> OpenSSH Server, "+
					"or run as Administrator: Add-WindowsCapability -Online -Name %s",
				installErr, installOut, windowsSSHCapabilityName,
			)
		}
	}

	if out, err := runWindowsPowerShell(buildWindowsSetServiceAutomaticScript()); err != nil {
		return "", fmt.Errorf("failed to set the %s service to Automatic startup: %w\n%s", windowsSSHServiceName, err, out)
	}
	if out, err := runWindowsPowerShell(buildWindowsStartServiceScript()); err != nil {
		return "", fmt.Errorf("failed to start the %s service: %w\n%s", windowsSSHServiceName, err, out)
	}

	status, _ := getSSHServerStatus()
	if !status.Running {
		return "", fmt.Errorf("%s's StartupType was set and Start-Service was issued, but the service does not report Running -- check `Get-Service %s` and the Windows Event Log", windowsSSHServiceName, windowsSSHServiceName)
	}
	if status.ListeningPort == 0 {
		return "", errors.New("sshd service is running, but no listening port could be confirmed via Get-NetTCPConnection -- check Windows Defender Firewall or a custom sshd_config Port")
	}

	return fmt.Sprintf("SSH server installed, set to start automatically, and listening on port %d.", status.ListeningPort), nil
}

func disableSSHServer() (string, error) {
	if out, err := runWindowsPowerShell(buildWindowsStopServiceScript()); err != nil {
		return "", fmt.Errorf("failed to stop the %s service: %w\n%s", windowsSSHServiceName, err, out)
	}
	if out, err := runWindowsPowerShell(buildWindowsDisableServiceScript()); err != nil {
		return "", fmt.Errorf("failed to set the %s service to Disabled startup: %w\n%s", windowsSSHServiceName, err, out)
	}
	return "SSH server disabled (stopped, and will not start on boot). The OpenSSH Server Windows capability itself was left installed.", nil
}

func ensureFirewallAllowsSSH(port int) (string, error) {
	validPort, usedFallback := sshPortOrFallback(port, defaultWindowsSSHPort)

	var note string
	if usedFallback {
		note = fmt.Sprintf(" (could not confirm the real listening port, so the firewall rule targets the default port %d instead)", defaultWindowsSSHPort)
	}

	existsOut, existsErr := runWindowsPowerShell(buildWindowsFirewallRuleExistsScript())
	if existsErr == nil && parseWindowsBoolOutput(existsOut) {
		portOut, portErr := runWindowsPowerShell(buildWindowsFirewallRulePortScript())
		if portErr == nil {
			if rulePort, ok := parseWindowsFirewallRulePortOutput(portOut); ok && rulePort == validPort {
				return fmt.Sprintf("Firewall rule %q already allows SSH on port %d -- nothing to change.%s", windowsSSHFirewallRule, validPort, note), nil
			}
		}
		// A rule with this exact Name already exists but doesn't match
		// the port we need -- refuse to touch it rather than silently
		// overwrite something this tool didn't create (it may be the
		// one OpenSSH's own installer creates automatically).
		return "", fmt.Errorf(
			"a firewall rule named %q already exists but its LocalPort does not match %d -- remove or rename it manually before retrying, so this tool never overwrites a rule it didn't create",
			windowsSSHFirewallRule, validPort,
		)
	}

	if out, err := runWindowsPowerShell(buildWindowsFirewallRuleCreateScript(validPort)); err != nil {
		return "", fmt.Errorf("failed to create the %s firewall rule: %w\n%s", windowsSSHFirewallRule, err, out)
	}

	saveSSHFirewallRuleRecord("windows", validPort)

	return fmt.Sprintf("Firewall rule %q created for port %d (Private/Domain profiles only).%s", windowsSSHFirewallRule, validPort, note), nil
}

// removeSSHFirewallRule removes the OpenSSH-Server-In-TCP rule only if
// this tool's own record (sshFirewallRuleRecordFile, system_common.go)
// says it created it -- never a rule that already existed beforehand,
// such as one OpenSSH's own installer creates automatically.
func removeSSHFirewallRule() (string, error) {
	tool, port, ok := getSSHFirewallRuleRecord()
	if !ok || tool != "windows" {
		return "No firewall rule was created by this tool -- nothing to remove.", nil
	}

	if out, err := runWindowsPowerShell(buildWindowsFirewallRuleRemoveScript()); err != nil {
		return "", fmt.Errorf("failed to remove the %s firewall rule: %w\n%s", windowsSSHFirewallRule, err, out)
	}

	removeSSHFirewallRuleRecord()

	return fmt.Sprintf("Firewall rule %q (port %d, added by this tool) removed.", windowsSSHFirewallRule, port), nil
}
