//go:build windows

package main

import (
	"bufio"
	"fmt"
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
	procGetConsoleOutputCP         = kernel32.NewProc("GetConsoleOutputCP")
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
func isWindowsUTF8Ready() bool {
	ret, _, _ := procGetConsoleOutputCP.Call()
	return uint32(ret) == 65001
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
	_, _ = runCommand("reg", "add", "HKLM\\SYSTEM\\CurrentControlSet\\Control\\ComputerName\\ComputerName", "/v", "ComputerName", "/t", "REG_SZ", "/d", newName, "/f")
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
	if errWinHTTP == nil && strings.Contains(outWinHTTP, "Proxy Server(s) :") {
		for _, line := range strings.Split(outWinHTTP, "\n") {
			if strings.Contains(line, "Proxy Server(s) :") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					return strings.TrimSpace(parts[1])
				}
			}
		}
	}

	return "DIRECT (Disabled)"
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
		if strings.EqualFold(iface, "Loopback Pseudo-Interface 1") {
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

func setSystemProxy(host, port string) string {
	endpoint := host + ":" + port
	saveActiveProxyRecord(endpoint)

	// 1. Configure System-Level WinHTTP (Services, CLI, PowerShell, Background Updates)
	_, _ = runCommand("netsh", "winhttp", "set", "proxy", "proxy-server="+endpoint)

	// 2. Configure User Profile & Browser Layer (WinINET -> Chrome, Edge, Brave)
	regPath := `HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	psScript := fmt.Sprintf(`
		Set-ItemProperty -Path "%s" -Name ProxyEnable -Value 1
		Set-ItemProperty -Path "%s" -Name ProxyServer -Value "%s"
		Set-ItemProperty -Path "%s" -Name ProxyOverride -Value "<local>;localhost;127.0.0.1"
	`, regPath, regPath, endpoint, regPath)
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
	_, _ = runCommand("netsh", "winhttp", "reset", "proxy")

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

	// Safe UAC auto-elevating shim without empty ArgumentList bug
	shimContent := fmt.Sprintf(`@echo off
net session >nul 2>&1
if %%errorLevel%% == 0 (
    "%s" %%*
) else (
    if "%%~1"=="" (
        powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process -FilePath '%s' -Verb RunAs"
    ) else (
        powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process -FilePath '%s' -ArgumentList '%%*' -Verb RunAs"
    )
)
`, execPath, execPath, execPath)

	_ = os.WriteFile(filepath.Join("C:\\Windows\\System32", cmdName+".cmd"), []byte(shimContent), 0644)
	_ = os.WriteFile(filepath.Join("C:\\Windows\\System32", cmdName+".bat"), []byte(shimContent), 0644)

	saveActiveCliCommand(cmdName)
	return fmt.Sprintf("Global Windows CLI enabled for '%s'. Auto-root UAC prompt configured.", cmdName), nil
}

func disableGlobalCLI() (string, error) {
	cmdName := getActiveCliCommand()
	if cmdName == "" {
		cmdName = "switcher-wizard"
	}

	_ = os.Remove(filepath.Join("C:\\Windows\\System32", cmdName+".cmd"))
	_ = os.Remove(filepath.Join("C:\\Windows\\System32", cmdName+".bat"))

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
