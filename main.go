package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"golang.org/x/term"
)

// ANSI Color Theme (Enterprise High-Contrast Production Palette)
const (
	Reset     = "\033[0m"
	Bold      = "\033[1m"
	Dim       = "\033[2m"
	Italic    = "\033[3m"
	Underline = "\033[4m"

	// Structural Borders: Electric Cyan
	BorderColor = "\033[38;2;6;182;212m"
	BorderDim   = "\033[38;2;6;182;212m"

	// Typography & Accents
	FgBright  = "\033[38;2;248;250;252m"
	FgMuted   = "\033[38;2;148;163;184m"
	FgCyan    = "\033[38;2;56;189;248m"
	FgEmerald = "\033[38;2;52;211;153m"
	FgAmber   = "\033[38;2;251;191;36m"
	FgRose    = "\033[38;2;244;63;94m"
	FgIndigo  = "\033[38;2;129;140;248m"

	BgSelect = "\033[48;2;15;40;70m"
)

// Legacy constants
const (
	Red    = FgRose
	Green  = FgEmerald
	Yellow = FgAmber
	Blue   = FgIndigo
	Cyan   = FgCyan
	NC     = Reset
)

// Locked to compact width
const FixedBoxWidth = 88

func initAlternateScreen() {
	fmt.Print("\033[?1049h\033[2J\033[H\033[?25l")
}

func restoreNormalScreen() {
	fmt.Print("\033[?25h\033[?1049l")
}

func autoElevateIfUnprivileged() {
	if isElevated() {
		return
	}

	execPath, err := os.Executable()
	if err != nil {
		return
	}

	if runtime.GOOS == "windows" {
		var psArgs string
		args := strings.Join(os.Args[1:], " ")
		if strings.TrimSpace(args) != "" {
			psArgs = fmt.Sprintf(`Start-Process -FilePath '%s' -ArgumentList '%s' -Verb RunAs`, execPath, args)
		} else {
			psArgs = fmt.Sprintf(`Start-Process -FilePath '%s' -Verb RunAs`, execPath)
		}

		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psArgs)
		_ = cmd.Run()
		os.Exit(0)
	}

	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		cmd := exec.Command("sudo", append([]string{execPath}, os.Args[1:]...)...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err == nil {
			os.Exit(0)
		}
		os.Exit(1)
	}
}

func getTerminalWidth() int {
	fd := int(os.Stdout.Fd())
	width, _, err := term.GetSize(fd)
	if err != nil || width <= 0 {
		return FixedBoxWidth
	}
	if width < FixedBoxWidth {
		return FixedBoxWidth
	}
	return FixedBoxWidth
}

