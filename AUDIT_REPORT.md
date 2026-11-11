# Switcher Suite Wizard — PHASE 1 Audit Report

Scope: `main.go`, `system_common.go`, `system_linux.go`, `system_darwin.go`, `system_windows.go`,
`windows_console.go`, `windows_console_stub.go`, `go.mod`/`go.sum`, `autorun.sh`, `autorun.ps1`,
`autorun.bat`, `rescue-network.sh`, `banner_wrapper.txt`, committed binaries, repo hygiene.
This is an audit only — no fixes have been applied.

---

## Executive Summary

The codebase is small, single-binary, and generally well-structured (clean `gofmt`/`go vet`,
discrete-argument `exec.Command` calls almost everywhere, solid input validation for
hostnames/CIDRs/gateways/DNS IPs). It cross-compiles cleanly for `linux`, `windows`, and `darwin`
(amd64) from this exact source tree.

The reported **Kali GUI → TTY "hang"** is explained by two concrete, independently confirmed bugs,
both in the Linux "switch mode immediately" path:

1. The program calls `os.Exit(0)` right after launching the mode-switch script, **without ever
   calling `restoreNormalScreen()`** — so the terminal is left parked in the ANSI alternate-screen
   buffer with the cursor hidden. To the user this looks exactly like a frozen/blank terminal.
2. The actual switch (`systemctl stop lightdm/gdm3/sddm`, `sleep`, `chvt`) runs in a **background
   subshell that is never detached from the current session** (no `setsid`/`disown`/`systemd-run
   --scope`). Stopping the display manager tears down the cgroup/session the subshell lives in,
   so the subshell can be killed before `chvt` ever runs — GUI stops, but the VT switch that was
   supposed to show the TTY1 login prompt never happens, leaving a blank screen that looks hung.

Beyond this, the audit found a real (if low-exploitability, since the attacker is already the
elevated local operator) **command/script-injection gap** in proxy-host validation that lets shell
metacharacters reach a world-sourced `/etc/profile.d/switcher_proxy.sh` on Linux and a PowerShell
`-Command` string on Windows, a destructive (non-merging) `/etc/hosts` rewrite on Linux, and several
hygiene issues (committed binaries/personal state files, missing `LICENSE`).

No build, vet, or format issues were found. No test files exist in the repo.

---

## Root Cause of the Kali GUI → TTY Hang

**Primary cause — alternate screen/cursor state never restored before exit**
(`system_linux.go:448-470`, `system_linux.go:472-498`; cf. `main.go:56-62`, `main.go:1635-1636`)

`main()` enters the ANSI alternate screen buffer and hides the cursor on startup:

```go
// main.go:56-62
func initAlternateScreen() { fmt.Print("\033[?1049h\033[2J\033[H\033[?25l") }
func restoreNormalScreen()  { fmt.Print("\033[?25h\033[?1049l") }
...
initAlternateScreen()
defer restoreNormalScreen()   // main.go:1636 — only runs on a normal return from main()
```

`defer` only fires on a normal function return — **never** on `os.Exit()`. `handleImmediateSwitch()`
in `system_linux.go` calls `os.Exit(0)` directly in both branches, right after starting the
background script:

```go
// system_linux.go:468-470  (switch to GUI)
cmd := exec.Command("bash", "-c", script)
_ = cmd.Start()
os.Exit(0)
...
// system_linux.go:496-498  (switch to TTY/CLI)
cmd := exec.Command("bash", "-c", script)
_ = cmd.Start()
os.Exit(0)
```

Neither call site invokes `restoreNormalScreen()` first. The process terminates with the terminal
still in `\033[?1049h` (alternate buffer) and `\033[?25l` (hidden cursor). Whatever the user sees
next — the returned shell prompt, or the TTY1 login screen after `chvt` — is rendered while the
terminal emulator/VT is still logically parked in that altscreen state on at least one of the
virtual consoles, producing the "stuck/blank screen" symptom. (`main.go`'s own `default:` menu
case and the `SIGINT`/`SIGTERM` handler goroutine both correctly call `restoreNormalScreen()`
before `os.Exit(0)` — only the two mode-switch call sites were missed.)

