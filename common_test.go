package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// validateHostname
// ---------------------------------------------------------------------------

func TestValidateHostname(t *testing.T) {
	valid := []string{
		"a",
		"abc123",
		"a-b-c",
		strings.Repeat("a", 63),
	}
	for _, h := range valid {
		if err := validateHostname(h); err != nil {
			t.Errorf("validateHostname(%q) expected valid, got error: %v", h, err)
		}
	}

	invalid := []string{
		"",
		strings.Repeat("a", 64),
		"-abc",
		"abc-",
		"ab_c",
		"ab c",
		"ab;id",
		"$(whoami)",
		"host`id`",
		"héllo",
		"a..b",
	}
	for _, h := range invalid {
		if err := validateHostname(h); err == nil {
			t.Errorf("validateHostname(%q) expected error, got nil", h)
		}
	}
}

// ---------------------------------------------------------------------------
// validateCliCommand
// ---------------------------------------------------------------------------

func TestValidateCliCommand(t *testing.T) {
	valid := []string{
		"ab",
		"sw_1-2",
		"switcher-wizard",
		strings.Repeat("a", 32),
	}
	for _, c := range valid {
		if err := validateCliCommand(c); err != nil {
			t.Errorf("validateCliCommand(%q) expected valid, got error: %v", c, err)
		}
	}

	invalid := []string{
		"",
		"a",
		strings.Repeat("a", 33),
		"sw cmd",
		"sw;cmd",
		"sw$(id)",
		"sw`id`",
		"héllo",
	}
	for _, c := range invalid {
		if err := validateCliCommand(c); err == nil {
			t.Errorf("validateCliCommand(%q) expected error, got nil", c)
		}
	}
}

// ---------------------------------------------------------------------------
// parseAndValidateProxy — includes shell/PowerShell injection attempts
// ---------------------------------------------------------------------------

func TestParseAndValidateProxy_Valid(t *testing.T) {
	cases := []struct {
		input, host, port string
	}{
		{"127.0.0.1:8080", "127.0.0.1", "8080"},
		{"http://127.0.0.1:8080", "127.0.0.1", "8080"},
		{"https://proxy.company.com:3128", "proxy.company.com", "3128"},
		{" 127.0.0.1:8080 ", "127.0.0.1", "8080"},
		{"127.0.0.1:1", "127.0.0.1", "1"},
		{"127.0.0.1:65535", "127.0.0.1", "65535"},
		{"[::1]:8080", "::1", "8080"},
		{"[2001:db8::1]:3128", "2001:db8::1", "3128"},
		{"http://[::1]:8080", "::1", "8080"},
		{" [::1]:8080 ", "::1", "8080"},
	}
	for _, c := range cases {
		host, port, err := parseAndValidateProxy(c.input)
		if err != nil {
			t.Errorf("parseAndValidateProxy(%q) unexpected error: %v", c.input, err)
			continue
		}
		if host != c.host || port != c.port {
			t.Errorf("parseAndValidateProxy(%q) = (%q,%q), want (%q,%q)", c.input, host, port, c.host, c.port)
		}
	}

	// Maximum allowed host length (255 chars: first+last anchor + 253 middle).
	maxHost := "a" + strings.Repeat("b", 253) + "a"
	if len(maxHost) != 255 {
		t.Fatalf("test setup error: maxHost length = %d, want 255", len(maxHost))
	}
	if _, _, err := parseAndValidateProxy(maxHost + ":8080"); err != nil {
		t.Errorf("parseAndValidateProxy with 255-char host unexpectedly rejected: %v", err)
	}
}