func getCurrentUser() string {
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		return sudoUser
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("LOGNAME"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "user"
}

func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

func clearToolScreen() {
	clearScreen()
}

func visibleWidth(value string) int {
	width := 0
	for index := 0; index < len(value); {
		if value[index] == '\033' && index+1 < len(value) && value[index+1] == '[' {
			index += 2
			for index < len(value) {
				char := value[index]
				if (char >= '0' && char <= '9') || char == ';' || char == '?' || char == 'm' {
					index++
					if char == 'm' {
						break
					}
					continue
				}
				index++
				break
			}
			continue
		}

		runeVal, size := utf8.DecodeRuneInString(value[index:])
		if runeVal == utf8.RuneError && size == 1 {
			index++
			width++
			continue
		}
		index += size
		width++
	}
	return width
}

func padDisplayWidth(value string, targetWidth int) string {
	curWidth := visibleWidth(value)
	if curWidth >= targetWidth {
		return truncateDisplayWidth(value, targetWidth)
	}
	return value + strings.Repeat(" ", targetWidth-curWidth)
}

func truncateDisplayWidth(value string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	var builder strings.Builder
	width := 0

	for index := 0; index < len(value); {
		if value[index] == '\033' && index+1 < len(value) && value[index+1] == '[' {
			start := index
			index += 2
			for index < len(value) {
				char := value[index]
				if (char >= '0' && char <= '9') || char == ';' || char == '?' || char == 'm' {
					index++
					if char == 'm' {
						break
					}
					continue
				}
				index++
				break
			}
			builder.WriteString(value[start:index])
			continue
		}

		runeVal, size := utf8.DecodeRuneInString(value[index:])
		if runeVal == utf8.RuneError && size == 1 {
			if width >= maxWidth {
				break
			}
			builder.WriteByte(value[index])
			index++
			width++
			continue
		}
		if width >= maxWidth {
			break
		}
		builder.WriteRune(runeVal)
		index += size
		width++
	}
	return builder.String()
}

func printBoxTop(newline string) {
	w := getTerminalWidth()
	fmt.Print(BorderColor + "╔" + strings.Repeat("═", w-2) + "╗" + Reset + newline)
}

func printBoxSeparator(newline string) {
	w := getTerminalWidth()
	fmt.Print(BorderColor + "╠" + strings.Repeat("═", w-2) + "╣" + Reset + newline)
}

func printBoxBottom(newline string) {
	w := getTerminalWidth()
	fmt.Print(BorderColor + "╚" + strings.Repeat("═", w-2) + "╝" + Reset + newline)
}

func printBoxRow(content, newline string) {
	w := getTerminalWidth()
	fmt.Print(BorderColor + "║" + Reset + padDisplayWidth(content, w-2) + BorderColor + "║" + Reset + newline)
}

func printBoxBlankRow(newline string) {
	printBoxRow("", newline)
}

func printHeaderSection(moduleTitle string, newline string) {
	w := getTerminalWidth()
	innerWidth := w - 2

	currentUser := getCurrentUser()

	privilegeTag := FgAmber + Bold + "UNPRIVILEGED" + Reset
	if isElevated() {
		privilegeTag = FgEmerald + Bold + "ROOT / ADMIN" + Reset
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "localhost"
	}

	currentProxy := getCurrentProxy()
	var proxyTag string
	if currentProxy == "" || strings.Contains(currentProxy, "DIRECT") {
		proxyTag = FgMuted + "DIRECT" + Reset
	} else {
		proxyTag = FgEmerald + Bold + currentProxy + Reset
	}

	printBoxTop(newline)

	titleBadge := " " + BgSelect + FgBright + Bold + " ❖  SWITCHER SUITE WIZARD " + Reset + FgEmerald + Bold + " [v2.5 Enterprise] " + Reset
	rightHost := FgMuted + "Host: " + FgBright + Bold + hostname + Reset + " "
	spaces := innerWidth - visibleWidth(titleBadge) - visibleWidth(rightHost)
	if spaces < 0 {
		spaces = 0
	}
	printBoxRow(titleBadge+strings.Repeat(" ", spaces)+rightHost, newline)

	printBoxSeparator(newline)

	leftSub := " " + FgMuted + "Platform: " + FgBright + runtime.GOOS + "/" + runtime.GOARCH + Reset + FgMuted + " │ Operator: " + FgBright + currentUser + Reset
	rightSub := FgMuted + "Privileges: " + privilegeTag + " "
	spaces = innerWidth - visibleWidth(leftSub) - visibleWidth(rightSub)
	if spaces < 0 {
		spaces = 0
	}
	printBoxRow(leftSub+strings.Repeat(" ", spaces)+rightSub, newline)

	leftGit := " " + FgAmber + Bold + "GitHub: " + Reset + FgBright + Underline + "https://github.com/ali4210" + Reset
	rightProxy := FgMuted + "Proxy: " + proxyTag + " "
	spaces = innerWidth - visibleWidth(leftGit) - visibleWidth(rightProxy)
	if spaces < 0 {
		spaces = 0
	}
	printBoxRow(leftGit+strings.Repeat(" ", spaces)+rightProxy, newline)

	printBoxSeparator(newline)

	modHeader := " " + FgIndigo + Bold + "ACTIVE MODULE => " + Reset + FgBright + Bold + strings.ToUpper(moduleTitle) + Reset
	printBoxRow(modHeader, newline)
	printBoxSeparator(newline)
}

func showHeader(title string) {
	clearScreen()
	printHeaderSection(title, "\n")
}

func printMainMenu(selected int, newline string) {
	var buf strings.Builder
	buf.WriteString("\033[H\033[2J")

	w := getTerminalWidth()
	fd := int(os.Stdout.Fd())
	_, terminalHeight, err := term.GetSize(fd)
	if err != nil || terminalHeight <= 0 {
		terminalHeight = 40
	}

	innerWidth := w - 2

	// Primary Artwork Engine (Your Favorite Graphic)
	favoriteGraphicArt := []string{
		"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣀⣀⣤⣤⣤⣤⣄⡀⠀⠀",
		"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣀⣀⣤⣤⣶⣶⣿⣿⢋⣭⣅⣠⣄⠠⣻⣷⣄⠀",
		"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣀⣤⣤⣴⣶⣾⣿⠿⠿⠛⠛⠋⠉⠁⣿⣿⣿⠈⢙⡿⣿⣿⣼⣿⣿⢿⣆",
		"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣀⡀⠤⠄⣤⣶⣾⠿⠿⠛⠛⠛⠉⠉⢀⣿⣿⣿⢾⣿⣿⣬⣬⠛⠛⠁⠌⣿",
		"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣀⣀⠤⠴⢒⣚⠭⠥⠀⠂⡰⠊⠉⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠑⠦⢸⣿⣿⡏⠀⠀⠹⡿⠟⠀⠀⢸⠀⢹",
		"⠀⠀⠀⠀⠀⠀⠀⠀⠀⣀⣠⠤⠔⣒⣊⠭⠄⠒⠒⠉⠉⠀⠀⠀⠀⢀⠔⠋⠀⠀⠀⣠⠔⠒⠒⢄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⣿⡇⢀⣶⣿⣿⣿⣦⡀⢸⠀⢸",
		"⠀⠀⠀⠀⠀⠀⡠⠒⠉⠀⡼⠀⠏⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣤⡴⣛⡞⠉⠉⢳⣜⣡⠊⢩⠂⡼⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⡿⡇⠟⣿⣯⣤⣿⣿⠇⠈⠀⢸",
		"⠀⠀⠀⠀⣠⠞⠓⣦⡄⢠⠃⠸⠀⠀⠀⠀⠀⠀⠀⠀⠀⣠⢿⣋⣽⢻⠃⣀⡀⡏⠀⢀⡴⢁⠜⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⡇⡇⠀⠘⠿⠿⠟⠁⠀⢰⠀⢸",
		"⠀⠀⠀⢠⢻⣤⣶⣿⠇⡎⠀⡆⠀⠀⠀⠀⠀⠀⢀⡴⠋⡼⡌⠀⡞⣾⠈⠋⢡⣡⠔⠁⠔⠁⢀⡆⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣸⣿⠃⡇⢠⣤⣄⠀⠀⠀⠀⢸⠀⢸",
		"⠀⠀⠀⡎⠀⠙⠉⠁⢠⠁⢸⠁⠀⠀⠀⠀⣀⠔⠉⠀⠀⣇⢧⣠⣣⣿⠀⢤⡊⠧⡖⠒⡂⠉⢹⡄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⠖⣿⣿⠀⡇⠘⢿⡋⠀⠀⠀⠀⢸⠀⢸",
		"⠀⠀⢠⠁⢠⣴⢆⡀⡼⠀⡇⠀⠀⠀⣠⠞⠁⠀⠀⠀⢠⣤⣽⣴⣿⣿⡟⣾⣧⣿⡿⠰⠁⠀⢸⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠔⠁⠀⣿⣿⢀⡇⢸⣿⡇⠀⠀⢀⣄⡏⠀⡌",
		"⠀⠀⣾⣼⢇⡹⠿⢣⡇⡸⠀⠀⠠⠊⠈⠁⠀⠀⠀⠀⡽⣛⣻⣿⣹⣿⢻⢱⠚⢯⣛⡄⠀⠀⠚⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣿⣿⢸⠁⠀⠀⠀⠀⠀⣨⡞⠀⣰⠃",
		"⠀⣸⠁⠙⠾⠃⠀⣾⠇⡇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢿⠧⠛⠞⠓⠁⠉⠈⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣀⣀⣰⣿⣿⢸⠀⠀⠀⣀⣠⣶⣷⣇⣠⠃⠀",
		"⢀⠇⠀⠀⣶⡿⢰⣿⣼⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠔⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣀⣀⣠⣤⣤⣴⣶⣶⣶⣿⣿⣟⣿⣿⣿⣿⣿⣿⣿⣿⣶⣿⣿⣿⣿⣿⡿⠛⠁⠀⠀",
		"⢸⠀⠀⠀⠀⠀⣎⣿⡏⠀⠀⠀⠀⠀⠀⠀⢀⣀⣀⣤⣤⣤⣤⣤⣶⣶⣶⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⠿⠿⠿⠛⠛⠛⠛⠋⠉⠁⠀⠀⠀⠀⠀",
		"⢸⡇⠀⠀⠀⢠⣼⣿⣥⣤⣴⣶⣶⣾⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⡿⠿⠿⠿⠛⠛⠛⠉⠉⠉⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
		"⠈⢿⣶⣴⣶⣾⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⡿⠿⠿⠿⠛⠛⠛⠋⠉⠉⠉⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
		"⠀⠀⠉⠛⠻⠿⠟⠛⠛⠛⠛⠉⠉⠉⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	}

	// Secondary Fallback Engine (Solid Switcher Typography)
	switcherFallbackArt := []string{
		"  ███████╗██╗    ██╗██╗████████╗ ██████╗██╗  ██╗███████╗██████╗ ",
		"  ██╔════╝██║    ██║██║╚══██╔══╝██╔════╝██║  ██║██╔════╝██╔══██╗",
		"  ███████╗██║ █╗ ██║██║   ██║   ██║     ███████║█████╗  ██████╔╝",
		"  ╚════██║██║███╗██║██║   ██║   ██║     ██╔══██║██╔══╝  ██╔══██╗",
		"  ███████║╚███╔███╔╝██║   ██║   ╚██████╗██║  ██║███████╗██║  ██║",
		"  ╚══════╝ ╚══╝╚══╝ ╚═╝   ╚═╝    ╚═════╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝",
	}

	// Cross-platform rendering decision
	renderPrimary := runtime.GOOS != "windows"

	if renderPrimary {
		for _, line := range favoriteGraphicArt {
			artWidth := visibleWidth(line)
			leftPadding := 0
			if w > artWidth {
				leftPadding = (w - artWidth) / 2
			}
			buf.WriteString(strings.Repeat(" ", leftPadding) + FgCyan + line + Reset + "\033[K" + newline)
		}
	} else {
		for _, line := range switcherFallbackArt {
			artWidth := visibleWidth(line)
			leftPadding := 0
			if w > artWidth {
				leftPadding = (w - artWidth) / 2
			}
			buf.WriteString(strings.Repeat(" ", leftPadding) + FgCyan + Bold + line + Reset + "\033[K" + newline)
		}
	}

	currentUser := getCurrentUser()
	privilegeTag := FgAmber + Bold + "UNPRIVILEGED" + Reset
	if isElevated() {
		privilegeTag = FgEmerald + Bold + "ROOT / ADMIN" + Reset
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "localhost"
	}

	currentProxy := getCurrentProxy()
	var proxyTag string
	if currentProxy == "" || strings.Contains(currentProxy, "DIRECT") {
		proxyTag = FgMuted + "DIRECT" + Reset
	} else {
		proxyTag = FgEmerald + Bold + currentProxy + Reset
	}

	buf.WriteString(BorderColor + "╔" + strings.Repeat("═", w-2) + "╗" + Reset + "\033[K" + newline)

	titleBadge := " " + BgSelect + FgBright + Bold + " ❖ SWITCHER SUITE WIZARD " + Reset + FgEmerald + Bold + " [v2.5 Enterprise] " + Reset
	rightHost := FgMuted + "Host: " + FgBright + Bold + hostname + Reset + " "
	spaces := innerWidth - visibleWidth(titleBadge) - visibleWidth(rightHost)
	if spaces < 0 {
		spaces = 0
	}
	buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(titleBadge+strings.Repeat(" ", spaces)+rightHost, w-2) + BorderColor + "║" + Reset + "\033[K" + newline)
	buf.WriteString(BorderColor + "╠" + strings.Repeat("═", w-2) + "╣" + Reset + "\033[K" + newline)

	leftSub := " " + FgMuted + "Platform: " + FgBright + runtime.GOOS + "/" + runtime.GOARCH + Reset + FgMuted + " │ Operator: " + FgBright + currentUser + Reset
	rightSub := FgMuted + "Privileges: " + privilegeTag + " "
	spaces = innerWidth - visibleWidth(leftSub) - visibleWidth(rightSub)
	if spaces < 0 {
		spaces = 0
	}
	buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(leftSub+strings.Repeat(" ", spaces)+rightSub, w-2) + BorderColor + "║" + Reset + "\033[K" + newline)

	leftGit := " " + FgAmber + Bold + "GitHub: " + Reset + FgBright + Underline + "https://github.com/ali4210" + Reset
	rightProxy := FgMuted + "Proxy: " + proxyTag + " "
	spaces = innerWidth - visibleWidth(leftGit) - visibleWidth(rightProxy)
	if spaces < 0 {
		spaces = 0
	}
	buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(leftGit+strings.Repeat(" ", spaces)+rightProxy, w-2) + BorderColor + "║" + Reset + "\033[K" + newline)
	buf.WriteString(BorderColor + "╠" + strings.Repeat("═", w-2) + "╣" + Reset + "\033[K" + newline)

	modHeader := " " + FgIndigo + Bold + "ACTIVE MODULE => " + Reset + FgBright + Bold + "MAIN CONTROL CENTER" + Reset
	buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(modHeader, w-2) + BorderColor + "║" + Reset + "\033[K" + newline)
	buf.WriteString(BorderColor + "╠" + strings.Repeat("═", w-2) + "╣" + Reset + "\033[K" + newline)

	menuItems := []struct {
		category string
		title    string
		desc     string
	}{
		{"SYSTEM MANAGEMENT", "System Mode Switcher", "Switch between Graphical Desktop GUI and TTY / CLI terminal mode"},
		{"SYSTEM MANAGEMENT", "System Hostname Manager", "Change system computer name in real-time or restore previous name"},
		{"NETWORK TOOLS", "IP Address Manager", "Inspect all adapter IPs, configure static IPs, and manage aliases"},
		{"NETWORK TOOLS", "DNS Resolver Switcher", "Apply Public DNS profiles, ping test resolvers, or restore DHCP"},
		{"NETWORK TOOLS", "Proxy Configuration", "Enable, configure, or disable operating-system proxy endpoints"},
		{"NETWORK TOOLS", "Restart Network Service", "Force restart NetworkManager daemon or refresh network bindings"},
		{"NETWORK TOOLS", "Automated Network Troubleshooter", "Run real-time diagnostics, fix missing gateways, and restore connectivity"},
		{"GLOBAL INTEGRATION", "Global CLI Integration", "Access Switcher Suite globally using 'switcher-wizard' with auto-root"},
		{"CONTROL", "Exit Switcher Suite", "Terminate current session and return safely to shell"},
	}

	lastCategory := ""

	for i, item := range menuItems {
		if item.category != lastCategory {
			catTitle := " " + FgIndigo + Bold + "── " + item.category + " " + Reset
			rem := innerWidth - visibleWidth(catTitle) - 1
			if rem < 0 {
				rem = 0
			}
			buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(catTitle+BorderColor+strings.Repeat("─", rem)+Reset, w-2) + BorderColor + "║" + Reset + "\033[K" + newline)
			lastCategory = item.category
		}

		if i == selected {
			titleRow := " " + FgEmerald + Bold + "=> " + BgSelect + FgBright + Bold + " " + item.title + " " + Reset
			descRow := "     " + FgEmerald + item.desc + Reset
			buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(titleRow, w-2) + BorderColor + "║" + Reset + "\033[K" + newline)
			buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(descRow, w-2) + BorderColor + "║" + Reset + "\033[K" + newline)
		} else {
			titleRow := "    " + FgBright + item.title + Reset
			descRow := "     " + FgMuted + item.desc + Reset
			buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(titleRow, w-2) + BorderColor + "║" + Reset + "\033[K" + newline)
			buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(descRow, w-2) + BorderColor + "║" + Reset + "\033[K" + newline)
		}
	}

	buf.WriteString(BorderColor + "╠" + strings.Repeat("═", w-2) + "╣" + Reset + "\033[K" + newline)

	footer := " " + FgCyan + "[↑/↓] Navigate" + Reset + FgMuted + " │ " + Reset +
		FgEmerald + "[ENTER] Select" + Reset + FgMuted + " │ " + Reset +
		FgAmber + "[Q] Exit" + Reset
	buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(footer, w-2) + BorderColor + "║" + Reset + "\033[K" + newline)
	buf.WriteString(BorderColor + "╚" + strings.Repeat("═", w-2) + "╝" + Reset + "\033[K" + newline)

	fmt.Print(buf.String())
}

