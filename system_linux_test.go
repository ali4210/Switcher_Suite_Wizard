//go:build linux

package main

import "strings"

import "testing"

// buildGuiSwitchScript / buildTtySwitchScript are pure string builders (no
// filesystem or command execution), so they're safe to exercise directly
// for every dm value including the empty-string fallback case.

func TestBuildGuiSwitchScript(t *testing.T) {
	cases := []struct {
		dm       string
		wantSubs []string
	}{
		{"", []string{"systemctl isolate graphical.target"}},
		{"lightdm.service", []string{"systemctl start lightdm.service"}},
		{"gdm.service", []string{"systemctl start gdm.service"}},
		{"gdm3.service", []string{"systemctl start gdm3.service"}},
		{"sddm.service", []string{"systemctl start sddm.service"}},
	}

	for _, c := range cases {
		script := buildGuiSwitchScript(c.dm)

		for _, want := range c.wantSubs {
			if !strings.Contains(script, want) {
				t.Errorf("buildGuiSwitchScript(%q) missing %q in:\n%s", c.dm, want, script)
			}
		}
		if !strings.Contains(script, "chvt 7") {
			t.Errorf("buildGuiSwitchScript(%q) missing chvt fallback chain", c.dm)
		}
		if !strings.Contains(script, switchLogFile) {
			t.Errorf("buildGuiSwitchScript(%q) does not log to %s", c.dm, switchLogFile)
		}
		if !strings.Contains(script, "switch-to-gui") {
			t.Errorf("buildGuiSwitchScript(%q) missing switch-to-gui marker", c.dm)
		}
		// Must not start whatever display manager it was NOT given.
		if c.dm != "" {
			if !strings.Contains(script, "systemctl start "+c.dm) {
				t.Errorf("buildGuiSwitchScript(%q) should target exactly the detected DM", c.dm)
			}
			if strings.Contains(script, "systemctl isolate graphical.target") {
				t.Errorf("buildGuiSwitchScript(%q) should not fall back to isolate graphical.target when a DM was detected", c.dm)
			}
		}
	}
}

func TestBuildTtySwitchScript(t *testing.T) {
	cases := []struct {
		dm       string
		wantSubs []string
	}{
		{"", []string{"systemctl isolate multi-user.target"}},
		{"lightdm.service", []string{"systemctl stop lightdm.service"}},
		{"gdm.service", []string{"systemctl stop gdm.service"}},
		{"gdm3.service", []string{"systemctl stop gdm3.service"}},
		{"sddm.service", []string{"systemctl stop sddm.service"}},
	}

	for _, c := range cases {
		script := buildTtySwitchScript(c.dm)

		for _, want := range c.wantSubs {
			if !strings.Contains(script, want) {
				t.Errorf("buildTtySwitchScript(%q) missing %q in:\n%s", c.dm, want, script)
			}
		}
		if !strings.Contains(script, "getty@tty1.service") {
			t.Errorf("buildTtySwitchScript(%q) missing getty@tty1 enable/restart", c.dm)
		}
		if !strings.Contains(script, "chvt 1") {
			t.Errorf("buildTtySwitchScript(%q) missing chvt 1 fallback", c.dm)
		}
		if !strings.Contains(script, switchLogFile) {
			t.Errorf("buildTtySwitchScript(%q) does not log to %s", c.dm, switchLogFile)
		}
		if !strings.Contains(script, "switch-to-tty") {
			t.Errorf("buildTtySwitchScript(%q) missing switch-to-tty marker", c.dm)
		}
		if c.dm != "" {
			if !strings.Contains(script, "systemctl stop "+c.dm) {
				t.Errorf("buildTtySwitchScript(%q) should target exactly the detected DM", c.dm)
			}
			if strings.Contains(script, "systemctl isolate multi-user.target") {
				t.Errorf("buildTtySwitchScript(%q) should not fall back to isolate multi-user.target when a DM was detected", c.dm)
			}
		}
	}
}

// launchDetached / runDetachedModeSwitch / detectDisplayManager /
// syncLinuxHostsFile / isSystemdResolvedManaged all either hardcode real
// system paths (/etc/X11/default-display-manager, /etc/hosts,
// /etc/resolv.conf, /run/systemd/resolve/...) or invoke systemd
// (systemctl/systemd-run/setsid) directly with no injectable seams. Per the
// audit's safety rules, they are intentionally NOT exercised here -- see
// FINAL_AUDIT_REPORT.md's testability-gap findings instead of adding a test
// that would read or write real system state.