func TestParseAndValidateProxy_Invalid(t *testing.T) {
	invalid := []string{
		"",
		"justhost",
		"host:port:extra",
		"a;id:8080",
		"$(id):8080",
		"`id`:8080",
		"'id':8080",
		`"id":8080`,
		"ho st:8080",
		"héllo.com:8080",
		":8080",
		"127.0.0.1:",
		"127.0.0.1:0",
		"127.0.0.1:65536",
		"127.0.0.1:abc",
		"127.0.0.1:-1",
		"127.0.0.1:99999",
		"::1:8080", // unbracketed IPv6 is ambiguous -- must be rejected, not guessed at
		"a:b:c",    // same ambiguity, non-IP
		"[::1]:0",
		"[::1]:65536",
		"[::1]:abc",
		"[::1; rm -rf /]:8080", // injection attempt disguised as an IPv6 literal
		"[$(id)::1]:8080",
		"[`id`::1]:8080",
		"[::1' ; id ; ']:8080",
	}
	for _, in := range invalid {
		if _, _, err := parseAndValidateProxy(in); err == nil {
			t.Errorf("parseAndValidateProxy(%q) expected error, got nil", in)
		}
	}

	// One character over the allowed host length must be rejected.
	tooLong := "a" + strings.Repeat("b", 254) + "a" // 256 chars
	if len(tooLong) != 256 {
		t.Fatalf("test setup error: tooLong length = %d, want 256", len(tooLong))
	}
	if _, _, err := parseAndValidateProxy(tooLong + ":8080"); err == nil {
		t.Errorf("parseAndValidateProxy with 256-char host expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// validateCIDR
// ---------------------------------------------------------------------------

func TestValidateCIDR(t *testing.T) {
	validCases := map[string]string{
		"192.168.1.1/24": "192.168.1.1/24",
		"192.168.1.1/0":  "192.168.1.1/0",
		"192.168.1.1/32": "192.168.1.1/32",
		" 10.0.0.5/8 ":   "10.0.0.5/8",
	}
	for in, want := range validCases {
		got, err := validateCIDR(in)
		if err != nil {
			t.Errorf("validateCIDR(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("validateCIDR(%q) = %q, want %q", in, got, want)
		}
	}

	invalid := []string{
		"192.168.1.1",
		"192.168.1.1/33",
		"not-an-ip/24",
		"::1/64", // IPv6 rejected -- only IPv4 supported
		"",
	}
	for _, in := range invalid {
		if _, err := validateCIDR(in); err == nil {
			t.Errorf("validateCIDR(%q) expected error, got nil", in)
		}
	}
}

// ---------------------------------------------------------------------------
// normalizeAddress / parsePrefix / prefixToMask / splitCIDR
// ---------------------------------------------------------------------------

func TestNormalizeAddress(t *testing.T) {
	got, err := normalizeAddress("192.168.1.5", "255.255.255.0")
	if err != nil || got != "192.168.1.5/24" {
		t.Errorf("normalizeAddress mask form = (%q, %v), want (192.168.1.5/24, nil)", got, err)
	}

	got, err = normalizeAddress("192.168.1.5", "/24")
	if err != nil || got != "192.168.1.5/24" {
		t.Errorf("normalizeAddress prefix form = (%q, %v), want (192.168.1.5/24, nil)", got, err)
	}

	if _, err := normalizeAddress("192.168.1.5", "255.255.255.1"); err == nil {
		t.Error("normalizeAddress with non-contiguous mask expected error, got nil")
	}
	if _, err := normalizeAddress("not-ip", "255.255.255.0"); err == nil {
		t.Error("normalizeAddress with invalid IP expected error, got nil")
	}
	if _, err := normalizeAddress("192.168.1.5", "bogus"); err == nil {
		t.Error("normalizeAddress with invalid mask expected error, got nil")
	}
}

func TestParsePrefix(t *testing.T) {
	validCases := map[string]int{
		"/24":             24,
		"/0":              0,
		"/32":             32,
		"255.255.255.0":   24,
		"255.255.255.255": 32,
		"0.0.0.0":         0,
	}
	for in, want := range validCases {
		got, err := parsePrefix(in)
		if err != nil {
			t.Errorf("parsePrefix(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parsePrefix(%q) = %d, want %d", in, got, want)
		}
	}

	invalid := []string{"/33", "/-1", "255.255.0.255", "bogus", ""}
	for _, in := range invalid {
		if _, err := parsePrefix(in); err == nil {
			t.Errorf("parsePrefix(%q) expected error, got nil", in)
		}
	}
}

func TestPrefixToMask(t *testing.T) {
	validCases := map[int]string{
		0:  "0.0.0.0",
		24: "255.255.255.0",
		32: "255.255.255.255",
	}
	for prefix, want := range validCases {
		got, err := prefixToMask(prefix)
		if err != nil || got != want {
			t.Errorf("prefixToMask(%d) = (%q,%v), want (%q,nil)", prefix, got, err, want)
		}
	}

	for _, prefix := range []int{-1, 33} {
		if _, err := prefixToMask(prefix); err == nil {
			t.Errorf("prefixToMask(%d) expected error, got nil", prefix)
		}
	}
}

func TestSplitCIDR(t *testing.T) {
	ip, prefix, err := splitCIDR("192.168.1.5/24")
	if err != nil || ip != "192.168.1.5" || prefix != 24 {
		t.Errorf("splitCIDR = (%q,%d,%v), want (192.168.1.5,24,nil)", ip, prefix, err)
	}

	if _, _, err := splitCIDR("not-valid"); err == nil {
		t.Error("splitCIDR(invalid) expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// validateGateway
// ---------------------------------------------------------------------------

func TestValidateGateway(t *testing.T) {
	validInputs := []string{"", "   ", "192.168.1.1", "10.0.0.1"}
	for _, in := range validInputs {
		if err := validateGateway(in); err != nil {
			t.Errorf("validateGateway(%q) unexpected error: %v", in, err)
		}
	}

	invalidInputs := []string{"bogus", "::1", "999.999.999.999"}
	for _, in := range invalidInputs {
		if err := validateGateway(in); err == nil {
			t.Errorf("validateGateway(%q) expected error, got nil", in)
		}
	}
}

// ---------------------------------------------------------------------------
// splitDNSInput / validateDNSServers
// ---------------------------------------------------------------------------

func TestSplitDNSInput(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"1.1.1.1,8.8.8.8", []string{"1.1.1.1", "8.8.8.8"}},
		{" 1.1.1.1 , 8.8.8.8 ", []string{"1.1.1.1", "8.8.8.8"}},
		{"", nil},
		{",,", nil},
		{"1.1.1.1,,8.8.8.8", []string{"1.1.1.1", "8.8.8.8"}},
	}
	for _, c := range cases {
		got := splitDNSInput(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitDNSInput(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitDNSInput(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestValidateDNSServers(t *testing.T) {
	if err := validateDNSServers(nil); err == nil {
		t.Error("validateDNSServers(nil) expected error, got nil")
	}
	if err := validateDNSServers([]string{}); err == nil {
		t.Error("validateDNSServers(empty) expected error, got nil")
	}
	if err := validateDNSServers([]string{"1.1.1.1"}); err != nil {
		t.Errorf("validateDNSServers([1.1.1.1]) unexpected error: %v", err)
	}
	if err := validateDNSServers([]string{"1.1.1.1", "bogus"}); err == nil {
		t.Error("validateDNSServers with one bad entry expected error, got nil")
	}
	// Documents a real asymmetry: unlike validateGateway/validateCIDR, this
	// validator accepts IPv6 because it only calls net.ParseIP without a
	// To4() check. Not asserting this is "wrong" -- just pinning the
	// current, inconsistent behavior so a future change is deliberate.
	if err := validateDNSServers([]string{"2001:db8::1"}); err != nil {
		t.Errorf("validateDNSServers([IPv6]) unexpected error: %v (documents IPv4/IPv6 asymmetry vs validateGateway)", err)
	}
}

// ---------------------------------------------------------------------------
// outputOrSuccess / commandError
// ---------------------------------------------------------------------------

func TestOutputOrSuccess(t *testing.T) {
	got := outputOrSuccess("", "Done")
	if !strings.Contains(got, "Done") {
		t.Errorf("outputOrSuccess empty-output case missing success message: %q", got)
	}
	if strings.Contains(got, "\n\n") {
		t.Errorf("outputOrSuccess empty-output case should not append blank output block: %q", got)
	}

	got = outputOrSuccess("raw tool output", "Done")
	if !strings.Contains(got, "Done") || !strings.Contains(got, "raw tool output") {
		t.Errorf("outputOrSuccess non-empty-output case missing content: %q", got)
	}
}

func TestCommandError(t *testing.T) {
	err := &testError{"boom"}
	got := commandError("Failed to do X", "", err)
	if !strings.Contains(got, "Failed to do X") || !strings.Contains(got, "boom") {
		t.Errorf("commandError missing operation/error text: %q", got)
	}

	got = commandError("Failed to do X", "tool output", err)
	if !strings.Contains(got, "tool output") {
		t.Errorf("commandError should include raw tool output: %q", got)
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// ---------------------------------------------------------------------------
// visibleWidth / padDisplayWidth / truncateDisplayWidth / valueOrNone (main.go)
// ---------------------------------------------------------------------------

func TestVisibleWidth(t *testing.T) {
	cases := map[string]int{
		"":                               0,
		"hello":                          5,
		"\033[1;33mhi\033[0m":            2,
		"héllo":                          5,
		"\033[38;2;6;182;212m╔═╗\033[0m": 3,
	}
	for in, want := range cases {
		if got := visibleWidth(in); got != want {
			t.Errorf("visibleWidth(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestPadDisplayWidth(t *testing.T) {
	if got := padDisplayWidth("abc", 5); got != "abc  " {
		t.Errorf("padDisplayWidth(abc,5) = %q, want %q", got, "abc  ")
	}
	if got := padDisplayWidth("abcde", 5); got != "abcde" {
		t.Errorf("padDisplayWidth(abcde,5) = %q, want %q", got, "abcde")
	}
	if got := padDisplayWidth("abcdef", 5); got != "abcde" {
		t.Errorf("padDisplayWidth(abcdef,5) = %q, want %q", got, "abcde")
	}
}

func TestTruncateDisplayWidth(t *testing.T) {
	if got := truncateDisplayWidth("abcdef", 3); got != "abc" {
		t.Errorf("truncateDisplayWidth(abcdef,3) = %q, want %q", got, "abc")
	}
	if got := truncateDisplayWidth("abc", 0); got != "" {
		t.Errorf("truncateDisplayWidth(abc,0) = %q, want empty", got)
	}
	// Documents current behavior: a trailing ANSI reset placed AFTER the
	// visible-width budget is reached gets dropped, because the loop
	// breaks on the over-budget rune before it ever sees the escape
	// sequence that follows. This can leave a color code unterminated.
	got := truncateDisplayWidth("\033[31mabcdef\033[0m", 3)
	want := "\033[31mabc"
	if got != want {
		t.Errorf("truncateDisplayWidth with ANSI = %q, want %q (see test comment re: dropped trailing reset)", got, want)
	}
}

func TestValueOrNone(t *testing.T) {
	cases := map[string]string{
		"":        "(none)",
		"   ":     "(none)",
		"abc":     "abc",
		"  abc  ": "abc",
	}
	for in, want := range cases {
		if got := valueOrNone(in); got != want {
			t.Errorf("valueOrNone(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Shell-profile block management (appendShellProfileBlock / stripShellProfileBlock)
// These take an explicit file path, so they're safe to exercise against a
// temp file -- never a real shell profile.
// ---------------------------------------------------------------------------

func TestShellProfileBlockLifecycle(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "fake_profile")

	original := "# pre-existing user content\nexport FOO=bar\n"
	if err := os.WriteFile(profile, []byte(original), 0644); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}

	appendShellProfileBlock(profile, "switcher-wizard", "/usr/local/bin/switcher-wizard")

	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("ReadFile after append failed: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "# pre-existing user content") {
		t.Error("appendShellProfileBlock destroyed pre-existing profile content")
	}
	if !strings.Contains(content, "# >>> SWITCHER SUITE CLI BLOCK >>>") ||
		!strings.Contains(content, "# <<< SWITCHER SUITE CLI BLOCK <<<") {
		t.Error("appendShellProfileBlock did not write the expected markers")
	}
	if !strings.Contains(content, "alias switcher-wizard='sudo /usr/local/bin/switcher-wizard'") {
		t.Errorf("appendShellProfileBlock did not write the expected alias line, got: %s", content)
	}

	// Calling it a second time (re-enabling with a different command) must
	// not leave two stacked blocks behind.
	appendShellProfileBlock(profile, "sw2", "/usr/local/bin/sw2")
	data, _ = os.ReadFile(profile)
	content = string(data)
	if strings.Count(content, "# >>> SWITCHER SUITE CLI BLOCK >>>") != 1 {
		t.Errorf("appendShellProfileBlock called twice left %d start markers, want 1:\n%s",
			strings.Count(content, "# >>> SWITCHER SUITE CLI BLOCK >>>"), content)
	}
	if strings.Contains(content, "switcher-wizard") {
		t.Error("second appendShellProfileBlock call should have replaced the first alias, not kept it")
	}

	stripShellProfileBlock(profile)
	data, _ = os.ReadFile(profile)
	content = string(data)
	if strings.Contains(content, "SWITCHER SUITE CLI BLOCK") {
		t.Error("stripShellProfileBlock left block markers behind")
	}
	if !strings.Contains(content, "# pre-existing user content") {
		t.Error("stripShellProfileBlock destroyed pre-existing profile content")
	}
}

func TestStripShellProfileBlock_MissingFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist")
	// Must not panic when the file doesn't exist.
	stripShellProfileBlock(missing)
}

// ---------------------------------------------------------------------------
// Relative state-file helpers (hostname rollback, CLI alias record, proxy
// record). These use fixed *relative* filenames, so tests chdir into a temp
// directory rather than touching anything in the real project directory.
// ---------------------------------------------------------------------------

func withTempCWD(t *testing.T) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})
}

func TestPreviousHostnameRoundTrip(t *testing.T) {
	withTempCWD(t)

	savePreviousHostname("old-host-name")
	if got := getPreviousHostname(); got != "old-host-name" {
		t.Errorf("getPreviousHostname() = %q, want %q", got, "old-host-name")
	}

	// Saving an empty name must be a no-op (not overwrite with blank).
	savePreviousHostname("")
	if got := getPreviousHostname(); got != "old-host-name" {
		t.Errorf("getPreviousHostname() after empty save = %q, want unchanged %q", got, "old-host-name")
	}
}

func TestGetPreviousHostname_NoFileFallsBackToCurrent(t *testing.T) {
	withTempCWD(t)

	want, _ := os.Hostname()
	if got := getPreviousHostname(); got != want {
		t.Errorf("getPreviousHostname() with no saved file = %q, want current hostname %q", got, want)
	}
}

func TestActiveCliCommandRoundTrip(t *testing.T) {
	withTempCWD(t)

	if got := getActiveCliCommand(); got != "" {
		t.Errorf("getActiveCliCommand() with no record = %q, want empty", got)
	}

	saveActiveCliCommand("switcher-wizard")
	if got := getActiveCliCommand(); got != "switcher-wizard" {
		t.Errorf("getActiveCliCommand() = %q, want %q", got, "switcher-wizard")
	}

	removeActiveCliRecord()
	if got := getActiveCliCommand(); got != "" {
		t.Errorf("getActiveCliCommand() after remove = %q, want empty", got)
	}
}

func TestActiveProxyRecordRoundTrip(t *testing.T) {
	withTempCWD(t)

	if got := getActiveProxyRecord(); got != "" {
		t.Errorf("getActiveProxyRecord() with no record = %q, want empty", got)
	}

	saveActiveProxyRecord("127.0.0.1:8080")
	if got := getActiveProxyRecord(); got != "127.0.0.1:8080" {
		t.Errorf("getActiveProxyRecord() = %q, want %q", got, "127.0.0.1:8080")
	}

	removeActiveProxyRecord()
	if got := getActiveProxyRecord(); got != "" {
		t.Errorf("getActiveProxyRecord() after remove = %q, want empty", got)
	}
}

// TestActiveProxyRecordRoundTrip_IPv6 exercises the full pipeline a real
// "set proxy" -> "test current proxy" cycle goes through: build the
// endpoint with net.JoinHostPort (what setSystemProxy now does), save it,
// read it back, and re-parse it with parseAndValidateProxy (what
// testCurrentProxyFlow does with whatever getCurrentProxy returns) --
// confirming the bracketed IPv6 form survives the save/load/parse cycle
// intact.
func TestActiveProxyRecordRoundTrip_IPv6(t *testing.T) {
	withTempCWD(t)

	endpoint := net.JoinHostPort("::1", "8080")
	if endpoint != "[::1]:8080" {
		t.Fatalf("net.JoinHostPort(%q, %q) = %q, want %q", "::1", "8080", endpoint, "[::1]:8080")
	}

	saveActiveProxyRecord(endpoint)

	rec := getActiveProxyRecord()
	if rec != endpoint {
		t.Fatalf("getActiveProxyRecord() = %q, want %q", rec, endpoint)
	}

	host, port, err := parseAndValidateProxy(rec)
	if err != nil {
		t.Fatalf("parseAndValidateProxy(%q) unexpected error: %v", rec, err)
	}
	if host != "::1" || port != "8080" {
		t.Errorf("parseAndValidateProxy(%q) = (%q,%q), want (%q,%q)", rec, host, port, "::1", "8080")
	}

	removeActiveProxyRecord()
}

// ---------------------------------------------------------------------------
// commandExists -- safe, read-only PATH lookups.
// ---------------------------------------------------------------------------

func TestCommandExists(t *testing.T) {
	if !commandExists("go") {
		t.Skip("go not on PATH in this environment; skipping positive case")
	}
	if commandExists("this-command-definitely-does-not-exist-xyz123") {
		t.Error("commandExists returned true for a nonexistent command")
	}
}

// ---------------------------------------------------------------------------
// N1: Global CLI alias collision protection
// ---------------------------------------------------------------------------

func TestIsReservedCliCommand(t *testing.T) {
	reserved := []string{"sudo", "SSH", "Bash", "top", "systemctl", "docker", "rm", "netsh"}
	for _, name := range reserved {
		if !isReservedCliCommand(name) {
			t.Errorf("isReservedCliCommand(%q) = false, want true", name)
		}
	}

	notReserved := []string{"switcher-wizard", "myalias", "sw", "switcher123"}
	for _, name := range notReserved {
		if isReservedCliCommand(name) {
			t.Errorf("isReservedCliCommand(%q) = true, want false", name)
		}
	}
}

func TestFileHasCliShimMarker(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "does-not-exist")
	if fileHasCliShimMarker(missing) {
		t.Error("fileHasCliShimMarker(missing file) = true, want false")
	}

	foreign := filepath.Join(dir, "foreign-binary")
	if err := os.WriteFile(foreign, []byte("#!/bin/bash\necho hi\n"), 0755); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}
	if fileHasCliShimMarker(foreign) {
		t.Error("fileHasCliShimMarker(foreign file) = true, want false")
	}

	ours := filepath.Join(dir, "our-shim")
	content := "#!/bin/bash\n# " + cliShimMarker + "\nexec sudo \"/opt/x\" \"$@\"\n"
	if err := os.WriteFile(ours, []byte(content), 0755); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}
	if !fileHasCliShimMarker(ours) {
		t.Error("fileHasCliShimMarker(our shim) = false, want true")
	}
}

func TestCheckCliAliasSafeToClaim_Reserved(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "sudo")
	if err := checkCliAliasSafeToClaim("sudo", []string{candidate}); err == nil {
		t.Error("checkCliAliasSafeToClaim(\"sudo\") expected error, got nil")
	}
}

func TestCheckCliAliasSafeToClaim_NoCollision(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "myalias")
	if err := checkCliAliasSafeToClaim("myalias", []string{candidate}); err != nil {
		t.Errorf("checkCliAliasSafeToClaim with no collision unexpectedly failed: %v", err)
	}
}

func TestCheckCliAliasSafeToClaim_CandidatePathCollision(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "myalias")

	// A foreign file (no marker) at the candidate path must block the claim.
	if err := os.WriteFile(candidate, []byte("not ours"), 0755); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}
	if err := checkCliAliasSafeToClaim("myalias", []string{candidate}); err == nil {
		t.Error("checkCliAliasSafeToClaim expected error for foreign file at candidate path, got nil")
	}

	// Once the file carries our marker, re-claiming (e.g. re-enabling) must
	// be allowed -- this is what makes enable idempotent.
	if err := os.WriteFile(candidate, []byte("#!/bin/bash\n# "+cliShimMarker+"\n"), 0755); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}
	if err := checkCliAliasSafeToClaim("myalias", []string{candidate}); err != nil {
		t.Errorf("checkCliAliasSafeToClaim with our own marker present unexpectedly failed: %v", err)
	}
}

func TestCheckCliAliasSafeToClaim_DirectoryAtCandidatePath(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "myalias")
	if err := os.Mkdir(candidate, 0755); err != nil {
		t.Fatalf("setup Mkdir failed: %v", err)
	}
	if err := checkCliAliasSafeToClaim("myalias", []string{candidate}); err == nil {
		t.Error("checkCliAliasSafeToClaim expected error when candidate path is a directory, got nil")
	}
}

func TestCheckCliAliasSafeToClaim_PathLookupCollision(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "myalias")
	if err := os.WriteFile(foreign, []byte("#!/bin/bash\necho hi\n"), 0755); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}

	t.Setenv("PATH", dir)

	// No candidatePaths at all -- the collision must still be caught via
	// exec.LookPath against PATH, not just the explicit candidate list.
	if err := checkCliAliasSafeToClaim("myalias", nil); err == nil {
		t.Error("checkCliAliasSafeToClaim expected error for a PATH collision with no shim marker, got nil")
	}
}

func TestCheckCliAliasSafeToClaim_MaliciousNames(t *testing.T) {
	// These would already be rejected upstream by validateCliCommand before
	// ever reaching this function in the real enable flow. This test only
	// documents that checkCliAliasSafeToClaim itself never shells out or
	// panics on such input -- it's a plain PATH/file-stat lookup, not a
	// shell invocation, so these are simply treated as (safe) non-matches.
	malicious := []string{
		"a;id",
		"$(id)",
		"`id`",
		"a b",
		`a"b`,
		"a'b",
	}
	for _, name := range malicious {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("checkCliAliasSafeToClaim(%q) panicked: %v", name, r)
				}
			}()
			_ = checkCliAliasSafeToClaim(name, nil)
		}()
	}
}