**Compounding cause — the switch script is not detached from the session it is tearing down**
(`system_linux.go:460-470`, `system_linux.go:485-498`)

```go
script := `
    (
        systemctl start lightdm.service >/dev/null 2>&1 || systemctl start gdm3.service ... || systemctl isolate graphical.target >/dev/null 2>&1
        sleep 1
        chvt 7 || chvt 2
    ) >/dev/null 2>&1 &
`
cmd := exec.Command("bash", "-c", script)
_ = cmd.Start()
```

`cmd.Stdin/Stdout/Stderr` are left `nil`, so Go connects them to `/dev/null` (not an inherited TTY —
this part is *not* the bug). The real gap is that `exec.Command` is launched with no
`SysProcAttr{Setsid: true}` and the inner `(...) &` only backgrounds the subshell within bash's own
job control — it does **not** move it to a new session or escape the cgroup of the process that
launched it. If the wizard is being run from a terminal inside the graphical session (the normal
case for a user testing "switch to TTY" from a desktop terminal), stopping `lightdm.service` tears
down that service's session/cgroup. Depending on how the session was constructed, the
`sleep 1; chvt 1` tail of the very subshell doing the stopping can be killed along with it — so the
display manager goes down but the VT is never switched and no TTY1 getty is shown to look at. This
is consistent with an intermittent "sometimes it hangs, sometimes it doesn't" report, since it
depends on exactly how the current session's process tree is rooted.

Both causes are specific to the *immediate* switch path; the "set startup target for next boot"
path (`handleNextBootSwitch`, `systemctl set-default` + optional `systemctl reboot`) does not
exhibit either bug.

---

## Findings Table

