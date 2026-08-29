package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"cpa-account-config-manager/internal/releasepack"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("releaseverify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	pluginID := flags.String("id", "cpa-account-config-manager", "plugin ID")
	version := flags.String("version", "", "release version without a leading v")
	goos := flags.String("goos", "", "target GOOS")
	goarch := flags.String("goarch", "", "target GOARCH")
	repository := flags.String("repository", "", "expected linker-injected plugin repository")
	registry := flags.String("registry", "", "path to the plugin registry JSON")
	archive := flags.String("archive", "", "path to the release ZIP")
	checksum := flags.String("checksum", "", "path to the ZIP checksum sidecar")
	if errParse := flags.Parse(args); errParse != nil {
		return 2
	}
	if *registry != "" {
		errRegistry := releasepack.VerifyRegistry(releasepack.RegistryOptions{
			Path: *registry, PluginID: *pluginID, Version: *version, Repository: *repository,
		})
		if errRegistry != nil {
			fmt.Fprintln(stderr, errRegistry)
			return 1
		}
		fmt.Fprintln(stdout, *registry)
		if *archive == "" && *checksum == "" {
			return 0
		}
	}
	errVerify := releasepack.Verify(releasepack.VerifyOptions{
		PluginID:   *pluginID,
		Version:    *version,
		GOOS:       *goos,
		GOARCH:     *goarch,
		Repository: *repository,
		Archive:    *archive,
		Checksum:   *checksum,
	})
	if errVerify != nil {
		fmt.Fprintln(stderr, errVerify)
		return 1
	}
	fmt.Fprintln(stdout, *archive)
	return 0
}