// ---------------------------------------------------------------------------
// N2: Windows elevation argument handling (no shell/PowerShell injection)
// ---------------------------------------------------------------------------

func TestFilterElevationArgs(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"--cli-shell"}, []string{"--cli-shell"}},
		{[]string{"--cli-shell", "extra"}, []string{"--cli-shell"}},
		{nil, []string{}},
		{[]string{}, []string{}},
		{[]string{"a;id"}, []string{}},
		{[]string{"$(id)"}, []string{}},
		{[]string{"`id`"}, []string{}},
		{[]string{"'; calc.exe; '"}, []string{}},
		{[]string{`" ; Start-Process calc ; "`}, []string{}},
		{[]string{"a b"}, []string{}},
	}
	for _, c := range cases {
		got := filterElevationArgs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("filterElevationArgs(%v) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("filterElevationArgs(%v) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestPowerShellSingleQuote(t *testing.T) {
	cases := map[string]string{
		"--cli-shell":    "'--cli-shell'",
		"a;id":           "'a;id'",
		"$(id)":          "'$(id)'",
		"`id`":           "'`id`'",
		"a'b":            "'a''b'",
		"he said 'hi'":   "'he said ''hi'''",
		`"; calc.exe; "`: `'"; calc.exe; "'`,
	}
	for in, want := range cases {
		if got := powerShellSingleQuote(in); got != want {
			t.Errorf("powerShellSingleQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAutoElevateArgsNeverContainRawInjection documents, end to end, that no
// malicious argument can ever reach the PowerShell -ArgumentList string
// unescaped: it's first dropped by filterElevationArgs unless it's exactly
// the one whitelisted flag, and even that flag is still run through
// powerShellSingleQuote before being joined into the command string.
func TestAutoElevateArgsNeverContainRawInjection(t *testing.T) {
	malicious := []string{
		"a;id",
		"$(id)",
		"`id`",
		"'; Start-Process calc.exe; '",
		`" ; Start-Process calc.exe ; "`,
		"a b",
	}

	safe := filterElevationArgs(malicious)
	if len(safe) != 0 {
		t.Fatalf("filterElevationArgs(malicious) = %v, want empty (nothing malicious is ever whitelisted)", safe)
	}

	// Even if a future change to the whitelist ever let one of these
	// through, the quoting step must still neutralize an embedded quote.
	quoted := powerShellSingleQuote("'; Start-Process calc.exe; '")
	if strings.Count(quoted, "''") < 2 {
		t.Errorf("powerShellSingleQuote did not double-escape embedded quotes: %q", quoted)
	}
}

// ---------------------------------------------------------------------------
// N1 follow-up: legacy (pre-marker) shim recognition, so files written by
// an older version of this tool don't get permanently stuck (un-reclaimable
// and un-removable) after the marker/collision checks were introduced.
// ---------------------------------------------------------------------------

// renderLegacyTemplateForTest replaces literal "%s" placeholders in a
// legacy shim template with values, in order. It deliberately does not use
// fmt.Sprintf: the legacy Windows template contains literal batch tokens
// like %errorLevel% and %~1 that Sprintf's verb parser would otherwise try
// (and fail) to interpret as format verbs.
func renderLegacyTemplateForTest(template string, values ...string) string {
	rendered := template
	for _, v := range values {
		rendered = strings.Replace(rendered, "%s", v, 1)
	}
	return rendered
}

func TestIsOwnedCliShimFile_CurrentMarker(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shim")
	content := "#!/bin/bash\n# " + cliShimMarker + "\nexec sudo \"/opt/tool\" \"$@\"\n"
	if err := os.WriteFile(p, []byte(content), 0755); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}
	if !isOwnedCliShimFile(p) {
		t.Error("isOwnedCliShimFile with current marker = false, want true")
	}
}

func TestIsOwnedCliShimFile_LegacyUnixTemplate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shim")
	content := renderLegacyTemplateForTest(legacyUnixShimTemplate, "/usr/local/bin/Switcher_Suite_Wizard")
	if err := os.WriteFile(p, []byte(content), 0755); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}
	if !isOwnedCliShimFile(p) {
		t.Error("isOwnedCliShimFile with exact legacy Unix template = false, want true")
	}
}

func TestIsOwnedCliShimFile_LegacyWindowsTemplate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shim.cmd")
	execPath := `C:\Program Files\Switcher\Switcher_Suite_Wizard.exe`
	content := renderLegacyTemplateForTest(legacyWindowsShimTemplate, execPath, execPath, execPath)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}
	if !isOwnedCliShimFile(p) {
		t.Error("isOwnedCliShimFile with exact legacy Windows template = false, want true")
	}
}

func TestIsOwnedCliShimFile_ForeignFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shim")
	if err := os.WriteFile(p, []byte("#!/bin/bash\necho 'not ours'\n"), 0755); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}
	if isOwnedCliShimFile(p) {
		t.Error("isOwnedCliShimFile with unrelated content = true, want false")
	}
}

