package releasepack

import (
	"archive/zip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type VerifyOptions struct {
	PluginID   string
	Version    string
	GOOS       string
	GOARCH     string
	Repository string
	Archive    string
	Checksum   string
}

func Verify(options VerifyOptions) error {
	options.PluginID = strings.TrimSpace(options.PluginID)
	options.Version = strings.TrimSpace(options.Version)
	options.GOOS = strings.TrimSpace(options.GOOS)
	options.GOARCH = strings.TrimSpace(options.GOARCH)
	options.Repository = strings.TrimSpace(options.Repository)
	options.Archive = strings.TrimSpace(options.Archive)
	options.Checksum = strings.TrimSpace(options.Checksum)

	if !pluginIDPattern.MatchString(options.PluginID) {
		return fmt.Errorf("invalid plugin id %q", options.PluginID)
	}
	if !versionPattern.MatchString(options.Version) || strings.HasPrefix(strings.ToLower(options.Version), "v") {
		return fmt.Errorf("invalid plugin version %q", options.Version)
	}
	if !goarchPattern.MatchString(options.GOARCH) {
		return fmt.Errorf("invalid GOARCH %q", options.GOARCH)
	}
	extension, errExtension := pluginExtension(options.GOOS)
	if errExtension != nil {
		return errExtension
	}
	expectedArchive := fmt.Sprintf("%s_%s_%s_%s.zip", options.PluginID, options.Version, options.GOOS, options.GOARCH)
	if filepath.Base(options.Archive) != expectedArchive {
		return fmt.Errorf("archive filename must be %s", expectedArchive)
	}
	if filepath.Base(options.Checksum) != expectedArchive+".sha256" {
		return fmt.Errorf("checksum filename must be %s.sha256", expectedArchive)
	}

	archiveData, errReadArchive := os.ReadFile(options.Archive)
	if errReadArchive != nil {
		return fmt.Errorf("read archive: %w", errReadArchive)
	}
	digest := sha256.Sum256(archiveData)
	wantDigest := hex.EncodeToString(digest[:])
	checksumData, errReadChecksum := os.ReadFile(options.Checksum)
	if errReadChecksum != nil {
		return fmt.Errorf("read checksum: %w", errReadChecksum)
	}
	checksumFields := strings.Fields(string(checksumData))
	if len(checksumFields) != 2 || checksumFields[0] != wantDigest || checksumFields[1] != expectedArchive {
		return fmt.Errorf("checksum does not match archive %s", expectedArchive)
	}

	archive, errOpen := zip.OpenReader(options.Archive)
	if errOpen != nil {
		return fmt.Errorf("open archive: %w", errOpen)
	}
	defer func() { _ = archive.Close() }()
	if len(archive.File) != 1 {
		return fmt.Errorf("archive must contain exactly one inner library, found %d entries", len(archive.File))
	}
	expectedLibrary := fmt.Sprintf("%s-v%s%s", options.PluginID, options.Version, extension)
	library := archive.File[0]
	if library.Name != expectedLibrary {
		return fmt.Errorf("inner library must be %s, found %s", expectedLibrary, library.Name)
	}
	if library.Mode().Perm() != 0o755 {
		return fmt.Errorf("inner library mode must be 0755, found %04o", library.Mode().Perm())
	}
	handle, errEntry := library.Open()
	if errEntry != nil {
		return fmt.Errorf("open inner library: %w", errEntry)
	}
	tempDir, errTemp := os.MkdirTemp("", "releaseverify-")
	if errTemp != nil {
		_ = handle.Close()
		return fmt.Errorf("create verification directory: %w", errTemp)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	libraryPath := filepath.Join(tempDir, filepath.Base(expectedLibrary))
	extracted, errCreate := os.OpenFile(libraryPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755)
	if errCreate != nil {
		_ = handle.Close()
		return fmt.Errorf("create extracted library: %w", errCreate)
	}
	_, errCopy := io.Copy(extracted, handle)
	errCloseExtracted := extracted.Close()
	errCloseHandle := handle.Close()
	if errCopy != nil {
		return fmt.Errorf("extract inner library: %w", errCopy)
	}
	if errCloseExtracted != nil {
		return fmt.Errorf("close extracted library: %w", errCloseExtracted)
	}
	if errCloseHandle != nil {
		return fmt.Errorf("close inner library: %w", errCloseHandle)
	}

	info, errBuildInfo := buildinfo.ReadFile(libraryPath)
	if errBuildInfo != nil {
		return fmt.Errorf("read inner library build info: %w", errBuildInfo)
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != options.GOOS {
		return fmt.Errorf("inner library GOOS = %q, want %q", settings["GOOS"], options.GOOS)
	}
	if settings["GOARCH"] != options.GOARCH {
		return fmt.Errorf("inner library GOARCH = %q, want %q", settings["GOARCH"], options.GOARCH)
	}
	ldflags := settings["-ldflags"]
	pluginVersion, hasPluginVersion := linkerAssignmentValue(ldflags, "PluginVersion")
	if !hasPluginVersion || pluginVersion != options.Version {
		return fmt.Errorf("inner library PluginVersion = %q, want %q", pluginVersion, options.Version)
	}
	pluginRepository, hasPluginRepository := linkerAssignmentValue(ldflags, "PluginRepository")
	if options.Repository != "" && (!hasPluginRepository || pluginRepository != options.Repository) {
		return fmt.Errorf("inner library PluginRepository = %q, want %q", pluginRepository, options.Repository)
	}
	return nil
}

func linkerAssignmentValue(ldflags, symbol string) (string, bool) {
	fields := strings.Fields(ldflags)
	value := ""
	found := false
	for index := 0; index+1 < len(fields); index++ {
		if fields[index] != "-X" {
			continue
		}
		assignment := fields[index+1]
		separator := strings.IndexByte(assignment, '=')
		if separator <= 0 || !strings.HasSuffix(assignment[:separator], "."+symbol) {
			continue
		}
		value = assignment[separator+1:]
		found = true
	}
	return value, found
}
