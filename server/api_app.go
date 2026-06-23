// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/githelper"
	"github.com/tidwall/sjson"
)

func (repman *ReplicationManager) apiAppProtectedHandler(router *mux.Router) {
	//PROTECTED ENDPOINTS FOR APPS
	router.Handle("/api/clusters/{clusterName}/apps/{appName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxApp)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/service-opensvc", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetAppServiceConfig)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/substitution", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppSubstitutionVariables)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/resolve-template", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppResolveTemplate)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployment", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppDeployments)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployment/{field}/index/{index}/{key}/modify", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxModifyDeploymentField)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployment/{field}/add", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAddDeploymentFieldRow)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployment/{field}/index/{index}/drop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxDropDeploymentFieldRow)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/unprovision", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppUnprovision)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/provision", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppProvision)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/update-routes", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppUpdateRoutes)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/stop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppStop)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/stop/{node}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppStop)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/start", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppStart)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/start/{node}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppStart)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/restart", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppRestart)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/restart/{node}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppRestart)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/abort", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppAbort)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/clear", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppClear)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/need-restart", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppNeedRestart)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/need-reprov", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppNeedReprov)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/settings/actions/set/{setting}/{value:.*}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppSetSetting)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/settings/actions/switch/{setting}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppSwitchSetting)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/settings/actions/clear/{setting}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppClearSetting)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/settings/actions/reset-from-template", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppResetFromTemplate)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/settings/actions/reset-from-template/preview", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppResetFromTemplatePreview)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/settings/actions/save-as-template", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppSaveAsTemplate)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/settings/actions/save-as-template/{templateName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppSaveAsTemplate)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/storages/{field}/index/{index}/{key}/modify", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxModifyStorageField)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/storages/{field}/add", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAddStorage)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/storages/{field}/index/{index}/drop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxDropStorageFieldRow)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/git/{gitName}/actions/get-repo-tree", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGitRepoTree)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/git/{gitName}/actions/get-repo-tree/{force}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGitRepoTree)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/git/actions/check", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGitCheckRepo)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/git/{gitName}/actions/check", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGitCheckRepoByName)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appHost}/{appPort}/actions/drop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppDropByName)),
	))
	router.Handle("/api/clusters/{clusterName}/templates/apps", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppTemplatesList)),
	))
	router.Handle("/api/clusters/{clusterName}/templates/apps/structure-guide", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppTemplateStructureGuide)),
	))
	router.Handle("/api/clusters/{clusterName}/templates/apps/{templateName:.*}/content", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppTemplateContent)),
	))
	router.Handle("/api/clusters/{clusterName}/templates/apps/{templateName:.*}/content/actions/save", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppTemplateContentSave)),
	))
	router.Handle("/api/clusters/{clusterName}/templates/apps/{templateName:.*}/content/actions/delete", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppTemplateContentDelete)),
	))
	router.Handle("/api/clusters/{clusterName}/templates/apps/{templateName:.*}/content/actions/create-local-copy", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppTemplateContentCreateLocalCopy)),
	))
	router.Handle("/api/terminal/connect/clusters/{clusterName}/apps/{appName}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerTerminal)),
	))
	router.Handle("/api/terminal/connect/clusters/{clusterName}/apps/{appName}/{command}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerTerminal)),
	))
}

type appTemplateSummary struct {
	Name         string `json:"name"`
	Origin       string `json:"origin"`
	Scope        string `json:"scope"` // "cluster" | "global"
	Editable     bool   `json:"editable"`
	HasLocalCopy bool   `json:"hasLocalCopy"`
	Refreshable  bool   `json:"refreshable"`
}

type appTemplateContentResponse struct {
	Name         string `json:"name"`
	Content      string `json:"content"`
	Origin       string `json:"origin"`
	HasLocalCopy bool   `json:"hasLocalCopy"`
	Refreshed    bool   `json:"refreshed"`
}

type appTemplateStructureGuideResponse struct {
	Content string `json:"content"`
}

type appTemplateFieldChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

type appTemplateResetPreview struct {
	TemplateName string                   `json:"templateName"`
	ForceRefresh bool                     `json:"forceRefresh"`
	ChangeCount  int                      `json:"changeCount"`
	Changes      []appTemplateFieldChange `json:"changes"`
}

func templateStructureGuideReadErrorStatus(err error) int {
	if errors.Is(err, os.ErrNotExist) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func normalizeTemplateIdentifier(templateName string) (string, error) {
	templateName = strings.TrimSpace(templateName)
	if templateName == "" {
		return "", fmt.Errorf("template name must be provided")
	}

	cleanTemplateName := filepath.Clean(filepath.FromSlash(templateName))
	if cleanTemplateName == "." || cleanTemplateName == string(filepath.Separator) {
		return "", fmt.Errorf("template name must be provided")
	}
	if filepath.IsAbs(cleanTemplateName) {
		return "", fmt.Errorf("template name must be relative to templates root")
	}

	relFromRoot, err := filepath.Rel(".", cleanTemplateName)
	if err != nil {
		return "", fmt.Errorf("template path validation failed: %w", err)
	}
	if relFromRoot == ".." || strings.HasPrefix(relFromRoot, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("template name contains invalid path traversal")
	}

	cleanTemplateNameSlash := filepath.ToSlash(cleanTemplateName)
	if cleanTemplateNameSlash == "shared" {
		return "", fmt.Errorf("shared template name must include a template path")
	}

	return cleanTemplateNameSlash, nil
}

// resolveTemplateCachePath resolves the canonical name and the cluster-scoped write path.
// All user write operations target the cluster-specific directory.
func resolveTemplateCachePath(workingDir, clusterName, templateName string) (string, string, error) {
	normalizedTemplateName, err := normalizeTemplateIdentifier(templateName)
	if err != nil {
		return "", "", err
	}

	root := filepath.Join(workingDir, clusterName, ".templates", "apps")
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", "", fmt.Errorf("invalid templates root path: %w", err)
	}
	localPathAbs, err := filepath.Abs(filepath.Clean(filepath.Join(rootAbs, filepath.FromSlash(normalizedTemplateName)+".toml")))
	if err != nil {
		return "", "", fmt.Errorf("invalid template path: %w", err)
	}
	relPath, err := filepath.Rel(rootAbs, localPathAbs)
	if err != nil {
		return "", "", fmt.Errorf("template path validation failed: %w", err)
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("template name contains invalid path traversal")
	}

	return normalizedTemplateName, localPathAbs, nil
}

func resolveLocalTemplateWritePath(workingDir, clusterName, templateName string) (string, error) {
	_, localPathAbs, err := resolveTemplateCachePath(workingDir, clusterName, templateName)
	if err != nil {
		return "", err
	}

	repoCacheRoot := filepath.Clean(cluster.TemplateRepoCacheRoot(workingDir))
	relToRepoRoot, relErr := filepath.Rel(repoCacheRoot, localPathAbs)
	if relErr == nil && relToRepoRoot != ".." && !strings.HasPrefix(relToRepoRoot, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("template write path is inside pull-only repo cache")
	}

	return localPathAbs, nil
}

func validateTemplateNameForLocalWrite(workingDir, clusterName, templateName string) error {
	normalized, err := normalizeTemplateIdentifier(templateName)
	if err != nil {
		return err
	}
	if strings.HasPrefix(normalized, "shared/") {
		return fmt.Errorf("shared template is read-only; create a local copy before saving")
	}
	_, err = resolveLocalTemplateWritePath(workingDir, clusterName, templateName)
	return err
}

func validateCanonicalTemplateContentForSave(mycluster *cluster.Cluster, templateName string, canonicalContent []byte) error {
	appViper, err := mycluster.LoadTemplateToViper(canonicalContent)
	if err != nil {
		return err
	}

	appcnf := config.AppConfig{Deployment: config.NewDeploymentConfig()}
	if err := appViper.Unmarshal(&appcnf); err != nil {
		return err
	}

	if appcnf.Deployment != nil {
		if resolveErrs := appcnf.Deployment.ResolvePaths(); len(resolveErrs) > 0 {
			for _, resolveErr := range resolveErrs {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr,
					"Template %q deployment path resolution error: %v", templateName, resolveErr)
			}
			return fmt.Errorf("invalid deployment path mapping for template %q", templateName)
		}
	}

	return nil
}

func writeTemplateContentAtomically(path string, content []byte) error {
	parentDir := filepath.Dir(path)
	if err := os.MkdirAll(parentDir, 0750); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(parentDir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmpFile.Write(content); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func inferTemplateMetadata(mycluster *cluster.Cluster, templateName string) appTemplateSummary {
	hasCluster := false
	hasGlobal := false
	hasShared := false
	if mycluster != nil && mycluster.Conf != nil {
		clusterPath := filepath.Join(mycluster.ClusterTemplatesRoot(), templateName+".toml")
		if _, err := os.Stat(clusterPath); err == nil {
			hasCluster = true
		}
		globalPath := filepath.Join(cluster.GlobalTemplatesRoot(mycluster.Conf.WorkingDir), templateName+".toml")
		if _, err := os.Stat(globalPath); err == nil {
			hasGlobal = true
		}
		if cluster.IsSharedDummyTemplate(templateName) {
			sharedPath := filepath.Join(mycluster.Conf.ShareDir, "app", "templates", "dummy.toml")
			if _, err := os.Stat(sharedPath); err == nil {
				hasShared = true
			}
		}
	}

	scope := "global"
	if hasCluster {
		scope = "cluster"
	}

	// origin tags: "shared" = seeded from embedded (global dir), "local" = user-saved (cluster dir), "repo" = remote only
	origin := "repo"
	if hasCluster {
		origin = "local"
	} else if hasShared {
		origin = "shared"
	} else if hasGlobal {
		origin = "local"
	}

	hasLocal := hasCluster || hasGlobal || hasShared
	return appTemplateSummary{
		Name:         templateName,
		Origin:       origin,
		Scope:        scope,
		Editable:     hasCluster,
		HasLocalCopy: hasLocal,
		Refreshable:  !hasCluster,
	}
}

func validateCustomEndpointCredentialPair(accessKey, secretKey string) error {
	hasAccessKey := strings.TrimSpace(accessKey) != ""
	hasSecretKey := strings.TrimSpace(secretKey) != ""
	if hasAccessKey != hasSecretKey {
		return fmt.Errorf("custom endpoint credentials must include both accesskey and secretkey")
	}
	return nil
}

// validateStandaloneCustomEndpointCredentials enforces the product contract for
// standalone custom mounts (no providerName): both credentials must be supplied.
func validateStandaloneCustomEndpointCredentials(accessKey, secretKey string) error {
	if err := validateCustomEndpointCredentialPair(accessKey, secretKey); err != nil {
		return err
	}
	if strings.TrimSpace(accessKey) == "" {
		return fmt.Errorf("standalone custom endpoint mounts require both accesskey and secretkey")
	}
	return nil
}

// hydrateS3MountFromProvider applies provider-managed fields to a provider-linked
// mount using ProviderName as the server-side authority.
//
// Contract notes:
//   - UI may submit credentials, but provider GET/list APIs never return stored
//     credentials to the UI.
//   - AccessKey remains classified as config/env (not secret) by product decision,
//     yet provider APIs still omit it.
//   - For provider-linked mounts, endpoint/region/credentials are resolved here.
func hydrateS3MountFromProvider(mycluster *cluster.Cluster, mount *config.S3Mount) error {
	if mount == nil {
		return fmt.Errorf("s3 mount is nil")
	}
	providerName := strings.TrimSpace(mount.ProviderName)
	if providerName == "" {
		return nil
	}

	var provider *config.S3Provider
	for _, p := range mycluster.GetS3ProvidersSnapshot() {
		if p.Name == providerName {
			cp := p
			provider = &cp
			break
		}
	}
	if provider == nil {
		return fmt.Errorf("provider %q not found", providerName)
	}

	switch provider.ProviderSource {
	case config.S3ProviderSourceCustom:
		mount.Endpoint = provider.Endpoint
		mount.Region = provider.Region
		mount.AccessKey = provider.AccessKey
		mount.SecretKey = provider.SecretKey
	case config.S3ProviderSourceApp:
		s3node, _ := mycluster.GetAppByURL(provider.ProviderApp)
		if s3node == nil {
			return fmt.Errorf("provider %q references unknown app endpoint %q", providerName, provider.ProviderApp)
		}
		acckey, err := s3node.AppConfig.Deployment.GetVariableByName("MINIO_ROOT_USER", false)
		if err != nil || acckey == nil {
			return fmt.Errorf("S3 endpoint app does not have MINIO_ROOT_USER variable set")
		}
		secretkey, err := s3node.AppConfig.Deployment.GetVariableByName("MINIO_ROOT_PASSWORD", false)
		if err != nil || secretkey == nil {
			return fmt.Errorf("S3 endpoint app does not have MINIO_ROOT_PASSWORD variable set")
		}

		mount.Endpoint = provider.ProviderApp
		mount.AccessKey = acckey.Value
		mount.SecretKey = mycluster.Conf.GetEncryptedString(mycluster.Conf.GetDecryptedPassword(mount.Name, secretkey.Value))

		region, _ := s3node.AppConfig.Deployment.GetVariableByName("REGION", false)
		if region != nil {
			mount.Region = region.Value
		} else {
			mount.Region = ""
		}
	default:
		return fmt.Errorf("unsupported provider source %q", provider.ProviderSource)
	}

	return nil
}

// volumeHasDirectoryToken reports whether dir is exactly one of vol's
// whitespace-separated VolumeDir tokens (e.g. "data" in "data mnt"), as
// opposed to a full relative path (e.g. "data/custom-media"). Used to detect
// an explicit bare directory-token S3 mount placement (Phase 16).
func volumeHasDirectoryToken(vol *config.Volume, dir string) bool {
	if vol == nil {
		return false
	}
	for _, tok := range vol.GetVolumeDirs() {
		if tok == dir {
			return true
		}
	}
	return false
}

// @Summary Shows the apps for that specific named cluster
// @Description Shows the apps for that specific named cluster
// @Tags Apps
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.App "Server details retrieved successfully"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/apps/{appName} [get]
func (repman *ReplicationManager) handlerMuxApp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	//marshal unmarchal for ofuscation deep copy of struc
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		uname := repman.GetUserFromRequest(r)
		if _, ok := mycluster.APIUsers[uname]; !ok {
			http.Error(w, "No Valid ACL", http.StatusInternalServerError)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			data, _ := json.Marshal(node)
			var app cluster.App
			err := json.Unmarshal(data, &app)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", http.StatusInternalServerError)
				return
			}
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			err = e.Encode(app)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", http.StatusInternalServerError)
				return
			}
		} else {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Start App Service
// @Description Start the app service for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param node path string false "Node Name (optional, if not provided, will start default node). Can use ALL or * to start on all nodes."
// @Success 200 {string} string "App Service Started"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/start [post]
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/start/{node} [post]
func (repman *ReplicationManager) handlerMuxAppStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Cluster Not Found"})
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		w.WriteHeader(http.StatusForbidden)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "No valid ACL"}); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
		}
		return
	}

	if mycluster.GetOrchestrator() != "opensvc" {
		w.WriteHeader(http.StatusNotImplemented)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "Start is only supported for OpenSVC orchestrator"}); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
		}
		return
	}

	app := mycluster.GetAppFromName(vars["appName"])
	if app == nil {
		w.WriteHeader(http.StatusNotFound)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "App Not Found"}); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
		}
		return
	}

	if err := mycluster.OpenSVCStartAppService(app, vars["node"]); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if encErr := json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to start app service: %s", err)}); encErr != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", encErr)
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "App service started successfully",
		"app":     app.GetName(),
	}); err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
	}
}

