package releasepack

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	verifyTestPluginID   = "cpa-account-config-manager"
	verifyTestVersion    = "0.3.1362-1"
	verifyTestRepository = "https://github.com/karlorz/cpa-account-config-manager"
)

func TestVerifyReleaseArchiveAcceptsMatchingArtifact(t *testing.T) {
	fixtureOptions := defaultReleaseVerificationFixtureOptions(t)
	fixture := writeReleaseVerificationFixture(t, fixtureOptions)
	errVerify := Verify(VerifyOptions{
		PluginID:   verifyTestPluginID,
		Version:    verifyTestVersion,
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		Repository: verifyTestRepository,
		Archive:    fixture.archive,
		Checksum:   fixture.checksum,
	})
	if errVerify != nil {
		t.Fatalf("Verify() error = %v", errVerify)
	}
}

func TestVerifyReleaseArchiveRejectsChecksumMismatch(t *testing.T) {
	fixtureOptions := defaultReleaseVerificationFixtureOptions(t)
	fixture := writeReleaseVerificationFixture(t, fixtureOptions)
	if errWrite := os.WriteFile(fixture.checksum, []byte(strings.Repeat("0", 64)+"  "+filepath.Base(fixture.archive)+"\n"), 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	errVerify := Verify(VerifyOptions{
		PluginID: verifyTestPluginID, Version: verifyTestVersion, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Repository: verifyTestRepository, Archive: fixture.archive, Checksum: fixture.checksum,
	})
	if errVerify == nil || !strings.Contains(errVerify.Error(), "checksum") {
		t.Fatalf("Verify() error = %v, want checksum rejection", errVerify)
	}
}

func TestVerifyReleaseArchiveRejectsUnexpectedInnerLibrary(t *testing.T) {
	fixtureOptions := defaultReleaseVerificationFixtureOptions(t)
	fixtureOptions.innerName = "wrong-plugin-name.bin"
	fixture := writeReleaseVerificationFixture(t, fixtureOptions)
	errVerify := Verify(VerifyOptions{
		PluginID: verifyTestPluginID, Version: verifyTestVersion, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Repository: verifyTestRepository, Archive: fixture.archive, Checksum: fixture.checksum,
	})
	if errVerify == nil || !strings.Contains(errVerify.Error(), "inner library") {
		t.Fatalf("Verify() error = %v, want inner-library rejection", errVerify)
	}
}

func TestVerifyReleaseArchiveRejectsMismatchedLinkerVersion(t *testing.T) {
	fixtureOptions := defaultReleaseVerificationFixtureOptions(t)
	fixtureOptions.linkerVersion = "0.3.1362-0"
	fixture := writeReleaseVerificationFixture(t, fixtureOptions)
	errVerify := Verify(VerifyOptions{
		PluginID: verifyTestPluginID, Version: verifyTestVersion, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Repository: verifyTestRepository, Archive: fixture.archive, Checksum: fixture.checksum,
	})
	if errVerify == nil || !strings.Contains(errVerify.Error(), "PluginVersion") {
		t.Fatalf("Verify() error = %v, want linker-version rejection", errVerify)
	}
}

func TestVerifyReleaseArchiveRejectsLinkerVersionWithExpectedPrefix(t *testing.T) {
	fixtureOptions := defaultReleaseVerificationFixtureOptions(t)
	fixtureOptions.linkerVersion = "0.3.1362-10"
	fixture := writeReleaseVerificationFixture(t, fixtureOptions)
	errVerify := Verify(VerifyOptions{
		PluginID: verifyTestPluginID, Version: verifyTestVersion, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Repository: verifyTestRepository, Archive: fixture.archive, Checksum: fixture.checksum,
	})
	if errVerify == nil || !strings.Contains(errVerify.Error(), "PluginVersion") {
		t.Fatalf("Verify() error = %v, want exact linker-version rejection", errVerify)
	}
}

func TestVerifyReleaseArchiveRejectsMultipleEntries(t *testing.T) {
	fixtureOptions := defaultReleaseVerificationFixtureOptions(t)
	fixtureOptions.extraEntry = true
	fixture := writeReleaseVerificationFixture(t, fixtureOptions)
	errVerify := Verify(VerifyOptions{
		PluginID: verifyTestPluginID, Version: verifyTestVersion, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Repository: verifyTestRepository, Archive: fixture.archive, Checksum: fixture.checksum,
	})
	if errVerify == nil || !strings.Contains(errVerify.Error(), "exactly one") {
		t.Fatalf("Verify() error = %v, want multiple-entry rejection", errVerify)
	}
}

func TestVerifyReleaseArchiveRejectsNonExecutableInnerLibrary(t *testing.T) {
	fixtureOptions := defaultReleaseVerificationFixtureOptions(t)
	fixtureOptions.mode = 0o644
	fixture := writeReleaseVerificationFixture(t, fixtureOptions)
	errVerify := Verify(VerifyOptions{
		PluginID: verifyTestPluginID, Version: verifyTestVersion, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Repository: verifyTestRepository, Archive: fixture.archive, Checksum: fixture.checksum,
	})
	if errVerify == nil || !strings.Contains(errVerify.Error(), "mode") {
		t.Fatalf("Verify() error = %v, want mode rejection", errVerify)
	}
}

func TestVerifyReleaseArchiveRejectsMismatchedTargetArchitecture(t *testing.T) {
	fixtureOptions := defaultReleaseVerificationFixtureOptions(t)
	if runtime.GOARCH == "amd64" {
		fixtureOptions.goarch = "arm64"
	} else {
		fixtureOptions.goarch = "amd64"
	}
	fixture := writeReleaseVerificationFixture(t, fixtureOptions)
	errVerify := Verify(VerifyOptions{
		PluginID: verifyTestPluginID, Version: verifyTestVersion, GOOS: runtime.GOOS, GOARCH: fixtureOptions.goarch,
		Repository: verifyTestRepository, Archive: fixture.archive, Checksum: fixture.checksum,
	})
	if errVerify == nil || !strings.Contains(errVerify.Error(), "GOARCH") {
		t.Fatalf("Verify() error = %v, want target-architecture rejection", errVerify)
	}
}

func TestVerifyReleaseArchiveRejectsMismatchedRepository(t *testing.T) {
	fixtureOptions := defaultReleaseVerificationFixtureOptions(t)
	fixtureOptions.linkerRepository = "https://github.com/example/wrong-fork"
	fixture := writeReleaseVerificationFixture(t, fixtureOptions)
	errVerify := Verify(VerifyOptions{
		PluginID: verifyTestPluginID, Version: verifyTestVersion, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Repository: verifyTestRepository, Archive: fixture.archive, Checksum: fixture.checksum,
	})
	if errVerify == nil || !strings.Contains(errVerify.Error(), "PluginRepository") {
		t.Fatalf("Verify() error = %v, want repository rejection", errVerify)
	}
}

func TestVerifyReleaseArchiveRejectsRepositoryWithExpectedPrefix(t *testing.T) {
	fixtureOptions := defaultReleaseVerificationFixtureOptions(t)
	fixtureOptions.linkerRepository = verifyTestRepository + ".evil"
	fixture := writeReleaseVerificationFixture(t, fixtureOptions)
	errVerify := Verify(VerifyOptions{
		PluginID: verifyTestPluginID, Version: verifyTestVersion, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Repository: verifyTestRepository, Archive: fixture.archive, Checksum: fixture.checksum,
	})
	if errVerify == nil || !strings.Contains(errVerify.Error(), "PluginRepository") {
		t.Fatalf("Verify() error = %v, want exact repository rejection", errVerify)
	}
}

type releaseVerificationFixture struct {
	archive  string
	checksum string
}

type releaseVerificationFixtureOptions struct {
	linkerVersion    string
	linkerRepository string
	innerName        string
	goos             string
	goarch           string
	mode             os.FileMode
	extraEntry       bool
}

func defaultReleaseVerificationFixtureOptions(t *testing.T) releaseVerificationFixtureOptions {
	t.Helper()
	return releaseVerificationFixtureOptions{
		linkerVersion:    verifyTestVersion,
		linkerRepository: verifyTestRepository,
		innerName:        expectedVerifyTestLibraryName(t),
		goos:             runtime.GOOS,
		goarch:           runtime.GOARCH,
		mode:             0o755,
	}
}

func writeReleaseVerificationFixture(t *testing.T, options releaseVerificationFixtureOptions) releaseVerificationFixture {
	t.Helper()
	tempDir := t.TempDir()
	moduleDir := filepath.Join(tempDir, "fixture")
	if errMkdir := os.MkdirAll(moduleDir, 0o755); errMkdir != nil {
		t.Fatal(errMkdir)
	}
	if errWrite := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte("module example.com/releaseverifyfixture\n\ngo 1.24\n"), 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	source := "package main\n\nvar PluginVersion = \"dev\"\nvar PluginRepository = \"\"\n\nfunc main() {}\n"
	if errWrite := os.WriteFile(filepath.Join(moduleDir, "main.go"), []byte(source), 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	binaryPath := filepath.Join(tempDir, "fixture-binary")
	ldflags := fmt.Sprintf("-X main.PluginVersion=%s -X main.PluginRepository=%s", options.linkerVersion, options.linkerRepository)
	command := exec.Command("go", "build", "-buildvcs=false", "-ldflags", ldflags, "-o", binaryPath, ".")
	command.Dir = moduleDir
	if output, errBuild := command.CombinedOutput(); errBuild != nil {
		t.Fatalf("build fixture: %v\n%s", errBuild, output)
	}
	binaryData, errRead := os.ReadFile(binaryPath)
	if errRead != nil {
		t.Fatal(errRead)
	}

	archiveName := fmt.Sprintf("%s_%s_%s_%s.zip", verifyTestPluginID, verifyTestVersion, options.goos, options.goarch)
	archivePath := filepath.Join(tempDir, archiveName)
	archiveFile, errCreate := os.Create(archivePath)
	if errCreate != nil {
		t.Fatal(errCreate)
	}
	writer := zip.NewWriter(archiveFile)
	header := &zip.FileHeader{Name: options.innerName, Method: zip.Deflate}
	header.SetMode(options.mode)
	entry, errEntry := writer.CreateHeader(header)
	if errEntry == nil {
		_, errEntry = entry.Write(binaryData)
	}
	if options.extraEntry && errEntry == nil {
		_, errEntry = writer.Create("unexpected.txt")
	}
	if errClose := writer.Close(); errEntry == nil {
		errEntry = errClose
	}
	if errClose := archiveFile.Close(); errEntry == nil {
		errEntry = errClose
	}
	if errEntry != nil {
		t.Fatalf("write fixture archive: %v", errEntry)
	}
	archiveData, errReadArchive := os.ReadFile(archivePath)
	if errReadArchive != nil {
		t.Fatal(errReadArchive)
	}
	digest := sha256.Sum256(archiveData)
	checksumPath := archivePath + ".sha256"
	checksumLine := hex.EncodeToString(digest[:]) + "  " + archiveName + "\n"
	if errWrite := os.WriteFile(checksumPath, []byte(checksumLine), 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	return releaseVerificationFixture{archive: archivePath, checksum: checksumPath}
}

func expectedVerifyTestLibraryName(t *testing.T) string {
	t.Helper()
	var extension string
	switch runtime.GOOS {
	case "linux":
		extension = ".so"
	case "darwin":
		extension = ".dylib"
	case "windows":
		extension = ".dll"
	default:
		t.Fatalf("unsupported test GOOS %q", runtime.GOOS)
	}
	return verifyTestPluginID + "-v" + verifyTestVersion + extension
}