func TestIsOwnedCliShimFile_ForeignFileResemblingLegacy(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shim")
	// One extra, unrelated line disqualifies it -- matching the legacy
	// template means exact, not "looks similar."
	content := "#!/bin/bash\necho hello\nexec sudo \"/opt/tool\" \"$@\"\n"
	if err := os.WriteFile(p, []byte(content), 0755); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}
	if isOwnedCliShimFile(p) {
		t.Error("isOwnedCliShimFile with a foreign file that merely resembles the legacy template = true, want false")
	}
}

func TestCheckCliAliasSafeToClaim_LegacyShimAllowsReenableAndDisable(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "switcher-wizard")
	content := fmt.Sprintf(legacyUnixShimTemplate, "/usr/local/bin/Switcher_Suite_Wizard")
	if err := os.WriteFile(candidate, []byte(content), 0755); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}

	// Re-enabling the default alias over a legacy shim must be allowed...
	if err := checkCliAliasSafeToClaim("switcher-wizard", []string{candidate}); err != nil {
		t.Errorf("checkCliAliasSafeToClaim should allow re-claiming a legacy shim, got error: %v", err)
	}
	// ...and disable's removal predicate (isOwnedCliShimFile) must agree
	// it's safe to delete.
	if !isOwnedCliShimFile(candidate) {
		t.Error("isOwnedCliShimFile should consider a legacy shim removable by disable, got false")
	}
}

