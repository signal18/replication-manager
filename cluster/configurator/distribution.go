package configurator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/share"
)

// DBOsFamily describes the OS-level package repository for a given OS family.
// Selected by tag: "rpm" → yum, default → apt/debian.
type DBOsFamily struct {
	Filter      string   `json:"filter"`
	RepoType    string   `json:"repo_type"`    // apt, yum
	RepoBaseURL string   `json:"repo_base_url"`
	RepoKeyURL  string   `json:"repo_key_url"`
	Codenames   []string `json:"codenames"`   // Debian/Ubuntu codenames
	OsVersions  []string `json:"os_versions"` // RHEL versions (8, 9)
	URLPattern  string   `json:"url_pattern"` // template: {repo_base_url}/{major_minor}/{os_id}
}

// DBDeployMethod describes the deployment mechanism.
// Selected by tag: "docker" → container image, "package" → tarball, default → OS repository.
type DBDeployMethod struct {
	Filter         string `json:"filter"`
	Type           string `json:"type"`             // docker, tarball, repository
	Image          string `json:"image"`             // Docker image name (docker only)
	TarballBaseURL string `json:"tarball_base_url"`  // download URL base (tarball only)
	Comment        string `json:"comment,omitempty"`
}

// DBDistributions is the top-level structure for db_distributions.json.
// Two orthogonal axes: OS family (where to get packages) and deploy method (how to install).
type DBDistributions struct {
	Version       string           `json:"version"`
	GeneratedAt   string           `json:"generated_at"`
	OsFamilies    []DBOsFamily     `json:"os_families"`
	DeployMethods []DBDeployMethod `json:"deploy_methods"`
}

// LoadDBDistributions loads db_distributions.json.
// Priority: PluginDataDir (BO push) > embedded default.
func LoadDBDistributions(pluginDataDir string) (*DBDistributions, error) {
	var byteValue []byte

	// Check BO-pushed file first
	if pluginDataDir != "" {
		boFile := filepath.Join(pluginDataDir, "db_distributions.json")
		if data, err := os.ReadFile(boFile); err == nil && len(data) > 0 {
			byteValue = data
		}
	}

	// Fall back to embedded default
	if len(byteValue) == 0 {
		var err error
		byteValue, err = share.EmbededDbModuleFS.ReadFile("plugins/data/db_distributions.json")
		if err != nil {
			return nil, err
		}
	}

	var dist DBDistributions
	if err := json.Unmarshal(byteValue, &dist); err != nil {
		return nil, err
	}
	return &dist, nil
}

// GetOsFamily returns the OS family matching the active DB tags.
// "rpm" tag → yum family, default (empty filter) → apt/debian.
func (d *DBDistributions) GetOsFamily(haveTag func(string) bool) *DBOsFamily {
	if d == nil {
		return nil
	}
	var defaultFamily *DBOsFamily
	for i := range d.OsFamilies {
		fam := &d.OsFamilies[i]
		if fam.Filter == "" {
			defaultFamily = fam
			continue
		}
		if haveTag(fam.Filter) {
			return fam
		}
	}
	return defaultFamily
}

// GetDeployMethod returns the deploy method matching the active DB tags.
// "docker" → docker, "package" → tarball, default (empty filter) → repository.
func (d *DBDistributions) GetDeployMethod(haveTag func(string) bool) *DBDeployMethod {
	if d == nil {
		return nil
	}
	var defaultMethod *DBDeployMethod
	for i := range d.DeployMethods {
		m := &d.DeployMethods[i]
		if m.Filter == "" {
			defaultMethod = m
			continue
		}
		if haveTag(m.Filter) {
			return m
		}
	}
	return defaultMethod
}

// GetDeployMethodByType returns a deploy method by its type name (docker, tarball, repository).
func (d *DBDistributions) GetDeployMethodByType(typeName string) *DBDeployMethod {
	if d == nil {
		return nil
	}
	for i := range d.DeployMethods {
		if d.DeployMethods[i].Type == typeName {
			return &d.DeployMethods[i]
		}
	}
	return nil
}

// isContainerOrchestrator returns true for orchestrators that deploy via containers.
func isContainerOrchestrator(orchestrator string) bool {
	return orchestrator == config.ConstOrchestratorOpenSVC ||
		orchestrator == config.ConstOrchestratorKubernetes
}

// ResolveRepoURL builds the full repository URL for a given MariaDB version.
// majorMinor is e.g. "11.8", osID is e.g. "debian", osVersion is e.g. "bookworm" or "9".
func (f *DBOsFamily) ResolveRepoURL(majorMinor, osID, osVersion string) string {
	if f.URLPattern != "" {
		url := f.URLPattern
		url = strings.ReplaceAll(url, "{repo_base_url}", f.RepoBaseURL)
		url = strings.ReplaceAll(url, "{major_minor}", majorMinor)
		url = strings.ReplaceAll(url, "{os_id}", osID)
		url = strings.ReplaceAll(url, "{os_version}", osVersion)
		return url
	}
	return f.RepoBaseURL + "/" + majorMinor
}