| ID | Severity | File:Line | Problem | Proposed Fix |
|----|----------|-----------|---------|---------------|
| C1 | Critical | `system_linux.go:470`, `system_linux.go:498` | `os.Exit(0)` called right after dispatching the mode-switch script, without calling `restoreNormalScreen()` first — terminal left in alt-screen buffer with hidden cursor. | Call `restoreNormalScreen()` (and flush) immediately before each `os.Exit(0)` in `handleImmediateSwitch`, or route through a single exit helper that always restores terminal state first. |
| C2 | Critical | `system_linux.go:460-470`, `system_linux.go:485-498` | Background switch script is not detached (`setsid`/`disown`/`systemd-run --scope`) from the session it stops (`lightdm`/`gdm3`/`sddm`); it can be killed mid-sequence before `chvt` runs, leaving the system stopped-but-not-switched. | Launch the tail of the script (`sleep; chvt`) via `systemd-run --scope --collect --quiet` (or `setsid nohup ... &`) so it survives the display-manager stop; or reorder to `chvt` before stopping the DM where safe. |
| H1 | High | `main.go:1638-1644`, `main.go:526-532`, `main.go:575-580` | `SIGINT`/`SIGTERM` handler goroutine calls `os.Exit(0)` directly, bypassing every deferred `term.Restore()` in `selectMainMenu`/`SelectFromOptions` — terminal left in raw/no-echo mode after Ctrl+C. | Have the signal handler set a flag / close a channel that the main loop checks, or explicitly restore the last known terminal state before `os.Exit` in the handler. |
| H2 | High | `system_windows.go:345-359` (sink), `system_common.go:395-426` (validation gap) | `parseAndValidateProxy` rejects whitespace/slashes in the proxy host but not quotes, backticks, `$()`, or `;`; `setSystemProxy` splices the raw host/port into a PowerShell `-Command` double-quoted literal (`Set-ItemProperty ... -Value "%s"`). A crafted host breaks out of the literal and runs arbitrary PowerShell under the already-elevated process. | Restrict proxy host to a strict hostname/IPv4/IPv6 allowlist regex, and/or pass values via `-EncodedCommand`/argument array instead of string interpolation into `-Command`. |
| H3 | High | `system_linux.go:368-375`, `system_common.go:395-426` (same validation gap) | `setSystemProxy` writes the unescaped host:port into `/etc/profile.d/switcher_proxy.sh` (mode 0755), which is **sourced by every login shell**. Shell metacharacters in the host value become code that runs for every user who logs in. | Same strict host validation as H2, plus quote the values when generating the shell script (`printf %q`-equivalent) rather than raw string concatenation. |
| H4 | High | repo root: `switcher`, `Switcher_Suite_Wizard_macOS`, `.switcher_cli_command`, `.switcher_previous_hostname`, `.switcher_default_hostname` | Compiled binaries and the developer's own runtime state files (containing a real former hostname, "lazy", and username, "saleem") are committed to git. | Remove from git history, add to `.gitignore` (already partially covers `Switcher_Suite_Wizard`/`.exe` but misses the macOS binary and the `.switcher_*` state files), ship binaries via GitHub Releases/CI instead. |
| M1 | Medium | `system_linux.go:32-34`, `system_linux.go:675-677` | `setSystemHostname` and the troubleshooter's auto-fix unconditionally overwrite `/etc/hosts` with a fixed 3-line template, destroying any pre-existing custom entries (Docker, local dev domains, etc.) with no backup — unlike the macOS path which merges. | Mirror `syncDarwinHostsFile`'s merge-don't-replace approach on Linux. |
| M2 | Medium | `system_linux.go:306` | `setDNS` writes `/etc/resolv.conf` directly; on systemd-resolved hosts (Kali's default on recent releases) this file is a symlink to a generated file that NetworkManager/systemd-resolved will regenerate, silently reverting the change with no feedback to the user. | Detect systemd-resolved (`/run/systemd/resolve/` present, or `resolvectl status`) and configure DNS via `resolvectl`/nmcli instead of writing the file directly. |
| M3 | Medium | `system_linux.go:21-37, 355-410`; `system_windows.go:98-119, 345-383`; `system_darwin.go` equivalents | Nearly every mutating helper ignores the error return of its own `os.WriteFile`/`exec.Command` calls (`_ = ...`) and unconditionally returns a hard-coded `"[✓] ..."` success string — a failed write/command is reported to the operator as success. | Propagate and surface the first real error instead of discarding it; only report success once the underlying operations actually succeeded. |
| M4 | Medium | `system_linux.go:462`, `system_linux.go:490` | Display-manager detection is a hardcoded `lightdm \|\| gdm3 \|\| sddm \|\| isolate graphical.target` chain with all output discarded (`>/dev/null 2>&1`); on a headless/minimal Kali install with none of these present, the operator gets no feedback that nothing happened. | Detect the active display manager first (`cat /etc/X11/default-display-manager`, check unit existence) and report clearly if none is found, instead of silently no-op'ing. |
| M5 | Medium | repo root (no `LICENSE`); `README.md:629-631` claims MIT | README claims "Distributed under the MIT License. See `LICENSE` for details," but no `LICENSE` file exists in the repo. | Add the MIT `LICENSE` file (also listed on the project's own roadmap). |
| M6 | Medium | `autorun.sh:34,152,155,160,350,360,371` | `shellcheck` SC2059: ANSI color variables used directly as the `printf` format string. Currently harmless (no attacker-controlled `%` content) but fragile if any interpolated value is ever attacker/user-controlled. | Use `printf '%s' "$msg"`-style calls, or `printf "${COLOR}%s${NC}\n" "$text"`. |
| L1 | Low | `system_linux.go:435-504` vs `system_windows.go:408-475` | Linux's immediate-switch flow terminates the whole program (`os.Exit(0)`); the equivalent Windows flow returns to an interactive menu/CLI shell instead of exiting. Inconsistent cross-platform UX despite README's parity claims. | Decide on one behavior (recommend: never hard-exit; return to the menu/CLI shell on both). |
| L2 | Low | `windows_console.go:67-84` | Manual `COORD` packing for `SetConsoleScreenBufferSize`/`SetConsoleWindowInfo` could not be runtime-verified — no Windows host available in this Linux audit environment. | Verify manually on real Windows hardware/VM; add a fallback path if buffer/window calls fail. |
| L3 | Low | repo root: `switcher` | Committed binary name (`switcher`) doesn't match any name the build system actually produces (`Switcher_Suite_Wizard`, `.exe`, `_macOS`) — looks like a stray local `go build -o switcher .`. | Remove (see H4). |

---

## Things Verified as Working Correctly

- `gofmt -l .` — clean, no formatting diffs.
- `go vet ./...` — clean, no reported issues.
- `GOOS=linux/windows/darwin GOARCH=amd64 go build .` — all three succeed cleanly from this exact source tree (build tags in `system_linux.go`/`system_darwin.go`/`system_windows.go`/`windows_console.go`/`windows_console_stub.go` are correctly scoped and non-overlapping).
- No `*_test.go` files exist; `autorun.sh`/`.bat`/`.ps1`'s "skip tests cleanly" branch is the one actually exercised, and it behaves correctly (no failure, clear message).
- `bash -n` passes on both `autorun.sh` and `rescue-network.sh`.
- `shellcheck` is fully clean on `rescue-network.sh` (0 findings).
- `rescue-network.sh` self-elevates correctly (`exec sudo bash "$0" "$@"`), has zero Go/network dependency for the fix itself, and its routing/DNS/hosts recovery logic matches the README's claims.
- Input validation for hostnames (`validateHostname`), CIDR/IP (`validateCIDR`, `normalizeAddress`, `parsePrefix`), gateways (`validateGateway`), and DNS server lists (`validateDNSServers`) is solid and correctly rejects malformed input before anything reaches `exec.Command`.
- Every `exec.Command` call that takes a discrete argument slice (the overwhelming majority — `ip`, `nmcli`, `netsh`, `reg`, `networksetup`, `hostnamectl`, etc.) is immune to shell injection because no shell is invoked for it.
- Committed binaries (`switcher`, `Switcher_Suite_Wizard_macOS`) were built from the exact same commit as the current `.go` sources — `git log` shows only `README.md` changed in the two "fix" commits after "Initial Commit," so the binaries are not stale relative to source, even though they still shouldn't be in git (see H4).
- `autorun.sh`/`.bat`/`.ps1` implement the same pipeline (format → sync deps → vet → test-if-present → build → launch-or-handoff) consistently across all three platforms, including offline/vendor-mode and portable-Go-toolchain bootstrap.
- `rm`/destructive steps in the build scripts are gated behind explicit `-f`/`--hard` flags, not the default path.

---

## Recommended Fix Order

1. **C1** — add `restoreNormalScreen()` before every `os.Exit(0)` outside `main()`'s own defer chain (both branches of `handleImmediateSwitch`, and the reboot branches in `handleNextBootSwitch`/`handleWindowsNextBootSwitch`). This alone very likely resolves the reported "hang" symptom.
2. **C2** — detach the background switch script from the current session (`systemd-run --scope --collect` or `setsid`/`disown`) so it survives the display-manager stop it's performing.
3. **H1** — make the `SIGINT`/`SIGTERM` handler restore terminal state (raw mode + alt screen) before exiting, rather than exiting from the goroutine with none of the normal defers having run.
4. **H2 / H3** — tighten `parseAndValidateProxy` to a strict hostname/IP allowlist and stop raw string-interpolating the value into PowerShell `-Command` strings and shell-sourced profile scripts.
5. **H4 / M5** — purge committed binaries and personal state files from git, fix `.gitignore`, add the missing `LICENSE`, and stand up a release/CI pipeline to produce binaries instead of committing them.
6. **M1 / M2** — merge instead of truncate `/etc/hosts`; detect systemd-resolved before writing `/etc/resolv.conf` directly.
7. **M3** — stop discarding errors from the writes/commands that back every "[✓] success" message.
8. **M4** — detect the installed/active display manager up front and report clearly when none is found.
9. Cosmetic cleanup: **M6** shellcheck quoting, **L1** cross-platform exit-behavior parity, **L3** remove the stray `switcher` binary.