// @Summary Stop App Service
// @Description Stop the app service for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param node path string false "Node Name (optional, if not provided, will stop default node). Can use ALL or * to stop on all nodes."
// @Success 200 {string} string "App Service Stopped"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/stop [post]
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/stop/{node} [post]
func (repman *ReplicationManager) handlerMuxAppStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Cluster Not Found"})
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		w.WriteHeader(http.StatusForbidden)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "No valid ACL"}); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
		}
		return
	}

	if mycluster.GetOrchestrator() != "opensvc" {
		w.WriteHeader(http.StatusNotImplemented)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "Stop is only supported for OpenSVC orchestrator"}); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
		}
		return
	}

	app := mycluster.GetAppFromName(vars["appName"])
	if app == nil {
		w.WriteHeader(http.StatusNotFound)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "App Not Found"}); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
		}
		return
	}

	if err := mycluster.OpenSVCStopAppService(app, vars["node"]); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if encErr := json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to stop app service: %s", err)}); encErr != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", encErr)
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "App service stopped successfully",
		"app":     app.GetName(),
	}); err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
	}
}

// @Summary Restart App Service
// @Description Restart the app service for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param node path string false "Node Name (optional, if not provided, will restart default node). Can use ALL or * to restart on all nodes."
// @Success 200 {string} string "App Service Restarted"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/restart [post]
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/restart/{node} [post]
func (repman *ReplicationManager) handlerMuxAppRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Cluster Not Found"})
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		w.WriteHeader(http.StatusForbidden)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "No valid ACL"}); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
		}
		return
	}

	if mycluster.GetOrchestrator() != "opensvc" {
		w.WriteHeader(http.StatusNotImplemented)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "Restart is only supported for OpenSVC orchestrator"}); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
		}
		return
	}

	app := mycluster.GetAppFromName(vars["appName"])
	if app == nil {
		w.WriteHeader(http.StatusNotFound)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "App Not Found"}); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
		}
		return
	}

	ridParam := r.URL.Query().Get("rid")
	if err := cluster.ValidateAppRestartRid(ridParam); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		if encErr := json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}); encErr != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", encErr)
		}
		return
	}
	nodeParam := vars["node"]
	if nodeParam == "" {
		nodeParam = app.GetAgent()
	}
	if nodeParam == "" {
		w.WriteHeader(http.StatusBadRequest)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "Node is required when app has no default agent"}); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
		}
		return
	}

	if err := mycluster.OpenSVCRestartAppService(app, nodeParam, ridParam); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if encErr := json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to restart app service: %s", err)}); encErr != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", encErr)
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "App service restarted successfully",
		"app":     app.GetName(),
		"node":    nodeParam,
		"rid":     ridParam,
	}); err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %s", err)
	}
}

func (repman *ReplicationManager) handlerMuxAppAbort(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cluster Not Found"})
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "No valid ACL"})
		return
	}

	if mycluster.GetOrchestrator() != "opensvc" {
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{"error": "Abort is only supported for OpenSVC orchestrator"})
		return
	}

	app := mycluster.GetAppFromName(vars["appName"])
	if app == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "App Not Found"})
		return
	}

	err := mycluster.OpenSVCAbortAppService(app)
	if err != nil {
		if errors.Is(err, cluster.ErrOpenSVCAbortNotSupported) {
			w.WriteHeader(http.StatusNotImplemented)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to abort app service: %s", err)})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Abort requested successfully",
		"app":     app.GetName(),
	})
}

func (repman *ReplicationManager) handlerMuxAppClear(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cluster Not Found"})
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "No valid ACL"})
		return
	}

	if mycluster.GetOrchestrator() != "opensvc" {
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{"error": "Clear is only supported for OpenSVC orchestrator"})
		return
	}

	app := mycluster.GetAppFromName(vars["appName"])
	if app == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "App Not Found"})
		return
	}

	nodeParam := r.URL.Query().Get("node")
	err := mycluster.OpenSVCClearAppInstanceState(app, nodeParam)
	if err != nil {
		if errors.Is(err, cluster.ErrOpenSVCClearNotSupported) {
			w.WriteHeader(http.StatusNotImplemented)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to clear app instance state: %s", err)})
		return
	}

	effectiveNode := nodeParam
	if effectiveNode == "" {
		effectiveNode = app.GetAgent()
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Instance monitor state cleared successfully",
		"app":     app.GetName(),
		"node":    effectiveNode,
	})
}

// @Summary Provision App Service
// @Description Provision the app service for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "App Service Provisioned"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/provision [post]
func (repman *ReplicationManager) handlerMuxAppProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		if mycluster.GetOrchestrator() != "opensvc" {
			http.Error(w, "Orchestrator not supported", http.StatusInternalServerError)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			err := mycluster.InitAppService(node)
			if err != nil {
				http.Error(w, "Failed to provision app service: "+err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}
}

// @Summary Update App Routes
// @Description Push route configuration to the OpenSVC gateway service for a given app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "App Routes Updated"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "App Not Found"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/update-routes [post]
func (repman *ReplicationManager) handlerMuxAppUpdateRoutes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		if mycluster.GetOrchestrator() != "opensvc" {
			http.Error(w, "Orchestrator not supported", http.StatusInternalServerError)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			if err := mycluster.OpenSVCProvisionRoute(node); err != nil {
				http.Error(w, "Failed to update app routes: "+err.Error(), http.StatusInternalServerError)
				return
			}
			fmt.Fprintf(w, "App Routes Updated")
		} else {
			http.Error(w, "App Not Found", http.StatusNotFound)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}
}

// @Summary Unprovision App Service
// @Description Unprovision the app service for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "App Service Unprovisioned"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/unprovision [post]
func (repman *ReplicationManager) handlerMuxAppUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		if mycluster.GetOrchestrator() != "opensvc" {
			http.Error(w, "Orchestrator not supported", http.StatusInternalServerError)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			if err := mycluster.OpenSVCUnprovisionAppService(node); err != nil {
				http.Error(w, fmt.Sprintf("Can not unprovision app service: %s", err), http.StatusInternalServerError)
				return
			}
		} else {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}
}