func readInput(prompt string) string {
	fmt.Print("\033[?25h")
	defer fmt.Print("\033[?25l")

	promptStr := " " + FgCyan + Bold + "=> " + Reset + FgBright + prompt + Reset
	printBoxRow(promptStr, "\n")
	printBoxBottom("\n")

	fmt.Print(FgCyan + " ╔═ " + Reset + FgBright + "Input: " + Reset)
	reader := bufio.NewReader(os.Stdin)
	text, err := reader.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(text)
}

func pauseAction() {
	printBoxSeparator("\n")
	printBoxRow(" "+FgCyan+"Press [ENTER] to return to the wizard..."+Reset, "\n")
	printBoxBottom("\n")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func confirmAction(message string) bool {
	fmt.Print("\033[?25h")
	defer fmt.Print("\033[?25l")

	printBoxSeparator("\n")
	printBoxRow(" "+FgAmber+Bold+"[?] "+Reset+FgBright+message+FgMuted+" (Type 'y' to confirm)"+Reset, "\n")
	printBoxBottom("\n")

	fmt.Print(FgAmber + " ╔═ " + Reset + FgBright + "Confirm [y/N]: " + Reset)
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	clean := strings.ToLower(strings.TrimSpace(answer))
	return clean == "y" || clean == "yes"
}

func selectMainMenu() int {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return selectMainMenuFallback()
	}
	defer term.Restore(fd, oldState)

	selected := 0
	reader := bufio.NewReader(os.Stdin)

	for {
		printMainMenu(selected, "\r\n")

		key, err := reader.ReadByte()
		if err != nil {
			continue
		}

		switch key {
		case '\r', '\n':
			return selected
		case 'q', 'Q':
			return 8
		case 27:
			next1, err1 := reader.ReadByte()
			next2, err2 := reader.ReadByte()
			if err1 != nil || err2 != nil || next1 != '[' {
				continue
			}
			switch next2 {
			case 'A':
				if selected == 0 {
					selected = 8
				} else {
					selected--
				}
			case 'B':
				selected = (selected + 1) % 9
			}
		}
	}
}