func TestCheckCliAliasSafeToClaim_ForeignFileSimilarNameStillRefused(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "switcher-wizard")
	// A totally unrelated real file that happens to sit at the exact
	// candidate path, under the exact default alias name, must still be
	// refused -- and must not be deletable by disable either.
	if err := os.WriteFile(candidate, []byte("#!/bin/bash\necho 'some other unrelated tool'\n"), 0755); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}

	if err := checkCliAliasSafeToClaim("switcher-wizard", []string{candidate}); err == nil {
		t.Error("checkCliAliasSafeToClaim should refuse a foreign file at the candidate path even under the default alias name, got nil")
	}
	if isOwnedCliShimFile(candidate) {
		t.Error("isOwnedCliShimFile should not consider an unrelated foreign file removable, got true")
	}
}

// ---------------------------------------------------------------------------
// N2 follow-up: Windows Global CLI shim ArgumentList injection
// (buildWindowsCliShim, system_common.go -- kept build-tag-free so it can
// be rendered and asserted on from any platform, including this one).
// ---------------------------------------------------------------------------

// extractPowerShellCommandSegments pulls out the string following each
// `-Command "..."` in rendered batch content, for inspection.
func extractPowerShellCommandSegments(t *testing.T, shim string) []string {
	t.Helper()
	re := regexp.MustCompile(`-Command "([^\n]*)"`)
	matches := re.FindAllStringSubmatch(shim, -1)
	segments := make([]string, 0, len(matches))
	for _, m := range matches {
		segments = append(segments, m[1])
	}
	return segments
}

