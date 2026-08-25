package cli

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/deploy"
)

func TestAnsiColorDisabled(t *testing.T) {
	got := ansiColor(false, 31, "boom")
	if got != "boom" {
		t.Errorf("ansiColor(false) = %q, want %q", got, "boom")
	}
}

func TestAnsiColorEnabled(t *testing.T) {
	got := ansiColor(true, 31, "boom")
	want := "\x1b[31mboom\x1b[0m"
	if got != want {
		t.Errorf("ansiColor(true, 31, ...) = %q, want %q", got, want)
	}
}

func TestIsTerminalBuffer(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) {
		t.Errorf("IsTerminal(bytes.Buffer) = true, want false")
	}
}

func TestPrintError_Config_Plain(t *testing.T) {
	w := &bytes.Buffer{}
	chain := fmt.Errorf("project.name is required: %w", &ExitError{Code: ExitGeneral, Kind: KindConfig, Err: errors.New("invalid pier.toml")})
	PrintError(w, chain, false, false)
	got := w.String()
	wantLines := []string{
		"error[config]: project.name is required",
		"  |",
		"  |-> caused by: invalid pier.toml",
		"  |",
		"= hint: see docs/superpowers/specs/2026-07-26-pier-design.md#configuration or run 'cat pier.toml'",
	}
	for _, line := range wantLines {
		if !strings.Contains(got, line) {
			t.Errorf("output missing line %q\nfull output:\n%s", line, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("plain output contains ANSI escape: %q", got)
	}
}

func TestPrintError_Docker_Color(t *testing.T) {
	w := &bytes.Buffer{}
	err := DockerError(errors.New("compose up failed"))
	PrintError(w, err, false, true)
	got := w.String()
	if !strings.Contains(got, "\x1b[31m") {
		t.Errorf("color output missing red ANSI code: %q", got)
	}
	if !strings.Contains(got, "error[docker]") {
		t.Errorf("color output missing [docker] label: %q", got)
	}
}

func TestPrintError_Unknown_NoBracket(t *testing.T) {
	w := &bytes.Buffer{}
	PrintError(w, errors.New("something exploded"), false, false)
	got := w.String()
	if strings.Contains(got, "[") {
		t.Errorf("unknown error should not have bracket label: %q", got)
	}
	if !strings.HasPrefix(got, "error: something exploded\n") {
		t.Errorf("unknown error should start with 'error: something exploded': %q", got)
	}
}

func TestPrintError_Chain_ThreeLevels(t *testing.T) {
	w := &bytes.Buffer{}
	l3 := errors.New("file not found")
	l2 := fmt.Errorf("read pier.toml: %w", l3)
	l1 := ConfigError(l2)
	PrintError(w, l1, false, false)
	got := w.String()
	if !strings.Contains(got, "error[config]: read pier.toml") {
		t.Errorf("missing top label: %q", got)
	}
	if !strings.Contains(got, "caused by: file not found") {
		t.Errorf("missing deep cause: %q", got)
	}
}

func TestPrintError_User_NoHint(t *testing.T) {
	w := &bytes.Buffer{}
	PrintError(w, UserError(errors.New("missing arg")), false, false)
	got := w.String()
	if strings.Contains(got, "= hint:") {
		t.Errorf("user error should not show hint: %q", got)
	}
}

func TestPrintError_Verbose_ShowsDuplicates(t *testing.T) {
	w := &bytes.Buffer{}
	dup := errors.New("same")
	chain := fmt.Errorf("same: %w", dup)
	PrintError(w, chain, true, false)
	got := w.String()
	if !strings.Contains(got, "same") {
		t.Errorf("verbose should show duplicate line: %q", got)
	}
}

func TestPrintError_RemoteDiskFullHint(t *testing.T) {
	w := &bytes.Buffer{}
	base := fmt.Errorf("failed to update builder last activity time: write /home/host/.docker/buildx/activity/.tmp-default3263565124: no space left on device")
	err := deploy.RemoteBuildError("prod.example.com", base)
	PrintError(w, err, false, false)
	got := w.String()
	want := "host prod.example.com is out of disk space: ssh in and run 'docker builder prune -af', then check 'docker system df'"
	if !strings.Contains(got, want) {
		t.Errorf("output missing disk-full hint %q\nfull output:\n%s", want, got)
	}
	if strings.Contains(got, "pier dev") {
		t.Errorf("disk-full error must not show local pier hint:\n%s", got)
	}
}

func TestPrintError_RemoteGenericHint(t *testing.T) {
	w := &bytes.Buffer{}
	err := deploy.RemoteUpError("prod.example.com", errors.New("compose up failed"))
	PrintError(w, err, false, false)
	got := w.String()
	want := "command failed on prod.example.com: ssh in and run 'docker compose ps' / 'docker system df' to inspect"
	if !strings.Contains(got, want) {
		t.Errorf("output missing remote hint %q\nfull output:\n%s", want, got)
	}
	if strings.Contains(got, "pier dev") {
		t.Errorf("remote error must not show local pier hint:\n%s", got)
	}
}

func TestPrintError_RemoteDiskFullDeepNeedle(t *testing.T) {
	w := &bytes.Buffer{}
	base := fmt.Errorf("write /var/lib/docker: no space left on device")
	err := fmt.Errorf("remote build failed: %w", deploy.RemoteBuildError("prod.example.com", base))
	PrintError(w, err, false, false)
	got := w.String()
	want := "host prod.example.com is out of disk space: ssh in and run 'docker builder prune -af', then check 'docker system df'"
	if !strings.Contains(got, want) {
		t.Errorf("output missing disk-full hint %q for needle below ExitError wrap\nfull output:\n%s", want, got)
	}
}

func TestPrintError_RemoteDiskFullWithoutExitError(t *testing.T) {
	w := &bytes.Buffer{}
	PrintError(w, fmt.Errorf("write /var/lib/docker: no space left on device"), false, false)
	got := w.String()
	want := "this host is out of disk space: run 'docker builder prune -af', then check 'docker system df'"
	if !strings.Contains(got, want) {
		t.Errorf("plain disk-full error missing local hint %q\nfull output:\n%s", want, got)
	}
	if strings.Contains(got, "ssh in") {
		t.Errorf("local disk-full error must not tell the user to ssh in:\n%s", got)
	}
}