func SelectFromOptions(title, prompt string, options []string) int {
	if len(options) == 0 {
		return -1
	}

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return selectFallback(title, prompt, options)
	}
	defer term.Restore(fd, oldState)

	selected := 0
	reader := bufio.NewReader(os.Stdin)

	clearScreen()

	for {
		var buf strings.Builder
		buf.WriteString("\033[H")

		w := getTerminalWidth()

		buf.WriteString(BorderColor + "╔" + strings.Repeat("═", w-2) + "╗" + Reset + "\r\n")
		modHeader := " " + FgIndigo + Bold + "ACTIVE MODULE => " + Reset + FgBright + Bold + strings.ToUpper(title) + Reset
		buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(modHeader, w-2) + BorderColor + "║" + Reset + "\r\n")
		buf.WriteString(BorderColor + "╠" + strings.Repeat("═", w-2) + "╣" + Reset + "\r\n")

		promptLines := strings.Split(prompt, "\n")
		for _, pl := range promptLines {
			buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(" "+FgCyan+Bold+pl+Reset, w-2) + BorderColor + "║" + Reset + "\r\n")
		}
		buf.WriteString(BorderColor + "╠" + strings.Repeat("═", w-2) + "╣" + Reset + "\r\n")

		for i, option := range options {
			if i == selected {
				optRow := " " + FgEmerald + Bold + "=> " + BgSelect + FgBright + Bold + " " + option + " " + Reset
				buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(optRow, w-2) + BorderColor + "║" + Reset + "\r\n")
			} else {
				optRow := "   " + FgMuted + option + Reset
				buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(optRow, w-2) + BorderColor + "║" + Reset + "\r\n")
			}
		}

		buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth("", w-2) + BorderColor + "║" + Reset + "\r\n")
		buf.WriteString(BorderColor + "╠" + strings.Repeat("═", w-2) + "╣" + Reset + "\r\n")
		footer := " " + FgCyan + "[↑/↓] Navigate" + Reset + FgMuted + " │ " + Reset +
			FgEmerald + "[ENTER] Choose" + Reset + FgMuted + " │ " + Reset +
			FgAmber + "[Q] Cancel" + Reset
		buf.WriteString(BorderColor + "║" + Reset + padDisplayWidth(footer, w-2) + BorderColor + "║" + Reset + "\r\n")
		buf.WriteString(BorderColor + "╚" + strings.Repeat("═", w-2) + "╝" + Reset + "\r\n")

		fmt.Print(buf.String())

		key, err := reader.ReadByte()
		if err != nil {
			continue
		}

		switch key {
		case '\r', '\n':
			return selected
		case 'q', 'Q':
			return -1
		case 27:
			next1, err1 := reader.ReadByte()
			next2, err2 := reader.ReadByte()
			if err1 != nil || err2 != nil || next1 != '[' {
				continue
			}
			switch next2 {
			case 'A':
				if selected == 0 {
					selected = len(options) - 1
				} else {
					selected--
				}
			case 'B':
				selected = (selected + 1) % len(options)
			}
		}
	}
}

func selectMainMenuFallback() int {
	showHeader("Main Menu")
	printBoxRow(" "+FgIndigo+Bold+"── SYSTEM MANAGEMENT "+BorderColor+"───────────────────────"+Reset, "\n")
	printBoxRow("  [1] System Mode Switcher", "\n")
	printBoxRow("  [2] System Hostname Manager", "\n")
	printBoxBlankRow("\n")
	printBoxRow(" "+FgIndigo+Bold+"── NETWORK TOOLS "+BorderColor+"─────────────────────────────"+Reset, "\n")
	printBoxRow("  [3] IP Address Changer", "\n")
	printBoxRow("  [4] DNS Switcher", "\n")
	printBoxRow("  [5] Proxy Switcher", "\n")
	printBoxRow("  [6] Restart Network Service", "\n")
	printBoxRow("  [7] Automated Network Troubleshooter", "\n")
	printBoxBlankRow("\n")
	printBoxRow(" "+FgIndigo+Bold+"── GLOBAL INTEGRATION "+BorderColor+"──────────────────────"+Reset, "\n")
	printBoxRow("  [8] Global CLI Integration", "\n")
	printBoxBlankRow("\n")
	printBoxRow("  [0] Exit Wizard", "\n")
	printBoxSeparator("\n")

	choice := readInput("Select option number: ")
	switch choice {
	case "1":
		return 0
	case "2":
		return 1
	case "3":
		return 2
	case "4":
		return 3
	case "5":
		return 4
	case "6":
		return 5
	case "7":
		return 6
	case "8":
		return 7
	default:
		return 8
	}
}

func selectFallback(title, prompt string, options []string) int {
	showHeader(title)
	printBoxRow(" "+FgCyan+prompt+Reset, "\n")
	printBoxSeparator("\n")

	for index, option := range options {
		printBoxRow(fmt.Sprintf("  [%d] %s", index+1, option), "\n")
	}
	printBoxRow("  [0] Cancel", "\n")
	printBoxSeparator("\n")

	choiceText := readInput("Enter choice: ")
	choice, err := strconv.Atoi(choiceText)
	if err != nil || choice < 1 || choice > len(options) {
		return -1
	}
	return choice - 1
}

func requirePrivilege() bool {
	if isElevated() {
		return true
	}

	showHeader("ELEVATION REQUIRED")
	printBoxRow(" "+FgRose+Bold+"[!] ROOT/ADMINISTRATOR PRIVILEGES REQUIRED"+Reset, "\n")
	printBoxBlankRow("\n")

	if runtime.GOOS == "windows" {
		printBoxRow(" "+FgAmber+"=> Please start PowerShell or CMD with 'Run as Administrator'."+Reset, "\n")
	} else {
		printBoxRow(" "+FgAmber+"=> Please relaunch the wizard with sudo:"+Reset, "\n")
		printBoxRow("    "+FgCyan+Bold+"sudo ./Switcher_Suite_Wizard"+Reset, "\n")
	}

	pauseAction()
	return false
}

func globalCliMenu() {
	for {
		currentAlias := getActiveCliCommand()
		if currentAlias == "" {
			currentAlias = "Not Configured"
		}

		options := []string{
			"Enable Global CLI (Default: switcher-wizard)",
			"Enable Global CLI with Custom Command Name",
			"Disable Global CLI (Remove from PATH/Shell Profiles)",
			"Return to Main Menu",
		}

		choice := SelectFromOptions(
			"GLOBAL CLI INTEGRATION",
			fmt.Sprintf("Active Global Command: %s%s%s\nSelect Action:", FgEmerald+Bold, currentAlias, Reset),
			options,
		)

		switch choice {
		case 0:
			enableGlobalCliFlow("switcher-wizard")
		case 1:
			showHeader("CUSTOM CLI COMMAND ALIAS")
			customName := readInput("Enter CLI Command (e.g. switcher or sw): ")
			if err := validateCliCommand(customName); err != nil {
				showHeader("VALIDATION ERROR")
				printBoxRow(" "+FgRose+"[!] "+err.Error()+Reset, "\n")
				pauseAction()
				continue
			}
			enableGlobalCliFlow(customName)
		case 2:
			disableGlobalCliFlow()
		default:
			return
		}
	}
}

