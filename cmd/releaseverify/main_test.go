package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReportsVerificationFailure(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run([]string{
		"-version", "0.3.1362-1",
		"-goos", "linux",
		"-goarch", "amd64",
		"-repository", "https://github.com/karlorz/cpa-account-config-manager",
	}, &bytes.Buffer{}, &stderr)
	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "archive filename") {
		t.Fatalf("run() stderr = %q, want archive verification error", stderr.String())
	}
}

func TestRunVerifiesRegistryWithoutArchive(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	registry := `{"plugins":[{"id":"cpa-account-config-manager","version":"0.3.1362-1","repository":"https://github.com/karlorz/cpa-account-config-manager","homepage":"https://github.com/karlorz/cpa-account-config-manager"}]}`
	if errWrite := os.WriteFile(registryPath, []byte(registry), 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{
		"-version", "0.3.1362-1",
		"-goos", "linux",
		"-goarch", "amd64",
		"-repository", "https://github.com/karlorz/cpa-account-config-manager",
		"-registry", registryPath,
	}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("run() exit code = %d stderr=%q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), registryPath) {
		t.Fatalf("run() stdout = %q, want verified registry path", stdout.String())
	}
}

func TestRunRejectsInvalidRegistryBeforeArchiveVerification(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	registry := `{"plugins":[{"id":"cpa-account-config-manager","version":"0.3.1362-0","repository":"https://github.com/karlorz/cpa-account-config-manager","homepage":"https://github.com/karlorz/cpa-account-config-manager"}]}`
	if errWrite := os.WriteFile(registryPath, []byte(registry), 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	var stderr bytes.Buffer
	exitCode := run([]string{
		"-version", "0.3.1362-1",
		"-goos", "linux",
		"-goarch", "amd64",
		"-repository", "https://github.com/karlorz/cpa-account-config-manager",
		"-registry", registryPath,
		"-archive", "missing.zip",
		"-checksum", "missing.zip.sha256",
	}, &bytes.Buffer{}, &stderr)
	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "registry version") {
		t.Fatalf("run() stderr = %q, want registry failure before archive failure", stderr.String())
	}
}

func TestRunContinuesFromValidRegistryToArchiveVerification(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	registry := `{"plugins":[{"id":"cpa-account-config-manager","version":"0.3.1362-1","repository":"https://github.com/karlorz/cpa-account-config-manager","homepage":"https://github.com/karlorz/cpa-account-config-manager"}]}`
	if errWrite := os.WriteFile(registryPath, []byte(registry), 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	var stderr bytes.Buffer
	exitCode := run([]string{
		"-version", "0.3.1362-1",
		"-goos", "linux",
		"-goarch", "amd64",
		"-repository", "https://github.com/karlorz/cpa-account-config-manager",
		"-registry", registryPath,
		"-archive", "missing.zip",
		"-checksum", "missing.zip.sha256",
	}, &bytes.Buffer{}, &stderr)
	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "archive filename") {
		t.Fatalf("run() stderr = %q, want archive verification after registry success", stderr.String())
	}
}
