package releasepack

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type RegistryOptions struct {
	Path       string
	PluginID   string
	Version    string
	Repository string
}

type registryPlugin struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	Repository string `json:"repository"`
	Homepage   string `json:"homepage"`
}

func VerifyRegistry(options RegistryOptions) error {
	options.Path = strings.TrimSpace(options.Path)
	options.PluginID = strings.TrimSpace(options.PluginID)
	options.Version = strings.TrimSpace(options.Version)
	options.Repository = strings.TrimSpace(options.Repository)
	if !pluginIDPattern.MatchString(options.PluginID) {
		return fmt.Errorf("invalid plugin id %q", options.PluginID)
	}
	if !versionPattern.MatchString(options.Version) || strings.HasPrefix(strings.ToLower(options.Version), "v") {
		return fmt.Errorf("invalid plugin version %q", options.Version)
	}

	data, errRead := os.ReadFile(options.Path)
	if errRead != nil {
		return fmt.Errorf("read registry: %w", errRead)
	}
	var registry struct {
		Plugins []registryPlugin `json:"plugins"`
	}
	if errDecode := json.Unmarshal(data, &registry); errDecode != nil {
		return fmt.Errorf("decode registry: %w", errDecode)
	}
	var matches []registryPlugin
	for _, plugin := range registry.Plugins {
		if strings.TrimSpace(plugin.ID) == options.PluginID {
			matches = append(matches, plugin)
		}
	}
	if len(matches) != 1 {
		return fmt.Errorf("registry must contain exactly one %s entry, found %d", options.PluginID, len(matches))
	}
	plugin := matches[0]
	if strings.TrimSpace(plugin.Version) != options.Version {
		return fmt.Errorf("registry version = %q, want %q", plugin.Version, options.Version)
	}
	if strings.TrimSpace(plugin.Repository) != options.Repository {
		return fmt.Errorf("registry repository = %q, want %q", plugin.Repository, options.Repository)
	}
	if strings.TrimSpace(plugin.Homepage) != options.Repository {
		return fmt.Errorf("registry homepage = %q, want %q", plugin.Homepage, options.Repository)
	}
	return nil
}