func enableGlobalCliFlow(cmdName string) {
	showHeader("ENABLE GLOBAL CLI")
	printBoxRow(" "+FgMuted+"Command to Register: "+FgEmerald+Bold+cmdName+Reset, "\n")
	printBoxBlankRow("\n")
	printBoxRow(" "+FgCyan+"[i] Injects auto-root wrapper into system path & shell profiles."+Reset, "\n")

	if !confirmAction(fmt.Sprintf("Register '%s' as a global terminal command?", cmdName)) {
		showHeader("ACTION CANCELLED")
		printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	showHeader("CONFIGURING GLOBAL CLI")
	res, err := enableGlobalCLI(cmdName)
	if err != nil {
		printBoxRow(" "+FgRose+"[!] Error: "+err.Error()+Reset, "\n")
	} else {
		lines := strings.Split(res, "\n")
		for _, l := range lines {
			printBoxRow(" "+FgEmerald+"[✓] "+l+Reset, "\n")
		}
	}
	pauseAction()
}

func disableGlobalCliFlow() {
	showHeader("DISABLE GLOBAL CLI")
	printBoxRow(" "+FgAmber+"[!] Removes wrapper script and purges shell aliases."+Reset, "\n")

	if !confirmAction("Remove global CLI command integration?") {
		showHeader("ACTION CANCELLED")
		printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	showHeader("REMOVING GLOBAL CLI")
	res, err := disableGlobalCLI()
	if err != nil {
		printBoxRow(" "+FgRose+"[!] Error: "+err.Error()+Reset, "\n")
	} else {
		printBoxRow(" "+FgEmerald+"[✓] "+res+Reset, "\n")
	}
	pauseAction()
}

func systemHostnameMenu() {
	for {
		currHostname, _ := os.Hostname()
		prevHost := getPreviousHostname()

		options := []string{
			"Set New System Hostname",
			fmt.Sprintf("Restore Previous Hostname (%s)", prevHost),
			"Return to Main Menu",
		}

		choice := SelectFromOptions(
			"SYSTEM HOSTNAME MANAGER",
			fmt.Sprintf("Current Hostname: %s%s%s", FgEmerald+Bold, currHostname, Reset),
			options,
		)

		switch choice {
		case 0:
			setCustomHostnameFlow()
		case 1:
			restorePreviousHostnameFlow(prevHost)
		default:
			return
		}
	}
}

func setCustomHostnameFlow() {
	showHeader("SET SYSTEM HOSTNAME")
	newName := readInput("Enter Desired Hostname (e.g. clock): ")

	if err := validateHostname(newName); err != nil {
		showHeader("VALIDATION ERROR")
		printBoxRow(" "+FgRose+"[!] "+err.Error()+Reset, "\n")
		pauseAction()
		return
	}

	showHeader("CONFIRM HOSTNAME UPDATE")
	printBoxRow(" "+FgMuted+"Target Hostname: "+FgEmerald+Bold+newName+Reset, "\n")
	printBoxBlankRow("\n")
	printBoxRow(" "+FgAmber+"[i] Applies permanently and captures previous name for rollback."+Reset, "\n")

	if !confirmAction("Apply new system hostname?") {
		showHeader("ACTION CANCELLED")
		printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	showHeader("APPLYING HOSTNAME")
	res, err := setSystemHostname(newName)
	if err != nil {
		printBoxRow(" "+FgRose+"[!] Error: "+err.Error()+Reset, "\n")
	} else {
		printBoxRow(" "+FgEmerald+"[✓] "+res+Reset, "\n")
	}
	pauseAction()
}

func restorePreviousHostnameFlow(prevHost string) {
	showHeader("RESTORE PREVIOUS HOSTNAME")
	printBoxRow(" "+FgMuted+"Previous Hostname: "+FgEmerald+Bold+prevHost+Reset, "\n")

	if !confirmAction("Restore system to previous hostname?") {
		showHeader("ACTION CANCELLED")
		printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	showHeader("RESTORING HOSTNAME")
	res, err := setSystemHostname(prevHost)
	if err != nil {
		printBoxRow(" "+FgRose+"[!] Error: "+err.Error()+Reset, "\n")
	} else {
		printBoxRow(" "+FgEmerald+"[✓] "+res+Reset, "\n")
	}
	pauseAction()
}

func bootModeMenu() {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		showHeader("SYSTEM MODE SWITCHER")
		printBoxRow(" "+FgAmber+"[!] System Mode Switching is supported on Linux and Windows only."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	systemModeIntroduction()
	showBootModeMenu()
}

func systemModeIntroduction() {
	showHeader("SYSTEM MODE SWITCHER — GUIDE")

	printBoxRow(" "+FgIndigo+Bold+"── OPERATIONAL MODES "+BorderColor+"───────────────────────────────"+Reset, "\n")
	if runtime.GOOS == "windows" {
		printBoxRow("  "+FgEmerald+Bold+"[1] GUI Desktop Mode (Windows Explorer)"+Reset, "\n")
		printBoxRow("      Standard desktop shell with Taskbar, Start Menu, and window compositor.", "\n")
		printBoxBlankRow("\n")
		printBoxRow("  "+FgAmber+Bold+"[2] CLI Terminal Mode (PowerShell Shell)"+Reset, "\n")
		printBoxRow("      Headless shell mode. Stops explorer.exe to reduce RAM and CPU overhead.", "\n")
		printBoxBlankRow("\n")
		printBoxRow(" "+FgRose+Bold+"── ADVISORY "+BorderColor+"──────────────────────────────────────────"+Reset, "\n")
		printBoxRow("  "+FgRose+Bold+"[!] Switching immediately stops Windows Explorer (desktop/taskbar)."+Reset, "\n")
	} else {
		printBoxRow("  "+FgEmerald+Bold+"[1] GUI Mode (Graphical Desktop)"+Reset, "\n")
		printBoxRow("      Full window manager, display manager, and graphical session.", "\n")
		printBoxBlankRow("\n")
		printBoxRow("  "+FgAmber+Bold+"[2] TTY / CLI Mode (Text Terminal)"+Reset, "\n")
		printBoxRow("      Lightweight headless terminal mode. Reduces RAM and CPU footprint.", "\n")
		printBoxBlankRow("\n")
		printBoxRow(" "+FgRose+Bold+"── ADVISORY "+BorderColor+"──────────────────────────────────────────"+Reset, "\n")
		printBoxRow("  "+FgRose+Bold+"[!] Switching immediately stops current X11/Wayland desktop."+Reset, "\n")
	}

	pauseAction()
}

func ipChangerMenu() {
	for {
		interfaces := getNetworkInterfaces()
		if len(interfaces) == 0 {
			showHeader("IP ADDRESS CHANGER")
			printBoxRow(" "+FgRose+"[!] No valid network interfaces detected on host."+Reset, "\n")
			pauseAction()
			return
		}

		interfaceChoice := SelectFromOptions(
			"IP ADDRESS CHANGER",
			"Select Network Interface to Configure or Inspect:",
			append(interfaces, "🔍 View All Active IP Addresses (All Interfaces)", "Return to Main Menu"),
		)

		if interfaceChoice < 0 || interfaceChoice == len(interfaces)+1 {
			return
		}

		if interfaceChoice == len(interfaces) {
			viewAllSystemIPsFlow()
			continue
		}

		selectedInterface := interfaces[interfaceChoice]
		activeAddrs := getInterfaceIPAddresses(selectedInterface)
		primaryIP := "No IP Bound"
		if len(activeAddrs) > 0 {
			primaryIP = activeAddrs[0]
		}

		for {
			headerModule := fmt.Sprintf("IP CHANGER │ ADDR: %s", primaryIP)

			actionChoice := SelectFromOptions(
				headerModule,
				fmt.Sprintf("Interface: %s%s%s (Primary IP: %s%s%s)\nSelect Action:", FgCyan, selectedInterface, Reset, FgEmerald+Bold, primaryIP, Reset),
				[]string{
					"Set Permanent Static IPv4 Address",
					"Set Temporary IPv4 Address",
					"Add Secondary IPv4 Address (Alias)",
					"Delete Secondary IPv4 Address (Alias)",
					"Return to Interface Selection",
				},
			)

			if actionChoice < 0 || actionChoice == 4 {
				break
			}

			switch actionChoice {
			case 0:
				setPermanentIPFlow(selectedInterface)
			case 1:
				setTemporaryIPFlow(selectedInterface)
			case 2:
				addIPAliasFlow(selectedInterface)
			case 3:
				deleteIPAliasFlow(selectedInterface)
			}

			activeAddrs = getInterfaceIPAddresses(selectedInterface)
			if len(activeAddrs) > 0 {
				primaryIP = activeAddrs[0]
			} else {
				primaryIP = "No IP Bound"
			}
		}
	}
}

func viewAllSystemIPsFlow() {
	showHeader("ACTIVE SYSTEM IP ADDRESSES")
	printBoxRow(" "+FgAmber+Bold+"INTERFACE    │ TYPE │ ADDRESS / NETMASK"+Reset, "\n")
	printBoxSeparator("\n")

	ips := getAllSystemIPs()
	for _, line := range ips {
		printBoxRow(" "+FgBright+line+Reset, "\n")
	}

	pauseAction()
}

func deleteIPAliasFlow(iface string) {
	activeIPs := getInterfaceIPAddresses(iface)

	if len(activeIPs) == 0 {
		showHeader("DELETE SECONDARY IP ALIAS")
		printBoxRow(" "+FgRose+"[!] No active IPv4 addresses found on "+iface+"."+Reset, "\n")
		pauseAction()
		return
	}

	if len(activeIPs) == 1 {
		showHeader("DELETE SECONDARY IP ALIAS")
		printBoxRow(" "+FgAmber+"[i] Interface "+iface+" currently has only 1 primary address ("+activeIPs[0]+")."+Reset, "\n")
		printBoxRow("     Deleting the primary IP may disconnect your network entirely.", "\n")
		printBoxBlankRow("\n")
		if !confirmAction("Proceed with deleting this IP address anyway?") {
			showHeader("ACTION CANCELLED")
			printBoxRow(" "+FgAmber+"=> Operation cancelled."+Reset, "\n")
			pauseAction()
			return
		}
	}

	options := append(activeIPs, "Cancel and Return")
	choice := SelectFromOptions(
		"DELETE SECONDARY IP ALIAS",
		fmt.Sprintf("Select IP Address to Remove from %s%s%s:", FgCyan, iface, Reset),
		options,
	)

	if choice < 0 || choice >= len(activeIPs) {
		return
	}

	selectedIP := activeIPs[choice]

	showHeader("CONFIRM IP REMOVAL")
	printBoxRow(" "+FgMuted+"Interface:     "+FgBright+Bold+iface+Reset, "\n")
	printBoxRow(" "+FgMuted+"IP to Delete:  "+FgRose+Bold+selectedIP+Reset, "\n")
	printBoxBlankRow("\n")
	printBoxRow(" "+FgAmber+"[!] The selected IP address will be unbound immediately."+Reset, "\n")

	if !confirmAction(fmt.Sprintf("Remove %s from %s?", selectedIP, iface)) {
		showHeader("ACTION CANCELLED")
		printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	showHeader("REMOVING IP ALIAS")
	res := deleteIPAlias(iface, selectedIP)
	printBoxRow(" "+FgEmerald+res+Reset, "\n")
	pauseAction()
}

func setPermanentIPFlow(iface string) {
	showHeader("PERMANENT STATIC IP")

	ip := readInput("IPv4 Address (e.g. 192.168.1.50): ")
	mask := readInput("Subnet Mask / CIDR (e.g. 255.255.255.0 or /24): ")
	gateway := readInput("Gateway IP (optional, press Enter to omit): ")

	cidr, err := normalizeAddress(ip, mask)
	if err != nil {
		showHeader("VALIDATION ERROR")
		printBoxRow(" "+FgRose+"[!] "+err.Error()+Reset, "\n")
		pauseAction()
		return
	}

	if err := validateGateway(gateway); err != nil {
		showHeader("VALIDATION ERROR")
		printBoxRow(" "+FgRose+"[!] "+err.Error()+Reset, "\n")
		pauseAction()
		return
	}

	showHeader("CONFIRM STATIC IP CONFIGURATION")
	printBoxRow(" "+FgMuted+"Interface: "+FgBright+Bold+iface+Reset, "\n")
	printBoxRow(" "+FgMuted+"Static IP: "+FgEmerald+Bold+cidr+Reset, "\n")
	printBoxRow(" "+FgMuted+"Gateway:   "+FgEmerald+Bold+valueOrNone(gateway)+Reset, "\n")
	printBoxBlankRow("\n")
	printBoxRow(" "+FgAmber+"[!] May cause momentary network interruption or reset SSH."+Reset, "\n")

	if !confirmAction("Apply permanent IP configuration?") {
		showHeader("ACTION CANCELLED")
		printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	showHeader("APPLYING CONFIGURATION")
	printBoxRow(" "+FgCyan+"=> Executing network configuration change..."+Reset, "\n")
	res := setPermanentIP(iface, cidr, gateway)
	printBoxRow(" "+FgEmerald+res+Reset, "\n")
	pauseAction()
}

func setTemporaryIPFlow(iface string) {
	showHeader("SET TEMPORARY IP")

	cidr := readInput("IPv4 with CIDR (e.g. 192.168.1.50/24): ")
	cidr, err := validateCIDR(cidr)
	if err != nil {
		showHeader("VALIDATION ERROR")
		printBoxRow(" "+FgRose+"[!] "+err.Error()+Reset, "\n")
		pauseAction()
		return
	}

	showHeader("CONFIRM TEMPORARY IP")
	printBoxRow(" "+FgMuted+"Interface: "+FgBright+Bold+iface+Reset, "\n")
	printBoxRow(" "+FgMuted+"Address:   "+FgEmerald+Bold+cidr+Reset, "\n")
	printBoxBlankRow("\n")
	printBoxRow(" "+FgAmber+"[!] Will revert automatically upon system restart."+Reset, "\n")

	if !confirmAction("Apply temporary IPv4 address?") {
		showHeader("ACTION CANCELLED")
		printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	showHeader("APPLYING TEMPORARY IP")
	res := setTemporaryIP(iface, cidr)
	printBoxRow(" "+FgEmerald+res+Reset, "\n")
	pauseAction()
}

func addIPAliasFlow(iface string) {
	showHeader("ADD SECONDARY IP ALIAS")

	cidr := readInput("Secondary IPv4 with CIDR (e.g. 192.168.1.51/24): ")
	cidr, err := validateCIDR(cidr)
	if err != nil {
		showHeader("VALIDATION ERROR")
		printBoxRow(" "+FgRose+"[!] "+err.Error()+Reset, "\n")
		pauseAction()
		return
	}

	showHeader("CONFIRM SECONDARY IP ALIAS")
	printBoxRow(" "+FgMuted+"Interface: "+FgBright+Bold+iface+Reset, "\n")
	printBoxRow(" "+FgMuted+"Alias IP:  "+FgEmerald+Bold+cidr+Reset, "\n")

	if !confirmAction("Bind secondary IPv4 alias?") {
		showHeader("ACTION CANCELLED")
		printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	showHeader("BINDING ALIAS")
	res := addIPAlias(iface, cidr)
	printBoxRow(" "+FgEmerald+res+Reset, "\n")
	pauseAction()
}

func dnsSwitcherMenu() {
	for {
		interfaces := getNetworkInterfaces()
		if len(interfaces) == 0 {
			showHeader("DNS SWITCHER")
			printBoxRow(" "+FgRose+"[!] No network interfaces found."+Reset, "\n")
			pauseAction()
			return
		}

		interfaceChoice := SelectFromOptions(
			"DNS SWITCHER",
			"Select Target Interface to Configure or Diagnose:",
			append(interfaces, "Return to Main Menu"),
		)

		if interfaceChoice < 0 || interfaceChoice == len(interfaces) {
			return
		}

		iface := interfaces[interfaceChoice]

		for {
			activeDNS := getCurrentDNS(iface)

			dnsChoice := SelectFromOptions(
				fmt.Sprintf("DNS SWITCHER │ CURRENT: %s", activeDNS),
				fmt.Sprintf("Interface: %s%s%s (Active Resolvers: %s%s%s)\nSelect DNS Profile or Diagnostic:", FgCyan, iface, Reset, FgEmerald+Bold, activeDNS, Reset),
				[]string{
					"Cloudflare DNS (1.1.1.1, 1.0.0.1)",
					"Google Public DNS (8.8.8.8, 8.8.4.4)",
					"Quad9 Security DNS (9.9.9.9, 149.112.112.112)",
					"Custom DNS Servers",
					"Restore Automatic DHCP DNS (Default Router Gateway)",
					"⚡ Run Live Ping & Resolution Diagnostic on Active DNS",
					"Return to Interface Selection",
				},
			)

			if dnsChoice < 0 || dnsChoice == 6 {
				break
			}

			var servers []string
			restoreAutomaticDNS := false

			switch dnsChoice {
			case 0:
				servers = []string{"1.1.1.1", "1.0.0.1"}
			case 1:
				servers = []string{"8.8.8.8", "8.8.4.4"}
			case 2:
				servers = []string{"9.9.9.9", "149.112.112.112"}
			case 3:
				showHeader("CUSTOM DNS CONFIGURATION")
				input := readInput("DNS IPs comma-separated (e.g. 1.1.1.1, 8.8.8.8): ")
				servers = splitDNSInput(input)
				if err := validateDNSServers(servers); err != nil {
					showHeader("VALIDATION ERROR")
					printBoxRow(" "+FgRose+"[!] "+err.Error()+Reset, "\n")
					pauseAction()
					continue
				}
			case 4:
				restoreAutomaticDNS = true
			case 5:
				pingDNSFlow(activeDNS)
				continue
			}

			showHeader("CONFIRM DNS PROFILE CHANGE")
			printBoxRow(" "+FgMuted+"Interface: "+FgBright+Bold+iface+Reset, "\n")
			if restoreAutomaticDNS {
				printBoxRow(" "+FgMuted+"DNS Mode:  "+FgEmerald+Bold+"Automatic DHCP Mode (Default Router)"+Reset, "\n")
			} else {
				printBoxRow(" "+FgMuted+"DNS List:  "+FgEmerald+Bold+strings.Join(servers, ", ")+Reset, "\n")
			}

			if !confirmAction("Apply DNS changes to host?") {
				showHeader("ACTION CANCELLED")
				printBoxRow(" "+FgAmber+"=> Operation cancelled."+Reset, "\n")
				pauseAction()
				continue
			}

			if !requirePrivilege() {
				continue
			}

			showHeader("UPDATING DNS CONFIGURATION")
			res := setDNS(iface, servers, restoreAutomaticDNS)
			printBoxRow(" "+FgEmerald+res+Reset, "\n")
			pauseAction()
		}
	}
}

func pingDNSFlow(activeDNS string) {
	showHeader("DNS RESOLVER DIAGNOSTIC")

	cleaned := strings.ReplaceAll(activeDNS, "(Router DHCP)", "")
	targets := splitDNSInput(cleaned)
	if len(targets) == 0 {
		targets = []string{"1.1.1.1", "8.8.8.8"}
	}

	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}

		printBoxRow(fmt.Sprintf(" %s[1] ICMP Ping Test: %s%s%s", FgCyan+Bold, FgBright, target, Reset), "\n")
		output := pingHost(target)
		lines := strings.Split(output, "\n")
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" {
				printBoxRow("     "+FgEmerald+l+Reset, "\n")
			}
		}

		printBoxBlankRow("\n")
		printBoxRow(fmt.Sprintf(" %s[2] Layer 7 DNS Query Test: %s (Port 53)%s", FgCyan+Bold, target, Reset), "\n")
		dnsResult := testDNSResolution(target, "google.com")
		printBoxRow("     "+FgEmerald+dnsResult+Reset, "\n")
		printBoxBlankRow("\n")
	}

	pauseAction()
}

func proxySwitcherMenu() {
	for {
		activeProxy := getCurrentProxy()

		choice := SelectFromOptions(
			fmt.Sprintf("PROXY CONFIGURATION │ ACTIVE: %s", activeProxy),
			fmt.Sprintf("Current Proxy Endpoint: %s%s%s\nSelect Operation:", FgEmerald+Bold, activeProxy, Reset),
			[]string{
				"📖 View Proxy Usage & Operator Guidelines (Beginner to Pro)",
				"Set System Proxy Endpoint (e.g. 127.0.0.1:8080)",
				"⚡ Test Live Socket Connectivity on Current Proxy",
				"Disable System Proxy (Revert to DIRECT)",
				"Return to Main Menu",
			},
		)

		if choice < 0 || choice == 4 {
			return
		}

		switch choice {
		case 0:
			showProxyGuidelinesFlow()
		case 1:
			setProxyFlow()
		case 2:
			testCurrentProxyFlow(activeProxy)
		case 3:
			disableProxyFlow()
		}
	}
}

func showProxyGuidelinesFlow() {
	showHeader("GOLD-STANDARD OPERATOR GUIDE: PROXY ENGINE")

	printBoxRow(" "+FgEmerald+Bold+"── 1. WHAT DOES THE PROXY ENGINE DO? "+BorderColor+"─────────────────────────"+Reset, "\n")
	printBoxRow("  "+FgBright+"• Intercepts & routes OS-level HTTP, HTTPS, and TCP network traffic."+Reset, "\n")

	switch runtime.GOOS {
	case "linux":
		printBoxRow("  "+FgCyan+"• Linux Tri-Layer Architecture:"+Reset, "\n")
		printBoxRow("    a) System Shells: Exports variables in /etc/environment.", "\n")
		printBoxRow("    b) Profile Scripts: Injects /etc/profile.d/switcher_proxy.sh.", "\n")
		printBoxRow("    c) Desktop Layer: Configures GNOME/XFCE via gsettings.", "\n")
	case "darwin":
		printBoxRow("  "+FgCyan+"• macOS Darwin Subsystem Architecture:"+Reset, "\n")
		printBoxRow("    a) NetworkSetup: Configures Web & Secure Web proxies on active ports.", "\n")
		printBoxRow("    b) CLI Environment: Exports HTTP_PROXY and HTTPS_PROXY to current shell.", "\n")
		printBoxRow("    c) CoreOS Sockets: Directs Safari, Chrome, and system daemons.", "\n")
	case "windows":
		printBoxRow("  "+FgCyan+"• Windows Dual-Layer Architecture:"+Reset, "\n")
		printBoxRow("    a) WinHTTP Subsystem: Machine routing for CLI, PowerShell, and services.", "\n")
		printBoxRow("    b) WinINET Layer: Registry proxy for Chrome, Edge, and desktop apps.", "\n")
	}
	printBoxBlankRow("\n")

	printBoxRow(" "+FgCyan+Bold+"── 2. COMMON USE CASES & SECURITY SUITES "+BorderColor+"──────────────────────"+Reset, "\n")
	printBoxRow("  "+FgBright+"• Web App Pentesting: Intercept traffic via Burp Suite or OWASP ZAP."+Reset, "\n")
	printBoxRow("  "+FgBright+"• Enterprise Routing: Route via Squid, Privoxy, or Corporate Gateways."+Reset, "\n")
	printBoxRow("  "+FgBright+"• API & CLI Debugging: Inspect curl, wget, and script traffic."+Reset, "\n")
	printBoxBlankRow("\n")

	printBoxRow(" "+FgAmber+Bold+"── 3. STEP-BY-STEP OPERATOR WORKFLOW "+BorderColor+"──────────────────────────"+Reset, "\n")
	printBoxRow("  "+FgEmerald+"Step 1:"+Reset+" Start your proxy listener (e.g. Burp Suite on 127.0.0.1:8080).", "\n")
	printBoxRow("  "+FgEmerald+"Step 2:"+Reset+" Select 'Set System Proxy Endpoint' and input '127.0.0.1:8080'.", "\n")
	printBoxRow("  "+FgEmerald+"Step 3:"+Reset+" Run 'Test Live Socket Connectivity' to verify listener status.", "\n")

	switch runtime.GOOS {
	case "linux":
		printBoxRow("  "+FgEmerald+"Step 4:"+Reset+" Test in terminal: 'curl -k http://httpbin.org/ip' or open browser.", "\n")
	case "darwin":
		printBoxRow("  "+FgEmerald+"Step 4:"+Reset+" Test in terminal: 'curl -k http://httpbin.org/ip' or open Safari.", "\n")
	case "windows":
		printBoxRow("  "+FgEmerald+"Step 4:"+Reset+" Test in PowerShell: 'curl.exe http://httpbin.org/ip' or open Edge.", "\n")
	}

	printBoxRow("  "+FgEmerald+"Step 5:"+Reset+" When done, choose 'Disable System Proxy (Revert to DIRECT)'", "\n")
	printBoxRow("          to restore standard unproxied internet access.", "\n")
	printBoxBlankRow("\n")

	printBoxRow(" "+FgRose+Bold+"── 4. CRITICAL OPERATOR WARNING "+BorderColor+"───────────────────────────────"+Reset, "\n")
	printBoxRow("  "+FgRose+"[!] If your proxy software is STOPPED while this proxy is ACTIVE,"+Reset, "\n")
	printBoxRow("      all web browsers and network tools will fail to load websites.", "\n")
	printBoxRow("      Always select 'Disable System Proxy' once testing concludes.", "\n")
	printBoxBlankRow("\n")

	pauseAction()
}

func testCurrentProxyFlow(activeProxy string) {
	showHeader("TEST CURRENT PROXY CONNECTIVITY")

	if activeProxy == "" || strings.Contains(activeProxy, "DIRECT") {
		printBoxRow(" "+FgAmber+"[i] No active proxy configured. System is in DIRECT connection mode."+Reset, "\n")
		pauseAction()
		return
	}

	host, port, err := parseAndValidateProxy(activeProxy)
	if err != nil {
		printBoxRow(" "+FgRose+"[!] Invalid active proxy format: "+err.Error()+Reset, "\n")
		pauseAction()
		return
	}

	printBoxRow(" "+FgCyan+"[i] Probing active proxy endpoint "+FgBright+Bold+host+":"+port+Reset+" ...", "\n")
	printBoxBlankRow("\n")

	isAlive, details := probeProxyEndpoint(host, port)
	if isAlive {
		printBoxRow(" "+FgEmerald+Bold+"[✓] PROXY ONLINE: "+Reset+FgBright+details+Reset, "\n")
		printBoxRow("     Browser and system HTTP/HTTPS traffic will route smoothly.", "\n")
	} else {
		printBoxRow(" "+FgRose+Bold+"[✗] PROXY OFFLINE: "+Reset+FgRose+details+Reset, "\n")
		printBoxRow("     Traffic routed through this endpoint will result in connection errors.", "\n")
	}

	pauseAction()
}

func setProxyFlow() {
	showHeader("SET PROXY CONFIGURATION")
	printBoxRow(" "+FgAmber+"[i] Supported Examples:"+Reset+" 127.0.0.1:8080 | 127.0.0.1:8888 | 192.168.1.50:3128", "\n")
	printBoxBlankRow("\n")

	rawEndpoint := readInput("Enter Proxy (host:port): ")
	host, port, err := parseAndValidateProxy(rawEndpoint)
	if err != nil {
		showHeader("VALIDATION ERROR")
		printBoxRow(" "+FgRose+"[!] "+err.Error()+Reset, "\n")
		pauseAction()
		return
	}

	showHeader("PROBING PROXY SOCKET")
	printBoxRow(" "+FgCyan+"[i] Probing TCP socket "+FgBright+Bold+host+":"+port+Reset+" ...", "\n")
	printBoxBlankRow("\n")

	isAlive, probeDetails := probeProxyEndpoint(host, port)
	if isAlive {
		printBoxRow(" "+FgEmerald+Bold+"[✓] "+probeDetails+Reset, "\n")
		printBoxRow("     Verified: The proxy daemon is actively listening.", "\n")
	} else {
		printBoxRow(" "+FgRose+Bold+"[!] WARNING: "+probeDetails+Reset, "\n")
		printBoxRow("     If you enable this proxy now, web browsers and curl will fail", "\n")
		printBoxRow("     until you start your proxy software (e.g. Burp Suite, Squid, mitmproxy).", "\n")
		printBoxBlankRow("\n")

		if !confirmAction("Proceed with enabling this unverified proxy anyway?") {
			showHeader("ACTION CANCELLED")
			printBoxRow(" "+FgAmber+"=> Operation cancelled to protect network routing."+Reset, "\n")
			pauseAction()
			return
		}
	}

	showHeader("CONFIRM PROXY CONFIGURATION")
	printBoxRow(" "+FgMuted+"Proxy Host:     "+FgBright+Bold+host+Reset, "\n")
	printBoxRow(" "+FgMuted+"Proxy Port:     "+FgBright+Bold+port+Reset, "\n")
	printBoxRow(" "+FgMuted+"Full Endpoint:  "+FgEmerald+Bold+host+":"+port+Reset, "\n")

	if !confirmAction("Apply system-wide proxy settings?") {
		showHeader("ACTION CANCELLED")
		printBoxRow(" "+FgAmber+"=> Operation cancelled."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	showHeader("APPLYING PROXY")
	res := setSystemProxy(host, port)
	lines := strings.Split(res, "\n")
	for _, l := range lines {
		printBoxRow(" "+l, "\n")
	}
	pauseAction()
}
func disableProxyFlow() {
	showHeader("DISABLE PROXY")
	printBoxRow(" "+FgAmber+"[!] Disables all system-wide proxy variables and settings."+Reset, "\n")

	if !confirmAction("Disable system proxy?") {
		showHeader("ACTION CANCELLED")
		printBoxRow(" "+FgAmber+"=> Operation cancelled."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	showHeader("DISABLING PROXY")
	res := disableSystemProxy()
	printBoxRow(" "+FgEmerald+"[✓] "+res+Reset, "\n")
	pauseAction()
}
func restartNetworkServiceFlow() {
	showHeader("RESTART NETWORK SERVICE")
	printBoxRow(" "+FgAmber+"[i] This will restart the host network daemon and re-initialize adapters."+Reset, "\n")
	printBoxBlankRow("\n")

	if !confirmAction("Force restart network service now?") {
		showHeader("ACTION CANCELLED")
		printBoxRow(" "+FgAmber+"=> Operation cancelled by user."+Reset, "\n")
		pauseAction()
		return
	}

	if !requirePrivilege() {
		return
	}

	showHeader("RESTARTING NETWORK SERVICE")
	res := restartNetworkManagerService()
	printBoxRow(" "+FgEmerald+res+Reset, "\n")
	pauseAction()
}

func networkTroubleshooterFlow() {
	showHeader("NETWORK AUDIT & SELF-HEALING ENGINE")
	printBoxRow(" "+FgCyan+"[i] Scanning network stack, routing tables, DNS, and host files..."+Reset, "\n")
	printBoxBlankRow("\n")

	if !requirePrivilege() {
		return
	}

	steps := runNetworkTroubleshooter()

	allPassed := true
	for i, s := range steps {
		stepTitle := fmt.Sprintf("[%d/5] %s", i+1, s.Name)
		if s.Passed {
			printBoxRow(" "+FgEmerald+Bold+"[✓] "+stepTitle+Reset, "\n")
			printBoxRow("     "+FgBright+s.Details+Reset, "\n")
		} else {
			allPassed = false
			printBoxRow(" "+FgRose+Bold+"[✗] "+stepTitle+Reset, "\n")
			printBoxRow("     "+FgRose+"Issue: "+Reset+FgBright+s.Details+Reset, "\n")
			if s.FixLog != "" {
				printBoxRow("     "+FgEmerald+Bold+"Applied Fix: "+Reset+FgEmerald+s.FixLog+Reset, "\n")
			}
		}
		printBoxBlankRow("\n")
	}

	printBoxSeparator("\n")
	if allPassed {
		printBoxRow(" "+FgEmerald+Bold+"[✓] HEALTH AUDIT PASSED: All network layers operating normally."+Reset, "\n")
	} else {
		printBoxRow(" "+FgAmber+Bold+"[i] HEALING COMPLETE: Detected failures were automatically repaired."+Reset, "\n")
	}

	pauseAction()
}

func valueOrNone(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "(none)"
	}
	return value
}

func main() {
	if runtime.GOOS == "windows" {
		ensureWindowsConsole()
	}
	autoElevateIfUnprivileged()

	// Direct OS-Agnostic Boot CLI-Shell Router
	if len(os.Args) > 1 && os.Args[1] == "--cli-shell" {
		handleDirectBootCliShell()
	}

	initAlternateScreen()
	defer restoreNormalScreen()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		restoreNormalScreen()
		os.Exit(0)
	}()

	for {
		choice := selectMainMenu()
		switch choice {
		case 0:
			bootModeMenu()
		case 1:
			systemHostnameMenu()
		case 2:
			ipChangerMenu()
		case 3:
			dnsSwitcherMenu()
		case 4:
			proxySwitcherMenu()
		case 5:
			restartNetworkServiceFlow()
		case 6:
			networkTroubleshooterFlow()
		case 7:
			globalCliMenu()
		default:
			restoreNormalScreen()
			os.Exit(0)
		}
	}
}
