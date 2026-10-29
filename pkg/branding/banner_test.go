package branding

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStripANSIRemovesColorSequences verifies the hand-rolled scanner in
// stripANSI handles single colors, multi-component sequences (`\033[1;36m`),
// and 256-color codes (`\033[38;5;214m`) -- the three forms the banner uses.
// Plain text must survive untouched.
func TestStripANSIRemovesColorSequences(t *testing.T) {
	cases := map[string]string{
		"plain text":                       "plain text",
		"\033[31mred\033[0m":               "red",
		"\033[1;36mbold cyan\033[0m":       "bold cyan",
		"\033[1;38;5;214mamber\033[0m":     "amber",
		"a\033[33mb\033[37mc\033[0md":      "abcd",
		"reset only \033[0m here":          "reset only  here",
		"\033[1;36m\033[37mlayered\033[0m": "layered",
	}
	for in, want := range cases {
		if got := stripANSI(in); got != want {
			t.Errorf("stripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestStripANSIPreservesESCNotFollowedByBracket guards against the scanner
// eating bare ESC bytes that are not part of a CSI sequence -- only `ESC [
// ... m` should be consumed.
func TestStripANSIPreservesESCNotFollowedByBracket(t *testing.T) {
	in := "before\x1bafter"
	got := stripANSI(in)
	if got != in {
		t.Errorf("stripANSI(%q) = %q, want unchanged", in, got)
	}
}

// TestBannerContainsExpectedTokens is a sanity check that the banner
// constant still spells out the brand once ANSI codes are removed. Cheap
// guard against accidental edits that mangle the ASCII art payload.
func TestBannerContainsExpectedTokens(t *testing.T) {
	plain := stripANSI(Banner)
	for _, want := range []string{
		"████████", // T cross-bar from "TELE"
		"load testing for telegram bots",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("Banner missing %q", want)
		}
	}
}

// TestPrintBannerStripsForNonTTY confirms PrintBanner removes ANSI when
// writing to a non-terminal (here, a bytes.Buffer). Keeps log files
// readable without ESC noise.
func TestPrintBannerStripsForNonTTY(t *testing.T) {
	var buf bytes.Buffer
	PrintBanner(&buf)
	if buf.Len() == 0 {
		t.Fatal("PrintBanner wrote nothing")
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("PrintBanner left ANSI escapes in non-TTY output:\n%s", buf.String())
	}
}

// TestPrintBannerKeepsColorsForCharDevice exercises the TTY branch of
// isTerminal: /dev/null is a character device, so PrintBanner must treat
// it like a terminal and write the colored banner unchanged.
func TestPrintBannerKeepsColorsForCharDevice(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer func() { _ = f.Close() }()

	if !isTerminal(f) {
		t.Errorf("isTerminal(%s) = false, want true for a character device", os.DevNull)
	}
	PrintBanner(f)
}

// TestIsTerminalRejectsNonTTYFiles covers the *os.File paths that must
// fall back to plain output: a regular file (redirected log) and a file
// whose Stat fails (already closed).
func TestIsTerminalRejectsNonTTYFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "banner.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if isTerminal(f) {
		t.Error("isTerminal(regular file) = true")
	}

	PrintBanner(f)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != stripANSI(Banner) {
		t.Error("PrintBanner to a regular file did not write the plain banner")
	}

	if isTerminal(f) {
		t.Error("isTerminal(closed file) = true")
	}
}

// TestPrintBannerToTTY checks that info is always delivered. With a
// controlling terminal both go to /dev/tty and fallback stays empty;
// without one (CI) the plain banner and info land in fallback.
func TestPrintBannerToTTY(t *testing.T) {
	var buf bytes.Buffer
	PrintBannerToTTY(&buf, "listening on :5678\n")

	if buf.Len() == 0 {
		return // written to /dev/tty
	}
	out := buf.String()
	if !strings.Contains(out, "load testing for telegram bots") || !strings.HasSuffix(out, "listening on :5678\n") {
		t.Errorf("fallback output = %q, want plain banner followed by info", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("fallback output contains ANSI escapes")
	}
}