func TestBuildWindowsCliShim_NoRuntimeArgsReachPowerShellCommand(t *testing.T) {
	shim := buildWindowsCliShim(`C:\Program Files\Switcher\Switcher_Suite_Wizard.exe`)

	segments := extractPowerShellCommandSegments(t, shim)
	if len(segments) == 0 {
		t.Fatal("no -Command segments found in rendered shim; test setup is wrong")
	}
	for _, seg := range segments {
		for _, tok := range []string{"%*", "%~1", "%~2"} {
			if strings.Contains(seg, tok) {
				t.Errorf("PowerShell -Command segment contains raw batch argument token %q (whatever the caller typed could reach it): %s", tok, seg)
			}
		}
	}

	if !strings.Contains(shim, "-ArgumentList '"+windowsCliShimAllowedArg+"'") {
		t.Errorf("rendered shim missing the fixed, whitelisted -ArgumentList literal:\n%s", shim)
	}
	if !strings.Contains(shim, cliShimMarker) {
		t.Error("rendered shim missing the shim marker")
	}
}

func TestBuildWindowsCliShim_MaliciousExecPathNeverBreaksOutOfQuotes(t *testing.T) {
	// These are all legal Windows filename characters (only < > : " / \ | ?
	// * are illegal), so a path like this is a realistic, if unlikely,
	// install location -- not just a synthetic attack string.
	maliciousPaths := []string{
		`C:\Tools\a'b.exe`,
		`C:\Tools\a;id.exe`,
		`C:\Tools\a$(id).exe`,
		"C:\\Tools\\a`id`.exe",
		`C:\Tools\a b.exe`,
	}

	for _, path := range maliciousPaths {
		shim := buildWindowsCliShim(path)
		segments := extractPowerShellCommandSegments(t, shim)
		if len(segments) == 0 {
			t.Fatalf("path %q: no -Command segments found; test setup is wrong", path)
		}

		quoteCount := strings.Count(path, "'")
		for _, seg := range segments {
			if quoteCount > 0 {
				if doubled := strings.Count(seg, "''"); doubled < quoteCount {
					t.Errorf("path %q: expected %d embedded quote(s) doubled in segment, got: %s", path, quoteCount, seg)
				}
			}
			// Whatever the path contains, the -ArgumentList value (when
			// present) must always be exactly the fixed whitelisted flag,
			// never anything derived from the path.
			if strings.Contains(seg, "-ArgumentList") && !strings.Contains(seg, "-ArgumentList '"+windowsCliShimAllowedArg+"'") {
				t.Errorf("path %q: -ArgumentList segment is not the fixed whitelisted literal: %s", path, seg)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// SSH Server -- shared, OS-agnostic idempotency/firewall-prompt decisions
// ---------------------------------------------------------------------------

func TestSSHServerNeedsSetup(t *testing.T) {
	cases := []struct {
		name   string
		status SSHServerStatus
		want   bool
	}{
		{"fully active, nothing to do", SSHServerStatus{Installed: true, Enabled: true, Running: true}, false},
		{"not installed", SSHServerStatus{Installed: false, Enabled: false, Running: false}, true},
		{"installed but not enabled", SSHServerStatus{Installed: true, Enabled: false, Running: true}, true},
		{"installed and enabled but not running", SSHServerStatus{Installed: true, Enabled: true, Running: false}, true},
		{"nothing set", SSHServerStatus{}, true},
	}
	for _, c := range cases {
		if got := sshServerNeedsSetup(c.status); got != c.want {
			t.Errorf("%s: sshServerNeedsSetup(%+v) = %v, want %v", c.name, c.status, got, c.want)
		}
	}
}

func TestSSHServerNeedsFirewallPrompt(t *testing.T) {
	cases := []struct {
		name   string
		status SSHServerStatus
		want   bool
	}{
		{"no firewall tool (e.g. macOS) -- never prompt", SSHServerStatus{FirewallTool: "", FirewallActive: true, FirewallAllowsSSH: false}, false},
		{"firewall tool present but inactive -- no prompt", SSHServerStatus{FirewallTool: "ufw", FirewallActive: false, FirewallAllowsSSH: false}, false},
		{"firewall active and already allows ssh -- no prompt", SSHServerStatus{FirewallTool: "ufw", FirewallActive: true, FirewallAllowsSSH: true}, false},
		{"firewall active and blocks ssh -- prompt", SSHServerStatus{FirewallTool: "ufw", FirewallActive: true, FirewallAllowsSSH: false}, true},
		{"windows firewall active and blocks ssh -- prompt", SSHServerStatus{FirewallTool: "Windows Defender Firewall", FirewallActive: true, FirewallAllowsSSH: false}, true},
	}
	for _, c := range cases {
		if got := sshServerNeedsFirewallPrompt(c.status); got != c.want {
			t.Errorf("%s: sshServerNeedsFirewallPrompt(%+v) = %v, want %v", c.name, c.status, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Main menu wraparound -- derived from the real item count, not a
// hardcoded number (this is exactly the bug the SSH Server menu entry
// would otherwise have reintroduced: adding a 10th item without updating
// a hardcoded 8/9 elsewhere).
// ---------------------------------------------------------------------------

func TestMainMenuItemsCount(t *testing.T) {
	// Sanity check that catches an edit to mainMenuItems() that forgets a
	// category, not a specific magic number: the main menu dispatch
	// switch in main() has one case per item except the last (Exit, the
	// default case), so this is "at least a couple of real entries plus
	// the always-present Exit", not an exact hardcoded count.
	items := mainMenuItems()
	if len(items) < 2 {
		t.Fatalf("mainMenuItems() returned %d items, want at least 2", len(items))
	}
	if items[len(items)-1].title != "Exit Switcher Suite" {
		t.Errorf("mainMenuItems() last item = %q, want the Exit entry last", items[len(items)-1].title)
	}
}

func TestMenuMoveUp(t *testing.T) {
	cases := []struct {
		name          string
		selected, cnt int
		want          int
	}{
		{"middle of a 10-item menu", 5, 10, 4},
		{"wraps from 0 to the last index", 0, 10, 9},
		{"single-item menu wraps to itself", 0, 1, 0},
		{"zero count never panics or goes negative", 0, 0, 0},
	}
	for _, c := range cases {
		if got := menuMoveUp(c.selected, c.cnt); got != c.want {
			t.Errorf("%s: menuMoveUp(%d, %d) = %d, want %d", c.name, c.selected, c.cnt, got, c.want)
		}
	}
}

func TestMenuMoveDown(t *testing.T) {
	cases := []struct {
		name          string
		selected, cnt int
		want          int
	}{
		{"middle of a 10-item menu", 5, 10, 6},
		{"wraps from the last index to 0", 9, 10, 0},
		{"single-item menu wraps to itself", 0, 1, 0},
		{"zero count never panics or divides by zero", 0, 0, 0},
	}
	for _, c := range cases {
		if got := menuMoveDown(c.selected, c.cnt); got != c.want {
			t.Errorf("%s: menuMoveDown(%d, %d) = %d, want %d", c.name, c.selected, c.cnt, got, c.want)
		}
	}
}

// TestSelectMainMenuWraparoundMatchesItemCount is the specific regression
// test for this fix: selectMainMenu must derive its wraparound bounds
// from len(mainMenuItems()), not a number that has to be remembered and
// hand-updated every time a menu entry is added or removed.
func TestSelectMainMenuWraparoundMatchesItemCount(t *testing.T) {
	itemCount := len(mainMenuItems())
	exitIndex := itemCount - 1

	if got := menuMoveUp(0, itemCount); got != exitIndex {
		t.Errorf("menuMoveUp(0, %d) = %d, want %d (pressing Up from the first item must land on Exit, the last item)", itemCount, got, exitIndex)
	}
	if got := menuMoveDown(exitIndex, itemCount); got != 0 {
		t.Errorf("menuMoveDown(%d, %d) = %d, want 0 (pressing Down from Exit must wrap back to the first item)", exitIndex, itemCount, got)
	}
}

// ---------------------------------------------------------------------------
// SSH Server firewall port validation and rule-ownership record (N11)
// ---------------------------------------------------------------------------

func TestSSHPortOrFallback(t *testing.T) {
	cases := []struct {
		name         string
		port         int
		fallback     int
		wantPort     int
		wantFallback bool
	}{
		{"valid standard port", 22, 22, 22, false},
		{"valid custom port", 2222, 22, 2222, false},
		{"lowest valid port", 1, 22, 1, false},
		{"highest valid port", 65535, 22, 65535, false},
		{"zero (not detected) falls back", 0, 22, 22, true},
		{"negative falls back", -1, 22, 22, true},
		{"over 65535 falls back", 65536, 22, 22, true},
		{"falls back to whatever fallback the caller passed, not a hardcoded 22", 0, 2200, 2200, true},
	}
	for _, c := range cases {
		port, usedFallback := sshPortOrFallback(c.port, c.fallback)
		if port != c.wantPort || usedFallback != c.wantFallback {
			t.Errorf("%s: sshPortOrFallback(%d, %d) = (%d, %v), want (%d, %v)", c.name, c.port, c.fallback, port, usedFallback, c.wantPort, c.wantFallback)
		}
	}
}

func TestSSHFirewallRuleRecordRoundTrip(t *testing.T) {
	withTempCWD(t)

	if _, _, ok := getSSHFirewallRuleRecord(); ok {
		t.Error("getSSHFirewallRuleRecord() with no record = ok, want not ok")
	}

	saveSSHFirewallRuleRecord("ufw", 2222)
	tool, port, ok := getSSHFirewallRuleRecord()
	if !ok || tool != "ufw" || port != 2222 {
		t.Errorf(`getSSHFirewallRuleRecord() = (%q, %d, %v), want ("ufw", 2222, true)`, tool, port, ok)
	}

	removeSSHFirewallRuleRecord()
	if _, _, ok := getSSHFirewallRuleRecord(); ok {
		t.Error("getSSHFirewallRuleRecord() after remove = ok, want not ok")
	}
}

func TestSSHFirewallRuleRecordRejectsCorruptContent(t *testing.T) {
	withTempCWD(t)

	cases := []string{
		"",
		"ufw",                // missing port
		"ufw not-a-number",   // non-numeric port
		"ufw 0",              // out of range (too low)
		"ufw 65536",          // out of range (too high)
		"ufw 22 extra-field", // too many fields
	}
	for _, content := range cases {
		if err := os.WriteFile(sshFirewallRuleRecordFile, []byte(content), 0644); err != nil {
			t.Fatalf("test setup: could not write record file: %v", err)
		}
		if _, _, ok := getSSHFirewallRuleRecord(); ok {
			t.Errorf("getSSHFirewallRuleRecord() with content %q = ok, want not ok (corrupt/foreign record must never be acted on)", content)
		}
	}
}