// @Summary Check if App Needs Restart
// @Description Check if the app service for a given cluster and app needs a restart
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "Need restart!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 503 {string} string "No restart needed!" "Not a Valid Server!"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/need-restart [get]
func (repman *ReplicationManager) handlerMuxAppNeedRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil && !node.IsDown() {
			if node.HasRestartCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No restart needed!"))
			http.Error(w, "Encoding error", http.StatusServiceUnavailable)

		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Check if App Needs Reprovision
// @Description Check if the app service for a given cluster and app needs reprovisioning
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "Need reprov!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 503 {string} string "No reprov needed!" "Not a Valid Server!"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/need-reprov [get]
func (repman *ReplicationManager) handlerMuxAppNeedReprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil && !node.IsDown() {
			if node.HasReprovCookie() {
				w.Write([]byte("200 -Need reprov!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "Encoding error", http.StatusServiceUnavailable)

		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Shows the deployments for that specific named app
// @Description Shows the deployments for that specific named app
// @Tags Apps
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {array} config.Deployment "Server details retrieved successfully"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployment [get]
func (repman *ReplicationManager) handlerMuxAppDeployments(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	//marshal unmarchal for ofuscation deep copy of struc
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		uname := repman.GetUserFromRequest(r)
		if _, ok := mycluster.APIUsers[uname]; !ok {
			http.Error(w, "No Valid ACL", http.StatusInternalServerError)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			// Take a locked JSON snapshot, then normalise and mask a copy so the
			// GET path never mutates live deployment state.
			node.Lock()
			snapshot, snapErr := json.Marshal(node.AppConfig.Deployment)
			node.Unlock()
			if snapErr != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", snapErr)
				http.Error(w, "Encoding error", http.StatusInternalServerError)
				return
			}

			var depCopy config.Deployment
			if unmarshalErr := json.Unmarshal(snapshot, &depCopy); unmarshalErr != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error decoding JSON: ", unmarshalErr)
				http.Error(w, "Encoding error", http.StatusInternalServerError)
				return
			}
			depCopy.NormalizeRoutes()

			dep, err := json.MarshalIndent(&depCopy, "", "\t")
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", http.StatusInternalServerError)
				return
			}

			for vidx, v := range depCopy.Variables {
				if v.Type == "secret" {
					dep, err = sjson.SetBytes(dep, fmt.Sprintf("variables.%d.value", vidx), "*****")
					if err != nil {
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error maskin secrets JSON: ", err)
						http.Error(w, "Encoding error", http.StatusInternalServerError)
						return
					}
				}
			}

			for gidx := range depCopy.Storages.GitClones {
				dep, err = sjson.SetBytes(dep, fmt.Sprintf("storages.gitClones.%d.pass", gidx), "*****")
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error maskin secrets JSON: ", err)
					http.Error(w, "Encoding error", http.StatusInternalServerError)
					return
				}
			}

			for midx := range depCopy.Storages.S3Mounts {
				dep, err = sjson.SetBytes(dep, fmt.Sprintf("storages.s3Mounts.%d.secretkey", midx), "*****")
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error maskin secrets JSON: ", err)
					http.Error(w, "Encoding error", http.StatusInternalServerError)
					return
				}
			}

			w.Write(dep)
			return
		} else {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Modify Deployment Field
// @Description Modify a specific field in a deployment for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param field path string true "Field to modify"
// @Param index path string true "Index of the field to modify"
// @Param key path string true "Key of the field to modify"
// @Param value body object{value=any} true "New value for the field"
// @Success 200 {string} string "Deployment field modified"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found" "Deployment not found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployment/{field}/index/{index}/{key}/modify [post]
func (repman *ReplicationManager) handlerMuxModifyDeploymentField(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			var newValue string
			var condValue config.AVSlice
			if vars["field"] == "variables" && vars["key"] == "conditional" {
				type ConditionalValue struct {
					Value config.AVSlice `json:"value"`
				}
				var body ConditionalValue
				err := json.NewDecoder(r.Body).Decode(&body)
				if err != nil {
					http.Error(w, "Error decoding JSON: "+err.Error(), http.StatusInternalServerError)
					return
				}

				condValue = body.Value
				sort.Sort(condValue)
			} else {
				type FieldValue struct {
					Value string `json:"value"`
				}
				var body FieldValue
				err := json.NewDecoder(r.Body).Decode(&body)
				if err != nil {
					http.Error(w, "Error decoding JSON: "+err.Error(), http.StatusInternalServerError)
					return
				}

				newValue = body.Value
			}

			if vars["index"] == "" || vars["index"] == "undefined" {
				http.Error(w, "Index not provided", http.StatusInternalServerError)
				return
			}

			index, err := strconv.Atoi(vars["index"])
			if err != nil {
				http.Error(w, "Error parsing index: "+err.Error(), http.StatusInternalServerError)
				return
			}

			if index < 0 {
				http.Error(w, "Index cannot be negative", http.StatusBadRequest)
				return
			}

			if vars["key"] == "" || vars["key"] == "undefined" {
				// For gitClones, variables, and path, key is required
				http.Error(w, "Key not provided", http.StatusBadRequest)
				return
			}

			switch vars["field"] {
			// fields which are arrays of objects
			case "routes":
				// For identity-changing edits (non-monitor), acquire the per-gateway mutex
				// before node.Lock() so that external-route snapshot and commit are
				// serialized across clusters sharing the same gateway.
				// gwUnlock is a no-op for monitor-only edits or when no gateway is set;
				// it is called explicitly at every exit so the mutex is released right
				// after the commit and before any post-commit I/O (SaveConfig, etc.).
				gwUnlock := func() {}
				var externalRoutes [][]config.Route
				if !strings.HasPrefix(vars["key"], "monitor") {
					gw := strings.ToLower(strings.TrimSpace(mycluster.Conf.Cloud18GatewayService))
					if gw != "" {
						gwMu := repman.getGatewayMutex(gw)
						gwMu.Lock()
						gwUnlock = gwMu.Unlock
					}
					externalRoutes = repman.allExternalGatewayRoutes(vars["clusterName"], vars["appName"])
				}

				// Hold node.Lock() for the full validate-mutate-write sequence so that:
				//   a) the base row is always the live value (no stale-base overwrite), and
				//   b) validation and commit are atomic w.r.t. concurrent same-app edits.
				node.Lock()
				if index >= len(node.AppConfig.Deployment.Routes) {
					node.Unlock()
					gwUnlock()
					http.Error(w, "Index out of range for routes", http.StatusInternalServerError)
					return
				}
				row := node.AppConfig.Deployment.Routes[index].Clone()
				switch vars["key"] {
				case "name":
					row.Name = newValue
				case "mode":
					row.Mode = strings.ToLower(strings.TrimSpace(newValue))
					// Auto-align protocol to a valid default for the new mode so that
					// the mode change doesn't fail validation mid-transition.
					// host requires https; port requires http or tcp.
					switch row.Mode {
					case "host":
						row.Protocol = "https"
					case "port":
						if row.Protocol != "http" && row.Protocol != "tcp" {
							row.Protocol = "tcp"
						}
					}
				case "cname":
					cname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(newValue)), ".")
					for i, existing := range node.AppConfig.Deployment.Routes {
						existing.Normalize() // handle legacy unnormalized in-memory routes
						if i != index && strings.EqualFold(existing.CName, cname) {
							node.Unlock()
							gwUnlock()
							http.Error(w, "Cannot duplicate route with same CName", http.StatusBadRequest)
							return
						}
					}
					row.CName = cname
				case "port":
					effectiveMode := strings.ToLower(strings.TrimSpace(row.Mode))
					// Mirror Normalize()'s implicit-mode inference so that legacy routes
					// saved with Mode=="" and Protocol=="tcp" are treated as port mode,
					// not bypassing the asymmetry guard below.
					if effectiveMode == "" {
						if strings.ToLower(strings.TrimSpace(row.Protocol)) == "tcp" {
							effectiveMode = "port"
						} else {
							effectiveMode = "host"
						}
					}
					isAsymmetric := effectiveMode == "port" &&
						row.SourcePort != "" && row.DestinationPort != "" &&
						row.SourcePort != row.DestinationPort
					if isAsymmetric && !strings.Contains(newValue, ":") {
						node.Unlock()
						gwUnlock()
						http.Error(w, "use 'src:dst' format or 'sourceport'/'destport' to edit an asymmetric port-mode route", http.StatusBadRequest)
						return
					}
					row.Port = newValue
					// Normalize() only copies Port into DestinationPort/SourcePort
					// when those fields are empty.  After first normalization they are
					// already set, so a "port" edit would be a silent no-op for the
					// effective routing port.  Reset the derived fields here so
					// Normalize() re-derives them from the new Port value.
					row.DestinationPort = ""
					if effectiveMode == "port" {
						row.SourcePort = ""
					}
				case "sourceport", "sourcePort":
					row.SourcePort = newValue
					row.Port = ""
				case "destport", "destPort":
					row.DestinationPort = newValue
					row.Port = ""
				case "protocol":
					row.Protocol = strings.ToLower(strings.TrimSpace(newValue))
				case "primary":
					row.Primary = newValue == "true"
				case "monitor.clear":
					row.Monitor = nil
				case "monitor.path":
					if row.Monitor == nil {
						row.Monitor = &config.RouteMonitor{}
					}
					row.Monitor.Path = newValue
				case "monitor.auth-type":
					if row.Monitor == nil {
						row.Monitor = &config.RouteMonitor{}
					}
					row.Monitor.AuthType = strings.ToLower(strings.TrimSpace(newValue))
				case "monitor.auth-user":
					if row.Monitor == nil {
						row.Monitor = &config.RouteMonitor{}
					}
					row.Monitor.AuthUser = newValue
				case "monitor.auth-secret-var":
					if newValue != "" {
						v, verr := node.AppConfig.Deployment.GetVariableByName(newValue, true)
						if verr != nil {
							node.Unlock()
							gwUnlock()
							http.Error(w, "monitor auth-secret-var: variable not found: "+verr.Error(), http.StatusBadRequest)
							return
						}
						if v.Type != config.VariableTypeSecret {
							node.Unlock()
							gwUnlock()
							http.Error(w, "monitor auth-secret-var must reference a variable of type 'secret'", http.StatusBadRequest)
							return
						}
					}
					if row.Monitor == nil {
						row.Monitor = &config.RouteMonitor{}
					}
					row.Monitor.AuthSecretVar = newValue
				case "monitor.expect-status":
					if newValue != "" {
						if _, perr := config.ParseExpectStatus(newValue); perr != nil {
							node.Unlock()
							gwUnlock()
							http.Error(w, "monitor expect-status: "+perr.Error(), http.StatusBadRequest)
							return
						}
					}
					if row.Monitor == nil {
						row.Monitor = &config.RouteMonitor{}
					}
					row.Monitor.ExpectStatus = newValue
				default:
					node.Unlock()
					gwUnlock()
					http.Error(w, "Invalid key for routes", http.StatusBadRequest)
					return
				}

				row.Normalize()
				if err := row.Validate(); err != nil {
					node.Unlock()
					gwUnlock()
					http.Error(w, "Invalid route: "+err.Error(), http.StatusBadRequest)
					return
				}
				if err := row.Monitor.ValidateSecretRef(node.AppConfig.Deployment.Variables); err != nil {
					node.Unlock()
					gwUnlock()
					http.Error(w, "Invalid route monitor: "+err.Error(), http.StatusBadRequest)
					return
				}
				if !strings.HasPrefix(vars["key"], "monitor") {
					peers := make([]config.Route, 0, len(node.AppConfig.Deployment.Routes)-1)
					for i, r := range node.AppConfig.Deployment.Routes {
						if i != index {
							peers = append(peers, r)
						}
					}
					if err := config.CheckGatewayConflicts([]config.Route{row}, config.NormalizedCopy(peers)); err != nil {
						node.Unlock()
						gwUnlock()
						http.Error(w, "Duplicate route: "+err.Error(), http.StatusBadRequest)
						return
					}
					if err := config.CheckGatewayConflicts([]config.Route{row}, externalRoutes...); err != nil {
						node.Unlock()
						gwUnlock()
						http.Error(w, "Gateway conflict: "+err.Error(), http.StatusBadRequest)
						return
					}
				}
				node.AppConfig.Deployment.Routes[index] = row
				node.AppConfig.Deployment.EnforceSinglePrimary()
				node.Unlock()
				gwUnlock()

				if row.Monitor != nil && row.Monitor.AuthType != "" {
					switch row.Protocol {
					case "http":
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
							"Route edit for app %s: monitor auth (%s) configured on plain HTTP route %s — credentials will be transmitted unencrypted",
							node.Name, row.Monitor.AuthType, row.CName)
					case "https":
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
							"Route edit for app %s: monitor auth (%s) configured on HTTPS route %s with unverified certificate (InsecureSkipVerify=true)",
							node.Name, row.Monitor.AuthType, row.CName)
					}
				}

				// DNS cleanup for renamed or rekeyed managed host routes happens
				// via the full reconcile path (update-routes / provision), not here.
			case "variables":
				if index >= len(node.AppConfig.Deployment.Variables) {
					http.Error(w, "Index out of range for variables", http.StatusBadRequest)
					return
				}

				row := node.AppConfig.Deployment.Variables[index]
				if row.Locked {
					http.Error(w, "Unable to change name of locked variable. Please change the source of the variable instead.", http.StatusBadRequest)
					return
				}
				// Modify field based on key
				switch vars["key"] {
				case "name":
					if row.Name == newValue {
						http.Error(w, "Variable name unchanged", http.StatusBadRequest)
						return
					}
					// Cascade rename into any route monitor that references this variable.
					oldName := row.Name
					node.Lock()
					for i, r := range node.AppConfig.Deployment.Routes {
						if r.Monitor != nil && r.Monitor.AuthSecretVar == oldName {
							node.AppConfig.Deployment.Routes[i].Monitor.AuthSecretVar = newValue
						}
					}
					node.Unlock()
					row.Name = newValue
				case "value":
					if row.Value == newValue {
						http.Error(w, "Variable value unchanged", http.StatusBadRequest)
						return
					}
					row.Value = newValue
				case "type":
					if row.Type == newValue {
						http.Error(w, "Variable type unchanged", http.StatusBadRequest)
						return
					}
					// Reject type changes away from 'secret' when routes reference this variable.
					if row.Type == config.VariableTypeSecret && newValue != config.VariableTypeSecret {
						if refs := routesReferencingSecretVar(node.AppConfig.Deployment.Routes, row.Name); len(refs) > 0 {
							http.Error(w, fmt.Sprintf("cannot change type of variable %q: referenced as auth-secret-var in %d route(s)", row.Name, len(refs)), http.StatusBadRequest)
							return
						}
					}
					row.Type = newValue
				case "conditional":
					old := row.Conditional
					// addFunc := func(new config.AgentVariable) config.AgentVariable {
					// 	// new.Value, _ = node.ClusterGroup.ParseAppTemplate(new.Value, node.AppClusterSubstitute)
					// 	return new
					// }
					// updateFunc := func(old, new config.AgentVariable) config.AgentVariable {
					// 	// new.Value, _ = node.ClusterGroup.ParseAppTemplate(new.Value, node.AppClusterSubstitute)
					// 	return new
					// }

					if row.Type == config.VariableTypeSecret {
						for i, v := range condValue {
							// Decrypt the value if it's a secret
							condValue[i].Value = mycluster.Conf.GetEncryptedString(mycluster.Conf.GetDecryptedPassword(row.Name+"@"+v.Agent, v.Value))
						}
					}

					row.Conditional = old.Merge(condValue, nil, nil)
				default:
					http.Error(w, "Invalid key for variables", http.StatusInternalServerError)
					return
				}
				if row.Type == config.VariableTypeSecret {
					row.Value = mycluster.Conf.GetEncryptedString(mycluster.Conf.GetDecryptedPassword(row.Name, row.Value))
				}
				node.AppConfig.Deployment.Variables[index] = row
			case "paths":
				if index >= len(node.AppConfig.Deployment.Paths) {
					http.Error(w, "Index out of range for paths", http.StatusInternalServerError)
					return
				}

				deployment := node.AppConfig.Deployment
				if index >= len(deployment.Paths) {
					http.Error(w, "Index out of range for paths", http.StatusInternalServerError)
					return
				}

				pm := deployment.Paths[index]

				// Modify field based on key
				switch vars["key"] {
				case "name":
					http.Error(w, "Cannot change name of path. Please drop the path and recreate it with the new name.", http.StatusInternalServerError)
					return
				case "parent":
					if pm.ParentName == newValue {
						http.Error(w, "Parent unchanged", http.StatusInternalServerError)
						return
					}

					if newValue == "" {
						pm.ParentName = ""
						pm.Parent = nil
					} else {
						parent, err := deployment.GetPathMapping(newValue)
						if err != nil {
							http.Error(w, "Parent path not found: "+err.Error(), http.StatusInternalServerError)
							return
						}

						pm.ParentName = newValue
						pm.Parent = parent
					}

				case "dockerpath":
					if pm.DockerPath == newValue {
						http.Error(w, "Docker path unchanged", http.StatusInternalServerError)
						return
					}

					pm.DockerPath = newValue
				case "srctype":
					newSourceType := config.SourceType(newValue)
					if pm.SourceType == newSourceType {
						http.Error(w, "Source type unchanged", http.StatusInternalServerError)
						return
					}

					if newValue == "" {
						http.Error(w, "Source type cannot be empty", http.StatusInternalServerError)
						return
					}

					switch newValue {
					case string(config.SourceGit), string(config.SourceS3), string(config.SourceVolume):
						// Reset source name/path/volume together so the row never
						// carries stale values from the previous source type. The
						// row is intentionally left incomplete (SourceType set,
						// SourceName empty) until a follow-up "srcname" edit
						// supplies the new source.
						pm.SourceType = newSourceType
						pm.SourceName = ""
						pm.SourcePath = ""
						pm.VolumeName = ""
						if node.HasProvisionCookie() {
							node.SetReprovCookie()
						}
					default:
						http.Error(w, "Invalid source type. Must be 'git', 's3', or 'volume'", http.StatusInternalServerError)
						return
					}
				case "srcname":
					if pm.SourceName == newValue {
						http.Error(w, "Source name unchanged", http.StatusInternalServerError)
						return
					}

					if newValue == "" && pm.SourceType != "" {
						http.Error(w, "Source name cannot be empty when source type is set", http.StatusInternalServerError)
						return
					}

					switch pm.SourceType {
					case config.SourceGit:
						source, err := deployment.GetGitClone(newValue)
						if err == nil {
							pm.SourcePath = source.GetSourcePath()
						}
					case config.SourceS3:
						source, err := deployment.GetS3Mount(newValue)
						if err == nil {
							pm.SourcePath = source.GetSourcePath()
						}
					case config.SourceVolume:
						// Phase 8 model: direct volume sources are relative to
						// the volume root, represented as "." -- not
						// Volume.GetSourcePath() (raw volumedir).
						if _, err := deployment.GetVolumeByName(newValue); err == nil {
							pm.SourcePath = "."
						}
					default:
						http.Error(w, "Invalid source type. Must be 'git', 's3', or 'volume'", http.StatusInternalServerError)
						return
					}

					pm.SourceName = newValue

				case "srcpath":
					if pm.SourcePath == newValue {
						http.Error(w, "Source path unchanged", http.StatusInternalServerError)
						return
					}

					if newValue != "" && newValue != "/" {
						pm.SourcePath = newValue
					} else if pm.SourceName != "" {
						// Reset/blank srcpath: derive the default from the path's
						// CURRENT source identity (pm.SourceName/pm.SourceType),
						// not from the just-submitted reset value ("" or "/").
						switch pm.SourceType {
						case config.SourceGit:
							source, err := deployment.GetGitClone(pm.SourceName)
							if err == nil {
								pm.SourcePath = source.GetSourcePath()
							}
						case config.SourceS3:
							source, err := deployment.GetS3Mount(pm.SourceName)
							if err == nil {
								pm.SourcePath = source.GetSourcePath()
							}
						case config.SourceVolume:
							// Phase 8 model: direct volume root is "."
							if _, err := deployment.GetVolumeByName(pm.SourceName); err == nil {
								pm.SourcePath = "."
							}
						default:
							http.Error(w, "Invalid source type. Must be 'git', 's3', or 'volume'", http.StatusInternalServerError)
							return
						}
					} else {
						http.Error(w, "Source path cannot be empty", http.StatusInternalServerError)
						return
					}

				default:
					http.Error(w, "Invalid key for path", http.StatusInternalServerError)
					return
				}

				// Resolve failures are warning-only and do not abort persistence:
				// field-by-field edits (e.g. "srctype" followed later by
				// "srcname") intentionally pass through a momentarily incomplete
				// row, and GetOpenSVCDeploymentPathMapping already skips
				// unresolved/incomplete path mappings at provisioning time.
				err := deployment.ResolvePath(pm)
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error resolving path after modification: ", err)
				}
			default:
				http.Error(w, "Invalid field", http.StatusInternalServerError)
				return
			}

			mycluster.EnqueueRefreshAppTemplateMD5(node)

			mycluster.ConfigManager.SaveConfig(mycluster, false)
			repman.RecomputeGatewayConflicts(mycluster.Name, "")
			w.Write([]byte("Deployment field modified"))
		} else {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// routesReferencingSecretVar returns the indices of routes whose
// monitor.auth-secret-var equals varName.
func routesReferencingSecretVar(routes []config.Route, varName string) []int {
	var out []int
	for i, r := range routes {
		if r.Monitor != nil && r.Monitor.AuthSecretVar == varName {
			out = append(out, i)
		}
	}
	return out
}

// getGatewayMutex returns the per-gateway serialization mutex for gw, creating
// it on first use.  Holding this mutex across allExternalGatewayRoutes + node.Lock()
// prevents two concurrent requests on different clusters from both passing the
// cross-cluster conflict check and both committing conflicting routes.
func (repman *ReplicationManager) getGatewayMutex(gw string) *sync.Mutex {
	actual, _ := repman.gatewayMu.LoadOrStore(gw, new(sync.Mutex))
	return actual.(*sync.Mutex)
}

// allExternalGatewayRoutes returns one normalized route-slice per app that
// shares the same gateway as the named cluster, excluding the named cluster+app.
// Used to check cross-cluster conflicts before accepting an API route change.
func (repman *ReplicationManager) allExternalGatewayRoutes(excludeClusterName, excludeAppName string) [][]config.Route {
	// Snapshot the Clusters map under the repman lock to avoid racing with
	// StartCluster / cluster removal, which mutate the map under that same lock.
	repman.Lock()
	clusterSnapshot := make(map[string]*cluster.Cluster, len(repman.Clusters))
	for k, v := range repman.Clusters {
		clusterSnapshot[k] = v
	}
	repman.Unlock()

	var thisGateway string
	if cl, ok := clusterSnapshot[excludeClusterName]; ok {
		thisGateway = strings.ToLower(strings.TrimSpace(cl.Conf.Cloud18GatewayService))
	}
	if thisGateway == "" {
		return nil
	}
	var others [][]config.Route
	for _, cl := range clusterSnapshot {
		if strings.ToLower(strings.TrimSpace(cl.Conf.Cloud18GatewayService)) != thisGateway {
			continue
		}
		// GetAppsCopy snapshots cl.Apps under the cluster lock so we don't
		// iterate a slice that another goroutine may be appending to.
		for _, app := range cl.GetAppsCopy() {
			if app == nil || app.AppConfig == nil {
				continue
			}
			if cl.Name == excludeClusterName && app.Name == excludeAppName {
				continue
			}
			// Conflicted apps are blocked from publishing — exclude their routes
			// so they don't falsely prevent other clusters from accepting routes.
			if conflicted, _ := cl.IsAppGatewayConflicted(app.AppConfig.AppHost, app.AppConfig.AppPort); conflicted {
				continue
			}
			if normalized := config.NormalizedCopy(app.GetDeploymentRoutesSnapshot()); len(normalized) > 0 {
				others = append(others, normalized)
			}
		}
	}
	return others
}

// @Summary Add Deployment Field Row
// @Description Add a new row to a specific field in a deployment for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param field path string true "Field to add a row to (routes, gitClones, variables, path)"
// @Param body body any true "Array of objects depending on field: - routes: []config.Route - gitClones: []config.GitClone - variables: []config.VariableMapping - path: []config.PathMapping"
// @Success 200 {string} string "Deployment field row added"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found" "Deployment not found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployment/{field}/add [post]
func (repman *ReplicationManager) handlerMuxAddDeploymentFieldRow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	node := mycluster.GetAppFromName(vars["appName"])
	if node == nil {
		http.Error(w, "Server Not Found", http.StatusInternalServerError)
		return
	}

	field := vars["field"]
	var affected bool

	switch field {
	case "routes":
		var body []config.Route
		body, err := decodeSlice[config.Route](r, w, "route")
		if err != nil {
			http.Error(w, "Error decoding JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Acquire per-gateway mutex and snapshot external routes once for the entire
		// batch.  gwUnlock is called explicitly at every exit so the mutex is released
		// right after the commit and before post-commit I/O (SaveConfig, etc.).
		gwUnlock := func() {}
		gw := strings.ToLower(strings.TrimSpace(mycluster.Conf.Cloud18GatewayService))
		if gw != "" {
			gwMu := repman.getGatewayMutex(gw)
			gwMu.Lock()
			gwUnlock = gwMu.Unlock
		}
		others := repman.allExternalGatewayRoutes(vars["clusterName"], vars["appName"])

		for _, row := range body {
			row.Normalize()
			if err := row.Validate(); err != nil {
				gwUnlock()
				http.Error(w, "Invalid route: "+err.Error(), http.StatusBadRequest)
				return
			}
			if err := row.Monitor.ValidateSecretRef(node.AppConfig.Deployment.Variables); err != nil {
				gwUnlock()
				http.Error(w, "Invalid route monitor: "+err.Error(), http.StatusBadRequest)
				return
			}

			// Hold node.Lock() across intra-app conflict check and append so that
			// two concurrent adds can't both validate against the same pre-append state.
			node.Lock()
			existing := config.NormalizedCopy(node.AppConfig.Deployment.Routes)
			if err := config.CheckGatewayConflicts([]config.Route{row}, existing); err != nil {
				node.Unlock()
				gwUnlock()
				http.Error(w, "Duplicate route: "+err.Error(), http.StatusBadRequest)
				return
			}
			if err := config.CheckGatewayConflicts([]config.Route{row}, others...); err != nil {
				node.Unlock()
				gwUnlock()
				http.Error(w, "Gateway conflict: "+err.Error(), http.StatusBadRequest)
				return
			}
			node.AppConfig.Deployment.Routes = append(node.AppConfig.Deployment.Routes, row)
			node.Unlock()
			affected = true
		}
		if affected {
			node.Lock()
			node.AppConfig.Deployment.EnforceSinglePrimary()
			node.Unlock()
		}
		gwUnlock()

	case "variables":
		var body []config.VariableMapping
		body, err := decodeSlice[config.VariableMapping](r, w, "variable")
		if err != nil {
			http.Error(w, "Error decoding JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}

		for _, row := range body {
			old := node.AppConfig.GetDeploymentVariables(row.Name)
			if old != nil {
				http.Error(w, "Cannot duplicate variable with same name", http.StatusBadRequest)
				return
			}
			// row.Value, _ = mycluster.ParseAppTemplate(row.Value, node.AppClusterSubstitute)
			mycluster.SetAppVariableValue(node, row)
		}
		affected = true

	case "paths":
		var body []config.PathMapping
		body, err := decodeSlice[config.PathMapping](r, w, "path")
		if err != nil {
			http.Error(w, "Error decoding JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}

		errors := make([]error, 0)
		deployment := node.AppConfig.Deployment
		for _, row := range body {
			if row.DockerPath == "" || row.SourceName == "" || row.SourcePath == "" {
				http.Error(w, "All fields (destPath, sourceName, sourcePath) must be provided for path", http.StatusInternalServerError)
				return
			}
			if row.Name == "" {
				// Generate a unique name for the path if not provided
				row.Name = fmt.Sprintf("path-%s-%s", row.SourceType, row.SourceName)
			}
			err := deployment.InsertPath(row)
			if err != nil {
				errors = append(errors, err)
			} else {
				affected = true
			}
		}

		if affected {
			node.AppConfig.Deployment.SortPaths()
		}

		if len(errors) > 0 {
			errorMessages := make([]string, len(errors))
			for i, err := range errors {
				errorMessages[i] = err.Error()
			}
			http.Error(w, "Error adding paths: "+strings.Join(errorMessages, ", "), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Invalid field", http.StatusInternalServerError)
		return
	}

	if !affected {
		http.Error(w, "No rows added to the field", http.StatusInternalServerError)
		return
	}

	mycluster.EnqueueRefreshAppTemplateMD5(node)

	mycluster.ConfigManager.SaveConfig(mycluster, false)
	repman.RecomputeGatewayConflicts(mycluster.Name, "")
	json.NewEncoder(w).Encode(map[string]string{"message": "Deployment field row added"})
}

func decodeStruct[T any](r *http.Request, w http.ResponseWriter, typename string) (*T, error) {
	var out T
	err := json.NewDecoder(r.Body).Decode(&out)
	if err != nil {
		http.Error(w, "Error decoding "+typename+": "+err.Error(), http.StatusBadRequest)
		return nil, err
	}
	return &out, nil
}

func decodeSlice[T any](r *http.Request, w http.ResponseWriter, typename string) ([]T, error) {
	var out []T
	err := json.NewDecoder(r.Body).Decode(&out)
	if err != nil {
		http.Error(w, "Error decoding "+typename+": "+err.Error(), http.StatusBadRequest)
		return nil, err
	}
	if len(out) == 0 {
		http.Error(w, "No "+typename+" provided", http.StatusBadRequest)
		return nil, fmt.Errorf("empty %s", typename)
	}
	return out, nil
}

func isValidPortFormat(value string) bool {
	if strings.Contains(value, ":") {
		return false
	}
	p, err := strconv.Atoi(value)
	return err == nil && p >= 1 && p <= 65535
}

// @Summary Drop Deployment Field Row
// @Description Drop a specific row from a field in a deployment for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param field path string true "Field to drop a row from (routes, gitClones, variables, path)"
// @Param index path string true "Index of the row to drop"
// @Success 200 {string} string "Deployment field row removed"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found" "Deployment not found" "Index out of range" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployment/{field}/index/{index}/drop [post]
func (repman *ReplicationManager) handlerMuxDropDeploymentFieldRow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	node := mycluster.GetAppFromName(vars["appName"])
	if node == nil {
		http.Error(w, "Server Not Found", http.StatusInternalServerError)
		return
	}

	field := vars["field"]
	indexStr := vars["index"]
	if indexStr == "" || indexStr == "undefined" {
		http.Error(w, "Index not provided", http.StatusInternalServerError)
		return
	}

	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 {
		http.Error(w, "Invalid index: "+indexStr, http.StatusInternalServerError)
		return
	}

	switch field {
	case "routes":
		gwUnlock := func() {}
		gw := strings.ToLower(strings.TrimSpace(mycluster.Conf.Cloud18GatewayService))
		if gw != "" {
			gwMu := repman.getGatewayMutex(gw)
			gwMu.Lock()
			gwUnlock = gwMu.Unlock
		}
		node.Lock()
		if index >= len(node.AppConfig.Deployment.Routes) {
			node.Unlock()
			gwUnlock()
			http.Error(w, "Index out of range for routes", http.StatusInternalServerError)
			return
		}
		node.AppConfig.Deployment.Routes = append(node.AppConfig.Deployment.Routes[:index], node.AppConfig.Deployment.Routes[index+1:]...)
		node.AppConfig.Deployment.EnforceSinglePrimary()
		node.Unlock()
		gwUnlock()
		// DNS cleanup for dropped managed host routes happens via the full
		// reconcile path (update-routes / provision), not here.
	case "variables":
		if index >= len(node.AppConfig.Deployment.Variables) {
			http.Error(w, "Index out of range for variables", http.StatusInternalServerError)
			return
		}
		if node.AppConfig.Deployment.Variables[index].Locked {
			http.Error(w, "Unable to drop locked variable. Please drop the source of the variable instead.", http.StatusInternalServerError)
			return
		}
		varName := node.AppConfig.Deployment.Variables[index].Name
		if refs := routesReferencingSecretVar(node.AppConfig.Deployment.Routes, varName); len(refs) > 0 {
			http.Error(w, fmt.Sprintf("cannot delete variable %q: referenced as auth-secret-var in %d route(s) — clear the monitor config first", varName, len(refs)), http.StatusBadRequest)
			return
		}
		node.AppConfig.Deployment.Variables = append(node.AppConfig.Deployment.Variables[:index], node.AppConfig.Deployment.Variables[index+1:]...)
	case "paths":
		if index >= len(node.AppConfig.Deployment.Paths) {
			http.Error(w, "Index out of range for path", http.StatusInternalServerError)
			return
		}

		node.AppConfig.Deployment.Paths = append(node.AppConfig.Deployment.Paths[:index], node.AppConfig.Deployment.Paths[index+1:]...)
		node.AppConfig.Deployment.SortPaths()
	default:
		http.Error(w, "Invalid field", http.StatusInternalServerError)
		return
	}

	mycluster.EnqueueRefreshAppTemplateMD5(node)

	// If we reach here, the row was successfully removed
	mycluster.ConfigManager.SaveConfig(mycluster, false)
	repman.RecomputeGatewayConflicts(mycluster.Name, "")
	w.Write([]byte("Deployment field row removed"))
}

// @Summary Add Storage to Deployment
// @Description Add a new storage to the deployment for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param field path string true "Field to add storage to (gitClones, localDirectories, sharedDirectories, s3Mounts)"
// @Param body body any true "Array of objects depending on field: - git: []config.GitClone - local: []config.Volume - shared: []config.Volume - s3: []config.S3Mapping"
// @Success 200 {string} string "Storage added successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/storages/{field}/add [post]
// This endpoint adds a new storage to the deployment for a given cluster and app.
func (repman *ReplicationManager) handlerMuxAddStorage(w http.ResponseWriter, r *http.Request) {
	var err error
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	node := mycluster.GetAppFromName(vars["appName"])
	if node == nil {
		http.Error(w, "Server Not Found", http.StatusInternalServerError)
		return
	}
	deployment := node.AppConfig.Deployment
	switch vars["field"] {
	case "gitClones":
		var row *config.GitClone
		row, err = decodeStruct[config.GitClone](r, w, "git clone")
		if err != nil {
			http.Error(w, "Error decoding git clone: "+err.Error(), http.StatusInternalServerError)
			return
		}

		old, _ := node.GetGitClone(row.Name)
		if old != nil {
			http.Error(w, "Cannot duplicate git clone with same name", http.StatusInternalServerError)
			return
		}

		if row.GitPass != "" {
			row.GitPass = mycluster.Conf.GetEncryptedString(mycluster.Conf.GetDecryptedPassword(row.Name, row.GitPass))
		}

		err := deployment.InsertGitClone(row)
		if err != nil {
			http.Error(w, "Error inserting git clone: "+err.Error(), http.StatusInternalServerError)
			return
		}
	case "volumes":
		var row *config.Volume
		row, err = decodeStruct[config.Volume](r, w, "volume mapping")
		if err != nil {
			http.Error(w, "Error decoding volume mapping: "+err.Error(), http.StatusInternalServerError)
			return
		}

		err := deployment.InsertVolume(row, node.AppConfig.AppConfigVersion)
		if err != nil {
			http.Error(w, "Error inserting volume mapping: "+err.Error(), http.StatusInternalServerError)
			return
		}
	case "s3Mounts":
		var row *config.S3Mount
		row, err = decodeStruct[config.S3Mount](r, w, "S3 directory")
		if err != nil {
			http.Error(w, "Error decoding S3 directory: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if err := hydrateS3MountFromProvider(mycluster, row); err != nil {
			http.Error(w, "Error resolving provider-linked S3 mount: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Story 6.7 – compatibility fix: allow custom-endpoint mounts to be created even when
		// GetAppByURL returns nil (i.e., the endpoint does not resolve to a sibling app in this cluster).
		// This path is reached by mounts that carry their own credentials (copied from a saved provider
		// or entered manually via the frontend). We only enforce sibling-app credential derivation when
		// the endpoint *does* resolve to an app. When it doesn't but is non-empty, treat it as custom.
		s3node, _ := mycluster.GetAppByURL(row.Endpoint)
		if s3node == nil && row.Endpoint != "" {
			// Custom-endpoint mount: row.Endpoint is set but no sibling app found.
			// Standalone mounts must provide credentials locally; provider-linked mounts
			// resolve credentials server-side from providerName above.
			if strings.TrimSpace(row.ProviderName) == "" {
				if err := validateStandaloneCustomEndpointCredentials(row.AccessKey, row.SecretKey); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
			} else if err := validateCustomEndpointCredentialPair(row.AccessKey, row.SecretKey); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else if s3node == nil {
			// Endpoint is empty and no sibling app found – require a valid app endpoint.
			http.Error(w, "S3 endpoint app not found: "+row.Endpoint, http.StatusInternalServerError)
			return
		}

		if row.Name == "" && row.Bucket != "" && row.Endpoint != "" {
			// Generate a unique name for the S3 mount if not provided.
			// For sibling-app mounts s3node carries the app name; for custom-endpoint
			// mounts s3node is nil so we derive the name from the bucket alone.
			if s3node != nil {
				row.Name = fmt.Sprintf("s3-%s-%s", s3node.Name, row.Bucket)
			} else {
				row.Name = fmt.Sprintf("s3-custom-%s", row.Bucket)
			}
		}

		if s3node != nil && strings.TrimSpace(row.ProviderName) == "" {
			// Derive credentials from sibling app only when endpoint resolved to an app.
			acckey, err := s3node.AppConfig.Deployment.GetVariableByName("MINIO_ROOT_USER", false)
			if err != nil || acckey == nil {
				http.Error(w, "S3 endpoint app does not have MINIO_ROOT_USER variable set", http.StatusInternalServerError)
				return
			}
			row.AccessKey = acckey.Value

			secretkey, err := s3node.AppConfig.Deployment.GetVariableByName("MINIO_ROOT_PASSWORD", false)
			if err != nil || secretkey == nil {
				http.Error(w, "S3 endpoint app does not have MINIO_ROOT_PASSWORD variable set", http.StatusInternalServerError)
				return
			}

			// Contract: mount SecretKey remains encrypted at rest in app config/API payloads.
			// The sibling app variable may arrive plaintext or encrypted depending on source;
			// GetDecryptedPassword normalizes to plaintext first, then GetEncryptedString
			// re-encrypts for this mount storage slot.
			row.SecretKey = mycluster.Conf.GetEncryptedString(mycluster.Conf.GetDecryptedPassword(row.Name, secretkey.Value))

			region, _ := s3node.AppConfig.Deployment.GetVariableByName("REGION", false)
			if region != nil {
				row.Region = region.Value
			}
		}

		if row.VolumeName == "" {
			// Phase 14 task 1: legacy-compatible autofill, only when S3 placement is unspecified.
			row.Volume, err = mycluster.SetAppLocalMountVolume(node)
			if err != nil {
				http.Error(w, "Volume selection required before adding storage: "+err.Error(), http.StatusBadRequest)
				return
			}
			row.VolumeName = row.Volume.Name
			row.VolumeDir = filepath.Join(row.Volume.S3MountSubdir(), row.Name)
		} else {
			// Phase 14 task 2/3: explicit V2 placement - resolve and preserve the
			// selected saved volume row, defaulting VolumeDir via S3MountSubdir()
			// (mnt as suggestion only, task 4) when the caller picked a volume but
			// left the directory unspecified.
			row.Volume, err = deployment.GetVolumeByName(row.VolumeName)
			if err != nil {
				http.Error(w, "Error getting volume by name: "+err.Error(), http.StatusBadRequest)
				return
			}
			if row.VolumeDir == "" {
				row.VolumeDir = filepath.Join(row.Volume.S3MountSubdir(), row.Name)
			} else if volumeHasDirectoryToken(row.Volume, row.VolumeDir) {
				// Phase 16: the "Add new" form has no Name field yet, so an
				// explicit directory-token choice with no subdirectory is
				// submitted as the bare token (e.g. "data"). Append the
				// generated mount name so this explicit token choice is
				// preserved instead of being mistaken for "unspecified".
				row.VolumeDir = filepath.Join(row.VolumeDir, row.Name)
			}
		}

		err = deployment.InsertS3Mount(row)
		if err != nil {
			http.Error(w, "Error inserting S3 mapping: "+err.Error(), http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "Invalid storage type", http.StatusInternalServerError)
		return
	}

	mycluster.EnqueueRefreshAppTemplateMD5(node)

	mycluster.ConfigManager.SaveConfig(mycluster, false)
	w.Write([]byte("Storage added successfully"))
}

// @Summary Modify Storage Field
// @Description Modify a specific field in a storage for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param field path string true "Field to modify (gitClones, localDirectories, sharedDirectories, s3Mounts)"
// @Param index path string true "Index of the field to modify"
// @Param key path string true "Key of the field to modify"
// @Param value body object{value=any} true "New value for the field"
// @Success 200 {string} string "Storage field modified"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/storages/{field}/index/{index}/{key}/modify [post]
// This endpoint modifies a specific field in a storage for a given cluster and app.
func (repman *ReplicationManager) handlerMuxModifyStorageField(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			var newValue string

			type FieldValue struct {
				Value string `json:"value"`
			}

			var body FieldValue
			err := json.NewDecoder(r.Body).Decode(&body)
			if err != nil {
				http.Error(w, "Error decoding JSON: "+err.Error(), http.StatusInternalServerError)
				return
			}

			newValue = body.Value

			if vars["index"] == "" || vars["index"] == "undefined" {
				http.Error(w, "Index not provided", http.StatusInternalServerError)
				return
			}

			index, err := strconv.Atoi(vars["index"])
			if err != nil {
				http.Error(w, "Error parsing index: "+err.Error(), http.StatusInternalServerError)
				return
			}

			if index < 0 {
				http.Error(w, "Index cannot be negative", http.StatusBadRequest)
				return
			}

			if vars["key"] == "" || vars["key"] == "undefined" {
				// For gitClones, variables, and path, key is required
				http.Error(w, "Key not provided", http.StatusBadRequest)
				return
			}

			deployment := node.AppConfig.Deployment

			switch vars["field"] {
			// fields which are arrays of objects
			case "gitClones":
				if index >= len(deployment.Storages.GitClones) {
					http.Error(w, "Index out of range for gitClones", http.StatusInternalServerError)
					return
				}

				// Get the git clone at the specified index
				gc := deployment.Storages.GitClones[index]
				if gc == nil {
					http.Error(w, "Git clone not found at index "+vars["index"], http.StatusInternalServerError)
					return
				}

				// Modify field based on key
				switch vars["key"] {
				case "name":
					if gc.Name == newValue {
						http.Error(w, "Name is the same as the current name", http.StatusInternalServerError)
						return
					}
					if newValue == "" {
						http.Error(w, "Name cannot be empty", http.StatusInternalServerError)
						return
					}
					old, _ := node.GetGitClone(newValue)
					if old != nil {
						http.Error(w, "Cannot duplicate git clone with same name", http.StatusInternalServerError)
						return
					}

					paths := deployment.GetGitPaths(gc.Name)
					gc.Name = newValue
					for _, p := range paths {
						p.SourceName = newValue
						deployment.ResolvePath(p)
					}

				case "repo":
					if gc.GitRepo == newValue {
						http.Error(w, "Repo is the same as the current repo", http.StatusInternalServerError)
						return
					}
					if newValue == "" {
						http.Error(w, "Repo cannot be empty", http.StatusInternalServerError)
						return
					}
					gc.GitRepo = newValue

				case "branch":
					if gc.GitBranch == newValue {
						http.Error(w, "Branch is the same as the current branch", http.StatusInternalServerError)
						return
					}

					if newValue == "" {
						http.Error(w, "Branch cannot be empty", http.StatusInternalServerError)
						return
					}

					gc.GitBranch = newValue

				case "volumename":
					if gc.VolumeName == newValue {
						http.Error(w, "VolumeName is the same as the current volume name", http.StatusInternalServerError)
						return
					}

					if newValue == "" {
						http.Error(w, "VolumeName cannot be empty", http.StatusInternalServerError)
						return
					}

					newvol, err := deployment.GetVolumeByName(newValue)
					if err != nil {
						http.Error(w, "Error getting volume by name: "+err.Error(), http.StatusInternalServerError)
						return
					}
					// Check for duplicate git volume path
					if deployment.HasDuplicateGitVolumePath(gc.Name, newValue, gc.VolumeDir) {
						gc.VolumeDir = filepath.Join(newvol.DefaultSubdir(), gc.Name)
					}
					gc.VolumeName = newValue
					gc.Volume = newvol

					deployment.ResolveGitPaths(gc.Name)

				case "volumedir":
					if newValue == "" {
						curvol, err := deployment.GetVolumeByName(gc.VolumeName)
						if err != nil {
							http.Error(w, "Error getting volume by name: "+err.Error(), http.StatusInternalServerError)
							return
						}
						newValue = filepath.Join(curvol.DefaultSubdir(), gc.Name)
					}

					if gc.VolumeDir == newValue {
						http.Error(w, "VolumeDir is the same as the current volume dir", http.StatusInternalServerError)
						return
					}

					if deployment.HasDuplicateGitVolumePath(gc.Name, gc.VolumeName, newValue) {
						http.Error(w, "Duplicate value for git volume path", http.StatusInternalServerError)
						return
					}
					gc.VolumeDir = newValue

					deployment.ResolveGitPaths(gc.Name)
				case "user":
					if gc.GitUser == newValue {
						http.Error(w, "User is the same as the current user", http.StatusInternalServerError)
						return
					}
					gc.GitUser = newValue
				case "pass":
					if mycluster.Conf.GetDecryptedPassword(gc.Name, gc.GitPass) == newValue {
						http.Error(w, "Password is the same as the current password", http.StatusInternalServerError)
						return
					}

					gc.GitPass = mycluster.Conf.GetEncryptedString(newValue)
				default:
					http.Error(w, "Invalid key for gitClones", http.StatusInternalServerError)
					return
				}
			case "volumes":
				if index >= len(deployment.Storages.Volumes) {
					http.Error(w, "Index out of range for volumes", http.StatusInternalServerError)
					return
				}

				// Get the volume at the specified index
				vol := deployment.Storages.Volumes[index]
				if vol == nil {
					http.Error(w, "Volume not found at index "+vars["index"], http.StatusInternalServerError)
					return
				}

				// Modify field based on key
				switch vars["key"] {
				case "name":
					if vol.Name == newValue {
						http.Error(w, "Name is the same as the current name", http.StatusInternalServerError)
						return
					}

					if newValue == "" {
						http.Error(w, "Name cannot be empty", http.StatusInternalServerError)
						return
					}

					old, _ := deployment.GetVolumeByName(newValue)
					if old != nil {
						http.Error(w, "Cannot duplicate volume with same name", http.StatusInternalServerError)
						return
					}

					oldName := vol.Name
					paths := deployment.GetVolumePaths(oldName)
					vol.Name = newValue
					for _, p := range paths {
						if p.SourceType == config.SourceVolume && p.SourceName == oldName {
							p.SourceName = vol.Name
						}
						deployment.ResolvePath(p)
					}

				case "poolname":
					if vol.PoolName == newValue {
						http.Error(w, "PoolName is the same as the current pool name", http.StatusInternalServerError)
						return
					}

					if newValue == "" {
						http.Error(w, "PoolName cannot be empty", http.StatusInternalServerError)
						return
					}

					// A row is canonical per pool for V1/unflagged content: reject
					// moving this row onto a pool that another saved row already
					// owns. V2 apps allow intentional multiple rows per pool.
					if node.AppConfig.AppConfigVersion < config.AppConfigVersionV2 {
						if existing := deployment.GetVolumeByPool(newValue); existing != nil && existing != vol {
							http.Error(w, fmt.Sprintf("a volume for pool %q already exists: %s", newValue, existing.Name), http.StatusBadRequest)
							return
						}
					}

					oldPoolName := vol.PoolName
					vol.PoolName = newValue
					if err := vol.Validate(); err != nil {
						vol.PoolName = oldPoolName
						http.Error(w, "Error validating volume: "+err.Error(), http.StatusInternalServerError)
						return
					}

				case "volumedir":
					normalized := config.NormalizeVolumeDirs(newValue)
					if normalized == vol.VolumeDir {
						http.Error(w, "VolumeDir is the same as the current volume dir", http.StatusInternalServerError)
						return
					}
					if normalized == "" {
						http.Error(w, "VolumeDir cannot be empty", http.StatusInternalServerError)
						return
					}

					oldVolumeDir := vol.VolumeDir
					vol.VolumeDir = normalized
					if err := vol.Validate(); err != nil {
						vol.VolumeDir = oldVolumeDir
						http.Error(w, "Error validating volume: "+err.Error(), http.StatusInternalServerError)
						return
					}

					deployment.ResolveVolumePaths(vol.Name)

				case "size":
					normalized, err := config.NormalizeVolumeSize(newValue)
					if err != nil {
						http.Error(w, "Invalid size: "+err.Error(), http.StatusBadRequest)
						return
					}
					vol.Size = normalized

				default:
					http.Error(w, "Invalid key for volumes", http.StatusInternalServerError)
					return
				}
			case "s3Mounts":
				if index >= len(deployment.Storages.S3Mounts) {
					http.Error(w, "Index out of range for s3Mounts", http.StatusInternalServerError)
					return
				}

				s3Mount := deployment.Storages.S3Mounts[index]
				if s3Mount == nil {
					http.Error(w, "S3 mount not found at index "+vars["index"], http.StatusInternalServerError)
					return
				}

				switch vars["key"] {
				case "name":
					if s3Mount.Name == newValue {
						http.Error(w, "Name is the same as the current name", http.StatusInternalServerError)
						return
					}

					if newValue == "" {
						http.Error(w, "Name cannot be empty", http.StatusInternalServerError)
						return
					}

					old, _ := deployment.GetS3Mount(newValue)
					if old != nil {
						http.Error(w, "Cannot duplicate volume with same name", http.StatusInternalServerError)
						return
					}

					paths := deployment.GetS3MountPaths(s3Mount.Name)
					s3Mount.Name = newValue
					for _, p := range paths {
						p.SourceName = newValue
						deployment.ResolvePath(p)
					}

				case "endpoint":
					if s3Mount.Endpoint == newValue {
						http.Error(w, "Endpoint is the same as the current endpoint", http.StatusInternalServerError)
						return
					}

					if newValue == "" {
						http.Error(w, "Endpoint cannot be empty", http.StatusInternalServerError)
						return
					}

					// Story 6.7 – compatibility fix: when the new endpoint does not resolve to a sibling app
					// (custom-endpoint mount), skip credential derivation and preserve the existing
					// AccessKey/SecretKey on the mount. The mount's own Endpoint field carries the
					// effective value for provisioning.
					s3node, _ := mycluster.GetAppByURL(newValue)
					if s3node != nil {
						// Sibling-app endpoint: derive credentials from the app's variables.
						acckey, err := s3node.AppConfig.Deployment.GetVariableByName("MINIO_ROOT_USER", false)
						if err != nil || acckey == nil {
							http.Error(w, "S3 endpoint app does not have MINIO_ROOT_USER variable set", http.StatusInternalServerError)
							return
						}

						secretkey, err := s3node.AppConfig.Deployment.GetVariableByName("MINIO_ROOT_PASSWORD", false)
						if err != nil || secretkey == nil {
							http.Error(w, "S3 endpoint app does not have MINIO_ROOT_PASSWORD variable set", http.StatusInternalServerError)
							return
						}

						s3Mount.AccessKey = acckey.Value
						s3Mount.SecretKey = mycluster.Conf.GetEncryptedString(mycluster.Conf.GetDecryptedPassword(s3Mount.Name, secretkey.Value))

						region, _ := s3node.AppConfig.Deployment.GetVariableByName("REGION", false)
						if region != nil {
							s3Mount.Region = region.Value
						} else {
							s3Mount.Region = ""
						}
					}
					// When s3node is nil (custom endpoint): keep existing s3Mount.AccessKey/SecretKey.
					if s3node == nil {
						if strings.TrimSpace(s3Mount.ProviderName) == "" {
							if err := validateStandaloneCustomEndpointCredentials(s3Mount.AccessKey, s3Mount.SecretKey); err != nil {
								http.Error(w, err.Error(), http.StatusBadRequest)
								return
							}
						} else if err := validateCustomEndpointCredentialPair(s3Mount.AccessKey, s3Mount.SecretKey); err != nil {
							http.Error(w, err.Error(), http.StatusBadRequest)
							return
						}
					}

					s3Mount.Endpoint = newValue

				case "bucket":
					if s3Mount.Bucket == newValue {
						http.Error(w, "Bucket is the same as the current bucket", http.StatusInternalServerError)
						return
					}
					if newValue == "" {
						http.Error(w, "Bucket cannot be empty", http.StatusInternalServerError)
						return
					}
					s3Mount.Bucket = newValue
				case "volumename":
					if s3Mount.VolumeName == newValue {
						http.Error(w, "VolumeName is the same as the current volume name", http.StatusInternalServerError)
						return
					}

					if newValue == "" {
						http.Error(w, "VolumeName cannot be empty", http.StatusInternalServerError)
						return
					}

					newvol, err := deployment.GetVolumeByName(newValue)
					if err != nil {
						http.Error(w, "Error getting volume by name: "+err.Error(), http.StatusInternalServerError)
						return
					}

					if deployment.HasDuplicateS3VolumePath(newValue, s3Mount.VolumeDir) {
						s3Mount.VolumeDir = filepath.Join(newvol.S3MountSubdir(), s3Mount.Name)
					}
					s3Mount.VolumeName = newValue
					s3Mount.Volume = newvol

					deployment.ResolveS3MountPaths(s3Mount.Name)
				case "region":
					s3Mount.Region = newValue

				case "providername":
					s3Mount.ProviderName = newValue

				case "volumedir":
					if s3Mount.VolumeDir == newValue {
						http.Error(w, "VolumeDir is the same as the current volume dir", http.StatusInternalServerError)
						return
					}

					if newValue == "" {
						curvol, err := deployment.GetVolumeByName(s3Mount.VolumeName)
						if err != nil {
							http.Error(w, "Error getting volume by name: "+err.Error(), http.StatusInternalServerError)
							return
						}
						newValue = filepath.Join(curvol.S3MountSubdir(), s3Mount.Name)
					}

					if deployment.HasDuplicateS3VolumePath(s3Mount.VolumeName, newValue) {
						http.Error(w, "Duplicate value for S3 volume path", http.StatusInternalServerError)
						return
					}
					s3Mount.VolumeDir = newValue

					deployment.ResolveS3MountPaths(s3Mount.Name)
				default:
					http.Error(w, "Invalid key for s3Mounts", http.StatusInternalServerError)
					return
				}

				if err := hydrateS3MountFromProvider(mycluster, s3Mount); err != nil {
					http.Error(w, "Error resolving provider-linked S3 mount: "+err.Error(), http.StatusBadRequest)
					return
				}
				if strings.TrimSpace(s3Mount.ProviderName) == "" {
					s3node, _ := mycluster.GetAppByURL(s3Mount.Endpoint)
					if s3node == nil && strings.TrimSpace(s3Mount.Endpoint) != "" {
						if err := validateStandaloneCustomEndpointCredentials(s3Mount.AccessKey, s3Mount.SecretKey); err != nil {
							http.Error(w, err.Error(), http.StatusBadRequest)
							return
						}
					}
				}
			default:
				http.Error(w, "Invalid field", http.StatusInternalServerError)
				return
			}

			mycluster.EnqueueRefreshAppTemplateMD5(node)

			mycluster.ConfigManager.SaveConfig(mycluster, false)
			w.Write([]byte("Storage field modified"))
		} else {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Drop Storage Field Row
// @Description Drop a specific row from a field in a storage for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param field path string true "Field to drop a row from (gitClones, localDirectories, sharedDirectories, s3Mounts)"
// @Param index path string true "Index of the row to drop"
// @Success 200 {string} string "Storage field row removed"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/storages/{field}/index/{index}/drop [post]
// This endpoint drops a specific row from a field in a storage for a given cluster and app.
func (repman *ReplicationManager) handlerMuxDropStorageFieldRow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	node := mycluster.GetAppFromName(vars["appName"])
	if node == nil {
		http.Error(w, "Server Not Found", http.StatusInternalServerError)
		return
	}

	field := vars["field"]
	indexStr := vars["index"]
	if indexStr == "" || indexStr == "undefined" {
		http.Error(w, "Index not provided", http.StatusInternalServerError)
		return
	}

	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 {
		http.Error(w, "Invalid index: "+indexStr, http.StatusInternalServerError)
		return
	}

	deployment := node.AppConfig.Deployment
	switch field {
	case "gitClones":
		if index >= len(deployment.Storages.GitClones) {
			http.Error(w, "Index out of range for gitClones", http.StatusInternalServerError)
			return
		}
		err := deployment.DropGitClone(deployment.Storages.GitClones[index])
		if err != nil {
			http.Error(w, "Error dropping git clone: "+err.Error(), http.StatusInternalServerError)
			return
		}
	case "volumes":
		if index >= len(deployment.Storages.Volumes) {
			http.Error(w, "Index out of range for volumes", http.StatusInternalServerError)
			return
		}
		err := deployment.DropVolume(deployment.Storages.Volumes[index])
		if err != nil {
			http.Error(w, "Error dropping volume: "+err.Error(), http.StatusInternalServerError)
			return
		}
	case "s3Mounts":
		if index >= len(deployment.Storages.S3Mounts) {
			http.Error(w, "Index out of range for s3Mounts", http.StatusInternalServerError)
			return
		}
		err := deployment.DropS3Mount(deployment.Storages.S3Mounts[index])
		if err != nil {
			http.Error(w, "Error dropping S3 mount: "+err.Error(), http.StatusInternalServerError)
			return
		}
	// Add more cases for other storage fields as needed
	default:
		http.Error(w, "Invalid field", http.StatusInternalServerError)
		return
	}

	mycluster.EnqueueRefreshAppTemplateMD5(node)

	// If we reach here, the row was successfully removed
	mycluster.ConfigManager.SaveConfig(mycluster, false)
	w.Write([]byte("Storage field row removed"))
}

// @Summary Set App Setting
// @Description Set a specific setting for a given app in a cluster
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appId path string true "App ID"
// @Param setting path string true "Setting to set"
// @Param value path string true "Value to set for the setting"
// @Success 200 {string} string "Setting updated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Server Not Found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appId}/settings/actions/set/{setting}/{value} [post]
func (repman *ReplicationManager) handlerMuxAppSetSetting(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		node := mycluster.GetAppFromName(vars["appId"])
		if node != nil {
			// Validate setting and value
			if vars["setting"] == "" || vars["value"] == "" {
				http.Error(w, "Setting and value must be provided", http.StatusBadRequest)
				return
			}

			setting := vars["setting"]
			value := vars["value"]
			err := node.SetSetting(setting, value)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error setting %s: %s", setting, err.Error()), http.StatusInternalServerError)
				return
			}

			mycluster.ConfigManager.SaveConfig(mycluster, false)
			w.Write([]byte("Setting updated successfully"))
		} else {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Switch App Setting
// @Description Switch a specific setting for a given app in a cluster
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appId path string true "App ID"
// @Param setting path string true "Setting to switch"
// @Success 200 {string} string "Setting switched successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Server Not Found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appId}/settings/actions/switch/{setting} [post]
func (repman *ReplicationManager) handlerMuxAppSwitchSetting(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		node := mycluster.GetAppFromName(vars["appId"])
		if node != nil {
			// Validate setting and value
			if vars["setting"] == "" {
				http.Error(w, "Setting must be provided", http.StatusBadRequest)
				return
			}

			setting := vars["setting"]
			err := node.SwitchSetting(setting)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error switch setting %s: %s", setting, err.Error()), http.StatusInternalServerError)
				return
			}
			mycluster.ConfigManager.SaveConfig(mycluster, false)
			w.Write([]byte("Setting switched successfully"))
		} else {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Clear App Setting
// @Description Clear a specific setting for a given app in a cluster
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appId path string true "App ID"
// @Param setting path string true "Setting to clear"
// @Success 200 {string} string "Setting cleared successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Server Not Found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appId}/settings/actions/clear/{setting} [post]
func (repman *ReplicationManager) handlerMuxAppClearSetting(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		node := mycluster.GetAppFromName(vars["appId"])
		if node != nil {
			// Validate setting
			if vars["setting"] == "" {
				http.Error(w, "Setting must be provided", http.StatusBadRequest)
				return
			}

			setting := vars["setting"]
			err := node.SetSetting(setting, "")
			if err != nil {
				http.Error(w, fmt.Sprintf("Error clearing setting %s: %s", setting, err.Error()), http.StatusInternalServerError)
				return
			}
			mycluster.ConfigManager.SaveConfig(mycluster, false)
			w.Write([]byte("Setting cleared successfully"))
		} else {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxGitRepoTree handles the HTTP request to get the tree structure of a Git repository.
// @Summary Get Git Repository Tree
// @Description Retrieves the tree structure of a specified Git repository.
// @Tags GitRepository
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appId path string true "App ID"
// @Param gitName path string true "Git Name"
// @Param force path string false "Force refresh of the repository tree"
// @Success 200 {object} treehelper.FileTreeCache "Git repository tree structure"
// @Failure 400 {string} string "Invalid Git repository URL"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error creating Git client" or "Error getting repository tree"
// @Router /api/clusters/{clusterName}/apps/{appId}/git/{gitName}/actions/get-repo-tree [get]
// @Router /api/clusters/{clusterName}/apps/{appId}/git/{gitName}/actions/get-repo-tree/{force} [get]
func (repman *ReplicationManager) handlerMuxGitRepoTree(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		force := strings.ToLower(vars["force"]) == "force"

		app := mycluster.GetAppFromName(vars["appId"])
		if app == nil {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}

		gc, _ := app.GetGitClone(vars["gitName"])
		if gc == nil {
			http.Error(w, "Git Clone Not Found", http.StatusInternalServerError)
			return
		}
		if isSSRFTarget(gc.GitRepo) {
			http.Error(w, "repo URL not allowed", http.StatusBadRequest)
			return
		}

		gitpass := mycluster.Conf.GetDecryptedPassword(gc.Name, gc.GitPass)

		cacheDir := filepath.Join(mycluster.WorkingDir, ".cache", "git", "repos")
		timeout := time.Duration(gc.Timeout) * time.Second
		if gc.Timeout <= 0 {
			timeout = 30 * time.Second // tree fetch downloads pack objects; needs at least as much time as a bare check
		}

		// All providers use GenericGitClient (go-git) over the git wire protocol.
		// No provider-specific REST API is needed for tree operations.
		gitClient := githelper.NewGenericGitClient(gc.GitUser, gitpass)

		tree, err := gitClient.GetRepositoryTree(cacheDir, gc.GitRepo, gc.GitBranch, timeout, force)
		if err != nil {
			http.Error(w, "Error getting repository tree: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tree)
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxGitCheckRepo validates a git repository URL, branch, and credentials
// before the user creates path mappings. Uses git ls-remote internally.
// @Summary Check Git Repository
// @Description Validates that a Git repository is reachable with the given credentials and that the branch exists.
// @Tags GitRepository
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {object} map[string]interface{} "Check result"
// @Failure 400 {string} string "Invalid request"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Internal error"
// @Router /api/clusters/{clusterName}/apps/{appName}/git/actions/check [post]
func (repman *ReplicationManager) handlerMuxGitCheckRepo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	type CheckRequest struct {
		Repo    string `json:"repo"`
		Branch  string `json:"branch"`
		User    string `json:"user"`
		Pass    string `json:"pass"`
		Timeout int    `json:"timeout"`
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req CheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Repo == "" {
		http.Error(w, "repo is required", http.StatusBadRequest)
		return
	}
	if isSSRFTarget(req.Repo) {
		http.Error(w, "repo URL not allowed", http.StatusBadRequest)
		return
	}
	if req.Branch == "" {
		req.Branch = "main"
	}

	timeout := time.Duration(req.Timeout) * time.Second
	if req.Timeout <= 0 {
		timeout = 30 * time.Second
	}

	type CheckResponse struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}

	gc := githelper.NewGenericGitClient(req.User, req.Pass)

	msg, err := gc.CheckRepo(req.Repo, req.Branch, timeout)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(CheckResponse{OK: false, Message: err.Error()})
	} else {
		json.NewEncoder(w).Encode(CheckResponse{OK: true, Message: msg})
	}
}

// handlerMuxGitCheckRepoByName checks an existing saved git clone using server-side
// credentials. The gitName path parameter is used to look up the clone from the app
// deployment; the password is decrypted server-side, so the frontend never needs to
// send a credential (which would be masked/encrypted in UI state anyway).
// @Summary Check Git Repository by Name
// @Tags GitRepository
// @Produce json
// @Param clusterName path string true "Cluster Name"
// @Param appId path string true "App ID"
// @Param gitName path string true "Git Clone Name"
// @Success 200 {object} map[string]interface{}
// @Failure 422 {object} map[string]interface{}
// @Router /api/clusters/{clusterName}/apps/{appId}/git/{gitName}/actions/check [post]
func (repman *ReplicationManager) handlerMuxGitCheckRepoByName(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	app := mycluster.GetAppFromName(vars["appId"])
	if app == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	gc, _ := app.GetGitClone(vars["gitName"])
	if gc == nil {
		http.Error(w, "Git clone not found", http.StatusNotFound)
		return
	}
	if isSSRFTarget(gc.GitRepo) {
		http.Error(w, "repo URL not allowed", http.StatusBadRequest)
		return
	}

	type CheckResponse struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}

	gitpass := mycluster.Conf.GetDecryptedPassword(gc.Name, gc.GitPass)
	timeout := time.Duration(gc.Timeout) * time.Second
	if gc.Timeout <= 0 {
		timeout = 30 * time.Second
	}

	branch := gc.GitBranch
	if branch == "" {
		branch = "main"
	}

	client := githelper.NewGenericGitClient(gc.GitUser, gitpass)
	msg, err := client.CheckRepo(gc.GitRepo, branch, timeout)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(CheckResponse{OK: false, Message: err.Error()})
	} else {
		json.NewEncoder(w).Encode(CheckResponse{OK: true, Message: msg})
	}
}

// @Summary Get App Service Config
// @Description Retrieves the OpenSVC service configuration for a specific app.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "OpenSVC service configuration"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error creating OpenSVC config template" or "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/service-opensvc [get]
func (repman *ReplicationManager) handlerMuxGetAppServiceConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		app := mycluster.GetAppFromName(vars["appName"])
		if app != nil {
			res, err := mycluster.OpenSVCGetAppTemplateV2(app)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't create OpenSVC config template  %s", err)
				http.Error(w, "Error creating OpenSVC config template: "+err.Error(), http.StatusInternalServerError)
				return
			}
			w.Write(res)
		} else {
			http.Error(w, "Not a valid app", http.StatusInternalServerError)
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Get App Substitution Variables
// @Description Retrieves the substitution variables for a specific app in a cluster.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "Substitution variables for the app"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No substitution variables defined for this app" or "Not a valid app" or "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/substitution [get]
func (repman *ReplicationManager) handlerMuxAppSubstitutionVariables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		app := mycluster.GetAppFromName(vars["appName"])
		if app != nil {
			if app.AppClusterSubstitute == "" {
				http.Error(w, "No substitution variables defined for this app", http.StatusInternalServerError)
				return
			}

			jsondata, _ := sjson.DeleteBytes([]byte(app.AppClusterSubstitute), "config.apps")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(jsondata)
		} else {
			http.Error(w, "Not a valid app", http.StatusInternalServerError)
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Save App to Template
// @Description Saves the app configuration to a template directory for a specific app in a cluster.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param templateName path string true "Template Name"
// @Success 200 {string} string "App template saved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Server Not Found" or "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/settings/actions/save-as-template/{templateName} [post]
// This endpoint saves the app configuration to a template directory for a specific app in a cluster.
func (repman *ReplicationManager) handlerMuxAppSaveAsTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			template := vars["templateName"]
			if template == "" {
				template = node.Name
			}
			templatePath := filepath.Join(mycluster.Conf.WorkingDir, ".templates", "apps", mycluster.Name, template+".toml")
			_, err := mycluster.SaveApp(node, templatePath)
			if err != nil {
				http.Error(w, "Error saving app template: "+err.Error(), http.StatusInternalServerError)
				return
			}
			w.Write([]byte("App template saved successfully"))
		} else {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

type appTemplateActionBody struct {
	Template     interface{} `json:"template"`
	ForceRefresh bool        `json:"forceRefresh"`
}

func decodeAppTemplateActionBody(r *http.Request) (string, bool, error) {
	var body appTemplateActionBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return "", false, err
	}

	templateName, err := normalizeTemplatePayloadName(body.Template)
	if err != nil {
		return "", body.ForceRefresh, err
	}
	if templateName == "" {
		return "", body.ForceRefresh, errors.New("template name must be provided")
	}

	return templateName, body.ForceRefresh, nil
}

func normalizeTemplatePayloadName(template interface{}) (string, error) {
	switch value := template.(type) {
	case string:
		return strings.TrimSpace(value), nil
	case map[string]interface{}:
		for _, key := range []string{"value", "name", "template", "label"} {
			raw, ok := value[key]
			if !ok {
				continue
			}

			s, ok := raw.(string)
			if ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s), nil
			}
		}
		return "", errors.New("template must be a string or an object with value/name/template/label")
	case nil:
		return "", nil
	default:
		return "", errors.New("template must be a string or an object")
	}
}

// @Summary Reset App from Template
// @Description Reloads the app template configuration for a specific app in a cluster.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "App template reloaded successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Server Not Found" or "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/settings/actions/reset-from-template [post]
func (repman *ReplicationManager) handlerMuxAppResetFromTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			templateName, forceRefresh, err := decodeAppTemplateActionBody(r)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error decoding request body: %v", err), http.StatusBadRequest)
				return
			}

			err = resetAppFromTemplateWithProjection(mycluster, node, templateName, forceRefresh)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error applying template: %v", err), http.StatusInternalServerError)
				return
			}

			mycluster.ConfigManager.SaveConfig(mycluster, false)
			w.Write([]byte("App template reloaded successfully"))
		} else {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Preview Reset App from Template Impact
// @Description Previews template-owned field changes that would be applied by reset-from-template.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {object} appTemplateResetPreview
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Server Not Found" or "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/settings/actions/reset-from-template/preview [post]
func (repman *ReplicationManager) handlerMuxAppResetFromTemplatePreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	node := mycluster.GetAppFromName(vars["appName"])
	if node == nil {
		http.Error(w, "Server Not Found", http.StatusInternalServerError)
		return
	}

	templateName, forceRefresh, err := decodeAppTemplateActionBody(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error decoding request body: %v", err), http.StatusBadRequest)
		return
	}

	tempConfig, err := buildValidatedTempAppConfigFromTemplate(mycluster, node, templateName, forceRefresh)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error applying template: %v", err), http.StatusInternalServerError)
		return
	}

	changes := buildTemplateProjectionImpact(node.AppConfig, tempConfig, templateName)
	preview := appTemplateResetPreview{
		TemplateName: templateName,
		ForceRefresh: forceRefresh,
		ChangeCount:  len(changes),
		Changes:      changes,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(preview)
}

func buildValidatedTempAppConfigFromTemplate(mycluster *cluster.Cluster, node *cluster.App, templateName string, forceRefresh bool) (*config.AppConfig, error) {
	var (
		content []byte
		err     error
	)

	if forceRefresh {
		content, err = mycluster.RefreshTemplateContent(templateName)
	} else {
		content, err = mycluster.GetTemplateContent(templateName)
	}
	if err != nil {
		return nil, err
	}

	parsedContent, err := mycluster.ParseTemplateContent(node, content)
	if err != nil {
		return nil, err
	}

	canonicalContent, _, err := cluster.CanonicalizeAppContent(parsedContent, node.Name)
	if err != nil {
		return nil, err
	}

	newViper, err := mycluster.LoadTemplateToViper(canonicalContent)
	if err != nil {
		return nil, err
	}

	tempConfig := &config.AppConfig{Deployment: config.NewDeploymentConfig()}
	if err := newViper.Unmarshal(tempConfig); err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Error unmarshalling parsed template file %s: %s", templateName, err)
		return nil, err
	}

	if tempConfig.Deployment != nil {
		if resolveErrs := tempConfig.Deployment.ResolvePaths(); len(resolveErrs) > 0 {
			for _, resolveErr := range resolveErrs {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr,
					"App template %q deployment path resolution error: %v", templateName, resolveErr)
			}
			return nil, fmt.Errorf("invalid deployment path mapping for template %q", templateName)
		}
	}

	return tempConfig, nil
}

func resetAppFromTemplateWithProjection(mycluster *cluster.Cluster, node *cluster.App, templateName string, forceRefresh bool) error {
	tempConfig, err := buildValidatedTempAppConfigFromTemplate(mycluster, node, templateName, forceRefresh)
	if err != nil {
		return err
	}

	applyTemplateOwnedProjection(node.AppConfig, tempConfig, templateName)
	return nil
}

func applyTemplateOwnedProjection(dst, src *config.AppConfig, templateName string) {
	if dst == nil || src == nil {
		return
	}

	// Template ownership projection (Milestone 1):
	// - Preserved (live app identity / unrelated):
	//   AppHost, AppPort, AppHostsIPV6, AppDbUser, AppDbPass, AppDbSchema,
	//   AppS3Provider, ProvAppCreditUsed, ProvAppCreditPlanned.
	// - Template-owned (overwritten from validated template):
	//   Deployment, AppConfigVersion, ProvAppTemplate, ProvAppDockerImg,
	//   ProvAppDockerCmd, ProvAppType, ProvAppMem, ProvAppCpuCores,
	//   ProvAppDisk, ProvAppDiskType, ProvAppRouteAddr, ProvAppRoutePort,
	//   ProvAppRouteMask, ProvAppAgents, ProvAppHATopology,
	//   ProvAppAgentsFailover.
	//
	// AppConfigVersion travels with Deployment: src.Deployment was
	// canonicalized under src.AppConfigVersion's V1/V2 gate
	// (CanonicalizeAppVolumesRaw), so leaving dst.AppConfigVersion at a stale
	// value would let the next load/save canonicalization pass
	// re-interpret an intentional V2 multi-row Deployment as V1 and merge it
	// back to one row per pool.
	dst.Deployment = src.Deployment
	dst.AppConfigVersion = src.AppConfigVersion
	dst.ProvAppTemplate = templateName
	dst.ProvAppDockerImg = src.ProvAppDockerImg
	dst.ProvAppDockerCmd = src.ProvAppDockerCmd
	dst.ProvAppType = src.ProvAppType
	dst.ProvAppMem = src.ProvAppMem
	dst.ProvAppCpuCores = src.ProvAppCpuCores
	dst.ProvAppDisk = src.ProvAppDisk
	dst.ProvAppDiskType = src.ProvAppDiskType
	dst.ProvAppRouteAddr = src.ProvAppRouteAddr
	dst.ProvAppRoutePort = src.ProvAppRoutePort
	dst.ProvAppRouteMask = src.ProvAppRouteMask
	dst.ProvAppAgents = src.ProvAppAgents
	dst.ProvAppHATopology = src.ProvAppHATopology
	dst.ProvAppAgentsFailover = src.ProvAppAgentsFailover
}

func buildTemplateProjectionImpact(current, projected *config.AppConfig, templateName string) []appTemplateFieldChange {
	if current == nil || projected == nil {
		return nil
	}

	deploymentSummary := func(d *config.Deployment) string {
		if d == nil {
			return "none"
		}
		if raw, err := json.Marshal(d); err == nil {
			return string(raw)
		}
		return fmt.Sprintf("paths=%d volumes=%d git=%d s3mounts=%d", len(d.Paths), len(d.Storages.Volumes), len(d.Storages.GitClones), len(d.Storages.S3Mounts))
	}

	changes := make([]appTemplateFieldChange, 0)
	appendIfChanged := func(field string, oldVal, newVal string) {
		if oldVal == newVal {
			return
		}
		changes = append(changes, appTemplateFieldChange{Field: field, Old: oldVal, New: newVal})
	}

	appendIfChanged("ProvAppTemplate", current.ProvAppTemplate, templateName)
	appendIfChanged("ProvAppDockerImg", current.ProvAppDockerImg, projected.ProvAppDockerImg)
	appendIfChanged("ProvAppDockerCmd", current.ProvAppDockerCmd, projected.ProvAppDockerCmd)
	appendIfChanged("ProvAppType", current.ProvAppType, projected.ProvAppType)
	appendIfChanged("ProvAppMem", current.ProvAppMem, projected.ProvAppMem)
	appendIfChanged("ProvAppCpuCores", current.ProvAppCpuCores, projected.ProvAppCpuCores)
	appendIfChanged("ProvAppDisk", current.ProvAppDisk, projected.ProvAppDisk)
	appendIfChanged("ProvAppDiskType", current.ProvAppDiskType, projected.ProvAppDiskType)
	appendIfChanged("ProvAppRouteAddr", current.ProvAppRouteAddr, projected.ProvAppRouteAddr)
	appendIfChanged("ProvAppRoutePort", current.ProvAppRoutePort, projected.ProvAppRoutePort)
	appendIfChanged("ProvAppRouteMask", current.ProvAppRouteMask, projected.ProvAppRouteMask)
	appendIfChanged("ProvAppAgents", current.ProvAppAgents, projected.ProvAppAgents)
	appendIfChanged("ProvAppHATopology", current.ProvAppHATopology, projected.ProvAppHATopology)
	appendIfChanged("ProvAppAgentsFailover", current.ProvAppAgentsFailover, projected.ProvAppAgentsFailover)
	appendIfChanged("Deployment", deploymentSummary(current.Deployment), deploymentSummary(projected.Deployment))

	return changes
}

// @Summary Resolve App Template Variable Values
// @Description Resolves the template variables for a specific app in a cluster.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param body body DecodedData true "Data to resolve in the template"
// @Success 200 {string} string "Resolved template variables"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error parsing template" or "Server Not Found" or "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/resolve-template [post]
// This endpoint resolves the template variables for a specific app in a cluster.
func (repman *ReplicationManager) handlerMuxAppResolveTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		var decodedData DecodedData

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Decode reading body :%s", err.Error()), http.StatusBadRequest)
			return
		}

		err = json.Unmarshal(body, &decodedData)
		if err != nil {
			http.Error(w, fmt.Sprintf("Decode body :%s. Err: %s", string(body), err.Error()), http.StatusBadRequest)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			newData, err := mycluster.ParseAppTemplate(decodedData.Data, node.AppClusterSubstitute)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error parsing template: %s", err), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			err = json.NewEncoder(w).Encode(newData)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error encoding response: %s", err.Error()), http.StatusInternalServerError)
				return
			}
		} else {
			http.Error(w, "Server Not Found", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Refresh App Template from Repo
// @Description Retrieves the tree structure of the application template repository for a specific cluster.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} treehelper.FileTreeCache "Application template repository tree structure"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error getting repository tree" or "No cluster"
// @Router /api/clusters/{clusterName}/actions/refresh-apps-template [get]
// This endpoint retrieves the tree structure of the application template repository for a specific cluster.
func (repman *ReplicationManager) handlerMuxAppRefreshTemplateFromRepo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		templates, _ := repman.GetAppTemplatesFromLocal(mycluster.Name)
		repolist, _ := mycluster.Conf.LoadAppTemplateListWithRefresh(true)
		for _, name := range repolist {
			if strings.TrimSpace(name) != "" {
				templates = append(templates, name)
			}
		}

		seen := make(map[string]bool)
		merged := make([]string, 0, len(templates))
		for _, name := range templates {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" || seen[trimmed] {
				continue
			}
			seen[trimmed] = true
			merged = append(merged, trimmed)
		}

		jsondata, err := json.Marshal(merged)
		if err != nil {
			http.Error(w, "Error getting repository tree: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsondata)
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary List App Templates with Metadata
// @Description Returns unified app template inventory with source/local metadata.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param forceRefresh query boolean false "Force refresh of pull-only repo cache before listing"
// @Success 200 {array} appTemplateSummary
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/templates/apps [get]
func (repman *ReplicationManager) handlerMuxAppTemplatesList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	forceRefresh := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("forceRefresh")), "true")
	templates, _ := repman.GetAppTemplatesFromLocal(mycluster.Name)
	repolist, _ := mycluster.Conf.LoadAppTemplateListWithRefresh(forceRefresh)
	templates = append(templates, "shared/dummy")
	for _, name := range repolist {
		if name != "" {
			templates = append(templates, name)
		}
	}

	seen := make(map[string]bool)
	result := make([]appTemplateSummary, 0, len(templates))
	for _, name := range templates {
		if strings.TrimSpace(name) == "" || seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, inferTemplateMetadata(mycluster, name))
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

// @Summary Get App Template Structure Guide
// @Description Returns TEMPLATE_STRUCTURE.md content for in-app guidance.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} appTemplateStructureGuideResponse
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "Template structure guide not found"
// @Failure 500 {string} string "No cluster" or "Error reading template structure guide"
// @Router /api/clusters/{clusterName}/templates/apps/structure-guide [get]
func (repman *ReplicationManager) handlerMuxAppTemplateStructureGuide(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	guidePath := filepath.Join(mycluster.Conf.ShareDir, "app", "templates", "TEMPLATE_STRUCTURE.md")
	content, err := os.ReadFile(guidePath)
	if err != nil {
		if templateStructureGuideReadErrorStatus(err) == http.StatusNotFound {
			http.Error(w, "Template structure guide not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("Error reading template structure guide: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(appTemplateStructureGuideResponse{Content: string(content)})
}

// @Summary Preview App Template Content
// @Description Returns canonical app template content for preview, optionally forcing source refresh.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param templateName path string true "Template Name"
// @Param forceRefresh query boolean false "Bypass local cache and refresh from source"
// @Success 200 {object} appTemplateContentResponse
// @Failure 400 {string} string "Template name must be provided"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/templates/apps/{templateName}/content [get]
func (repman *ReplicationManager) handlerMuxAppTemplateContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	normalizedTemplateName, _, pathErr := resolveTemplateCachePath(mycluster.Conf.WorkingDir, mycluster.Name, vars["templateName"])
	if pathErr != nil {
		http.Error(w, pathErr.Error(), http.StatusBadRequest)
		return
	}

	forceRefresh := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("forceRefresh")), "true")
	var (
		content []byte
		err     error
	)
	if forceRefresh {
		content, err = mycluster.RefreshTemplateContent(normalizedTemplateName)
	} else {
		content, err = mycluster.GetTemplateContent(normalizedTemplateName)
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting template content: %v", err), http.StatusInternalServerError)
		return
	}

	meta := inferTemplateMetadata(mycluster, normalizedTemplateName)
	resp := appTemplateContentResponse{
		Name:         normalizedTemplateName,
		Content:      string(content),
		Origin:       meta.Origin,
		HasLocalCopy: meta.HasLocalCopy,
		Refreshed:    forceRefresh,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// @Summary Save Local App Template Content
// @Description Saves editable local template content after canonicalization and strict validation.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param templateName path string true "Template Name"
// @Param body body object true "Template content payload"
// @Success 200 {string} string "Template saved successfully"
// @Failure 400 {string} string "Invalid request"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/templates/apps/{templateName}/content/actions/save [post]
func (repman *ReplicationManager) handlerMuxAppTemplateContentSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	templateName := vars["templateName"]
	localPath, err := resolveLocalTemplateWritePath(mycluster.Conf.WorkingDir, mycluster.Name, templateName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf("Error decoding request body: %v", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		http.Error(w, "Template content must be provided", http.StatusBadRequest)
		return
	}

	canonicalContent, _, err := cluster.CanonicalizeAppContent([]byte(body.Content), "")
	if err != nil {
		http.Error(w, fmt.Sprintf("Error canonicalizing template content: %v", err), http.StatusBadRequest)
		return
	}
	if err := validateCanonicalTemplateContentForSave(mycluster, templateName, canonicalContent); err != nil {
		http.Error(w, fmt.Sprintf("Error validating template content: %v", err), http.StatusBadRequest)
		return
	}

	if err := writeTemplateContentAtomically(localPath, canonicalContent); err != nil {
		http.Error(w, fmt.Sprintf("Error saving template content: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Template saved successfully"))
}

// @Summary Delete Local App Template Content
// @Description Deletes editable local template content.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param templateName path string true "Template Name"
// @Success 200 {string} string "Template deleted successfully"
// @Failure 400 {string} string "Invalid request"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "Template not found"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/templates/apps/{templateName}/content/actions/delete [post]
func (repman *ReplicationManager) handlerMuxAppTemplateContentDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	templateName := vars["templateName"]
	localPath, err := resolveLocalTemplateWritePath(mycluster.Conf.WorkingDir, mycluster.Name, templateName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		http.Error(w, "Template not found", http.StatusNotFound)
		return
	}
	if err := os.Remove(localPath); err != nil {
		http.Error(w, fmt.Sprintf("Error deleting template content: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Template deleted successfully"))
}

// @Summary Create Local Copy of App Template Content
// @Description Creates an editable local template copy from an existing template source/content.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param templateName path string true "Template Name"
// @Param body body object true "Create local copy payload"
// @Success 200 {string} string "Template local copy created successfully"
// @Failure 400 {string} string "Invalid request"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/templates/apps/{templateName}/content/actions/create-local-copy [post]
func (repman *ReplicationManager) handlerMuxAppTemplateContentCreateLocalCopy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	normalizedTemplateName, _, err := resolveTemplateCachePath(mycluster.Conf.WorkingDir, mycluster.Name, vars["templateName"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var body struct {
		LocalTemplateName string `json:"localTemplateName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf("Error decoding request body: %v", err), http.StatusBadRequest)
		return
	}
	if err := validateTemplateNameForLocalWrite(mycluster.Conf.WorkingDir, mycluster.Name, body.LocalTemplateName); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := createLocalTemplateCopyFromTemplate(mycluster, normalizedTemplateName, body.LocalTemplateName); err != nil {
		http.Error(w, fmt.Sprintf("Error creating local template copy: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Template local copy created successfully"))
}

func createLocalTemplateCopyFromTemplate(mycluster *cluster.Cluster, templateName, localTemplateName string) error {
	normalizedTemplateName, _, err := resolveTemplateCachePath(mycluster.Conf.WorkingDir, mycluster.Name, templateName)
	if err != nil {
		return err
	}
	if err := validateDummyTemplateRenamePolicy(normalizedTemplateName, localTemplateName); err != nil {
		return err
	}

	content, err := mycluster.GetTemplateContent(normalizedTemplateName)
	if err != nil {
		return err
	}

	canonicalContent, _, err := cluster.CanonicalizeAppContent(content, "")
	if err != nil {
		return err
	}
	if err := validateCanonicalTemplateContentForSave(mycluster, localTemplateName, canonicalContent); err != nil {
		return err
	}

	localPath, err := resolveLocalTemplateWritePath(mycluster.Conf.WorkingDir, mycluster.Name, localTemplateName)
	if err != nil {
		return err
	}
	if err := writeTemplateContentAtomically(localPath, canonicalContent); err != nil {
		return err
	}

	return nil
}

func validateDummyTemplateRenamePolicy(sourceTemplateName, localTemplateName string) error {
	if !cluster.IsSharedDummyTemplate(sourceTemplateName) {
		return nil
	}
	normalizedLocal, err := normalizeTemplateIdentifier(localTemplateName)
	if err != nil {
		return err
	}
	baseName := strings.TrimSpace(strings.ToLower(filepath.Base(normalizedLocal)))
	if baseName == "dummy" {
		return fmt.Errorf("please rename dummy template before creating local copy")
	}
	return nil
}

// @Summary Drop App Monitor
// @Description Drops the monitoring configuration for a specific app in a cluster.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appId path string true "App ID"
// @Success 200 {string} string "App monitor dropped successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No app found with the provided app ID" or "Cluster Not Found"
// @Router /api/clusters/{clusterName}/apps/{appHost}/{appPort}/actions/drop [post]
func (repman *ReplicationManager) handlerMuxAppDropByName(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		if vars["appHost"] == "" || vars["appPort"] == "" {
			http.Error(w, "No app host or port provided", http.StatusBadRequest)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive drop app monitor for %s:%s", vars["appHost"], vars["appPort"])
		err := mycluster.RemoveAppMonitor(vars["appHost"], vars["appPort"])
		if err != nil {
			http.Error(w, "Error dropping app monitor: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}

}
