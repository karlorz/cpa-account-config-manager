package releasepack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyRegistryAcceptsMatchingForkEntry(t *testing.T) {
	registryPath := writeRegistryFixture(t, "0.3.1362-1", "https://github.com/karlorz/cpa-account-config-manager")
	errVerify := VerifyRegistry(RegistryOptions{
		Path:       registryPath,
		PluginID:   "cpa-account-config-manager",
		Version:    "0.3.1362-1",
		Repository: "https://github.com/karlorz/cpa-account-config-manager",
	})
	if errVerify != nil {
		t.Fatalf("VerifyRegistry() error = %v", errVerify)
	}
}

func TestVerifyRegistryRejectsMismatchedVersion(t *testing.T) {
	registryPath := writeRegistryFixture(t, "0.3.1362-0", "https://github.com/karlorz/cpa-account-config-manager")
	errVerify := VerifyRegistry(RegistryOptions{
		Path:       registryPath,
		PluginID:   "cpa-account-config-manager",
		Version:    "0.3.1362-1",
		Repository: "https://github.com/karlorz/cpa-account-config-manager",
	})
	if errVerify == nil || !strings.Contains(errVerify.Error(), "version") {
		t.Fatalf("VerifyRegistry() error = %v, want version rejection", errVerify)
	}
}

func TestVerifyRegistryRejectsMismatchedForkIdentity(t *testing.T) {
	registryPath := writeRegistryFixture(t, "0.3.1362-1", "https://github.com/example/wrong-fork")
	errVerify := VerifyRegistry(RegistryOptions{
		Path:       registryPath,
		PluginID:   "cpa-account-config-manager",
		Version:    "0.3.1362-1",
		Repository: "https://github.com/karlorz/cpa-account-config-manager",
	})
	if errVerify == nil || !strings.Contains(errVerify.Error(), "repository") {
		t.Fatalf("VerifyRegistry() error = %v, want repository rejection", errVerify)
	}
}

func TestVerifyRegistryRejectsMismatchedHomepage(t *testing.T) {
	registryPath := writeRegistryFixtureWithHomepage(
		t,
		"0.3.1362-1",
		"https://github.com/karlorz/cpa-account-config-manager",
		"https://github.com/example/wrong-homepage",
	)
	errVerify := VerifyRegistry(RegistryOptions{
		Path:       registryPath,
		PluginID:   "cpa-account-config-manager",
		Version:    "0.3.1362-1",
		Repository: "https://github.com/karlorz/cpa-account-config-manager",
	})
	if errVerify == nil || !strings.Contains(errVerify.Error(), "homepage") {
		t.Fatalf("VerifyRegistry() error = %v, want homepage rejection", errVerify)
	}
}

func writeRegistryFixture(t *testing.T, version, repository string) string {
	t.Helper()
	return writeRegistryFixtureWithHomepage(t, version, repository, repository)
}

func writeRegistryFixtureWithHomepage(t *testing.T, version, repository, homepage string) string {
	t.Helper()
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	contents := `{
  "schema_version": 1,
  "plugins": [{
    "id": "cpa-account-config-manager",
    "version": "` + version + `",
    "repository": "` + repository + `",
    "homepage": "` + homepage + `"
  }]
}`
	if errWrite := os.WriteFile(registryPath, []byte(contents), 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	return registryPath
}
