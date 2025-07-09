package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Deployment struct {
	PrimaryRoute Route           `mapstructure:"-"  toml:"-" json:"primaryRoute" groups:"apps"`
	Routes       Routes          `mapstructure:"routes"  toml:"routes" json:"routes" groups:"apps"`
	Storages     *StorageMapping `mapstructure:"storages"  toml:"storages" json:"storages" groups:"apps"`
	Variables    VariableMaps    `mapstructure:"variables"  toml:"variables" json:"variables" groups:"apps"`
}

func NewDeploymentConfig() *Deployment {
	return &Deployment{
		Routes:    Routes{},
		Storages:  NewStorageMapping(),
		Variables: []VariableMapping{},
	}
}

type Routes []Route

type Route struct {
	CName    string `mapstructure:"cname"  toml:"cname" json:"cname" groups:"apps"`
	Port     string `mapstructure:"port"  toml:"port" json:"port" groups:"apps"`
	Protocol string `mapstructure:"protocol"  toml:"protocol" json:"protocol" options:"https|tcp" groups:"apps"`
	Primary  bool   `mapstructure:"primary"  toml:"primary" json:"primary" groups:"apps"`
}

type RouteStatus struct {
	Route
	Status string `mapstructure:"status"  toml:"status" json:"status"`
}

type VariableMaps []VariableMapping

type VariableMapping struct {
	Name        string  `mapstructure:"name" toml:"name" json:"name" groups:"apps"`
	Value       string  `mapstructure:"value" toml:"value" json:"value" groups:"apps"`
	Type        string  `mapstructure:"type" toml:"type" json:"type" options:"secret|env" groups:"apps"`
	Locked      bool    `mapstructure:"locked" toml:"locked" json:"locked" groups:"apps"`
	Conditional AVSlice `mapstructure:"conditional" toml:"conditional" json:"conditional" groups:"apps"` // This is used to set the variable value only if the agent matches
}

func NewStorageMapping() *StorageMapping {
	return &StorageMapping{
		GitClones:      make(GitClones, 0),
		S3Directories:  make(S3Mappings, 0),
		Volumes:        make(Volumes, 0),
		VolumeMappings: make(VolumeMappings, 0),
		Paths:          make(PathMaps, 0),
		Mutex:          sync.RWMutex{},
	}
}

type StorageMapping struct {
	GitClones      GitClones      `mapstructure:"git-clones" toml:"git-clones" json:"gitClones" groups:"apps"`
	S3Directories  S3Mappings     `mapstructure:"s3-directories" toml:"s3-directories" json:"s3Directories" groups:"apps"`
	Volumes        Volumes        `mapstructure:"volumes" toml:"volumes" json:"volumes" groups:"apps"`
	VolumeMappings VolumeMappings `mapstructure:"volume-mappings" toml:"volume-mappings" json:"volumeMappings" groups:"apps"`
	Paths          PathMaps       `mapstructure:"paths"  toml:"paths" json:"paths" groups:"apps"`

	// Use sync.RWMutex to protect concurrent access to Volumes and VolumeMappings
	Mutex sync.RWMutex
}

func (sm *StorageMapping) GetVolumeByName(name string) (*Volume, error) {
	// Use a mutex to protect concurrent access
	sm.Mutex.RLock()
	defer sm.Mutex.RUnlock()

	for _, v := range sm.Volumes {
		if v.Name == name {
			return v, nil
		}
	}
	return nil, fmt.Errorf("volume %s not found", name)
}

func (sm *StorageMapping) InsertVolume(v *Volume) error {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Validate the volume
	if v.Name == "" {
		return errors.New("volume name is required")
	}

	if v.VolumeDir == "" {
		return errors.New("volume directory is required")
	}

	// Check if the volume directory is valid
	if strings.Contains(v.VolumeDir, "..") {
		return fmt.Errorf("invalid volume directory: %s", v.VolumeDir)
	}

	// Check if the volume already exists
	for _, existingVolume := range sm.Volumes {
		if existingVolume.Name == v.Name {
			return fmt.Errorf("volume already exists: %s", v.Name)
		}

		if existingVolume.VolumeDir == v.VolumeDir && existingVolume.PoolName == v.PoolName {
			return fmt.Errorf("volume with same directory and pool already exists: %s", v.VolumeDir)
		}
	}

	// Add the new volume
	sm.Volumes = append(sm.Volumes, v)
	return nil
}

func (sm *StorageMapping) DropVolume(vol *Volume) error {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Find the volume by name
	var volume *Volume
	var vindex int
	for i, v := range sm.Volumes {
		if v.Name == vol.Name {
			volume = v
			vindex = i
			break
		}
	}

	if volume == nil {
		return fmt.Errorf("volume %s not found", vol.Name)
	}

	// Check for dependencies in VolumeMappings
	for _, p := range sm.VolumeMappings {
		if p.SourceName == volume.Name && p.Name != volume.Name {
			return fmt.Errorf("cannot drop volume %s, it is used in volume mappings", volume.Name)
		}
	}

	// Remove the volume mapping for the given name.
	// We iterate in reverse to avoid index shifting issues.
	for i := len(sm.VolumeMappings) - 1; i >= 0; i-- {
		if sm.VolumeMappings[i].SourceName == volume.Name {
			// Remove the volume mapping
			sm.VolumeMappings = append(sm.VolumeMappings[:i], sm.VolumeMappings[i+1:]...)
		}
	}

	// Remove the volume
	sm.Volumes = append(sm.Volumes[:vindex], sm.Volumes[vindex+1:]...)

	return nil
}

func (sm *StorageMapping) InsertVolumeMapping(vm *VolumeMapping) error {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Validate the volume mapping
	if err := vm.Validate(); err != nil {
		return err
	}

	// Check if the volume mapping already exists
	for _, existingVM := range sm.VolumeMappings {
		if existingVM.Name == vm.Name {
			return fmt.Errorf("volume mapping already exists: %s", vm.Name)
		}

		if existingVM.Name == vm.ParentName {
			vm.Parent = existingVM
		}
	}

	// Resolve the source for the new volume mapping
	if err := vm.Resolve(sm.Volumes, sm.GitClones, sm.S3Directories, sm.VolumeMappings); err != nil {
		return err
	}

	// Add the new volume mapping
	sm.VolumeMappings = append(sm.VolumeMappings, vm)

	return nil
}

func (sm *StorageMapping) DropVolumeMapping(volmap *VolumeMapping) error {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Prevent dropping volume mappings that are used in paths or other mappings
	for _, p := range sm.Paths {
		if p.SourceName == volmap.Name {
			return fmt.Errorf("cannot drop volume mapping %s, it is used in path mappings", volmap.Name)
		}
	}
	for _, vm := range sm.VolumeMappings {
		if vm.ParentName == volmap.Name {
			return fmt.Errorf("cannot drop volume mapping %s, it is used in other volume mappings", volmap.Name)
		}
	}

	// Find the volume mapping by name
	var index int = -1
	for i, vm := range sm.VolumeMappings {
		if vm.Name == volmap.Name {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("volume mapping %s not found", volmap.Name)
	}

	// Remove the volume mapping
	sm.VolumeMappings = append(sm.VolumeMappings[:index], sm.VolumeMappings[index+1:]...)

	return nil
}

func (sm *StorageMapping) ResolveVolumeMappings() {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	for i, vm := range sm.VolumeMappings {
		if err := vm.Resolve(sm.Volumes, sm.GitClones, sm.S3Directories, sm.VolumeMappings); err != nil {
			fmt.Printf("Error resolving volume mapping %s: %v\n", vm.Name, err)
		} else {
			sm.VolumeMappings[i] = vm // Update the resolved mapping
		}
	}
}

func (sm *StorageMapping) GetVolumeMapping(name string) (*VolumeMapping, error) {
	// Use a mutex to protect concurrent access
	sm.Mutex.RLock()
	defer sm.Mutex.RUnlock()

	for _, vm := range sm.VolumeMappings {
		if vm.Name == name {
			return vm, nil
		}
	}
	return nil, fmt.Errorf("volume mapping %s not found", name)
}

func (sm *StorageMapping) InsertGitClone(gc *GitClone) error {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Check if the git clone already exists
	for _, existingGC := range sm.GitClones {
		if existingGC.Name == gc.Name {
			return fmt.Errorf("git clone already exists: %s", gc.Name)
		}
	}

	// Add the new git clone
	sm.GitClones = append(sm.GitClones, gc)

	return nil
}

// DropGitClone removes a git clone by name.
func (sm *StorageMapping) DropGitClone(gc *GitClone) error {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Prevent dropping git clones that are used in volumes mappings
	for _, p := range sm.VolumeMappings {
		if p.SourceType == SourceGit && p.SourceName == gc.Name {
			return fmt.Errorf("cannot drop git clone %s, it is used in volume mappings", gc.Name)
		}
	}

	// Find the git clone by name
	var index int = -1
	for i, g := range sm.GitClones {
		if gc.Name == g.Name {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("git clone %s not found", gc.Name)
	}

	// Remove the git clone
	sm.GitClones = append(sm.GitClones[:index], sm.GitClones[index+1:]...)
	return nil
}

func (sm *StorageMapping) InsertPath(p PathMapping) error {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Check if the path already exists
	for _, existingPath := range sm.Paths {
		if existingPath.DockerPath == p.DockerPath {
			return fmt.Errorf("path mapping already exists: %s -> %s:%s", p.DockerPath, p.SourceName, p.SourcePath)
		}
	}

	// Resolve the source for the new path mapping
	for _, vm := range sm.VolumeMappings {
		if vm.Name == p.SourceName {
			p.Source = vm
			break
		}
	}

	// Add the new path mapping
	sm.Paths = append(sm.Paths, &p)

	return nil
}

func (sm *StorageMapping) ResolvePaths() {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	for i, p := range sm.Paths {
		for _, vm := range sm.VolumeMappings {
			if vm.Name == p.SourceName {
				sm.Paths[i].Source = vm
				break
			}
		}
	}
}

func (sm *StorageMapping) SortPaths() {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Sort the paths by source name and then by full host path
	sm.Paths.Sort()
}

func (sm *StorageMapping) DropPath(p PathMapping) error {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Find the path mapping by docker path
	var index int = -1
	for i, existingPath := range sm.Paths {
		if existingPath.DockerPath == p.DockerPath {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("path mapping %s not found", p.DockerPath)
	}

	// Remove the path mapping
	sm.Paths = append(sm.Paths[:index], sm.Paths[index+1:]...)

	return nil
}

func (sm *StorageMapping) GetPathMapping(dockerPath string) (*PathMapping, error) {
	// Use a mutex to protect concurrent access
	sm.Mutex.RLock()
	defer sm.Mutex.RUnlock()

	for _, p := range sm.Paths {
		if p.DockerPath == dockerPath {
			return p, nil
		}
	}
	return nil, fmt.Errorf("path mapping %s not found", dockerPath)
}

func (sm *StorageMapping) InsertS3Mapping(s3 *S3Mapping) error {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Check if the S3 mapping already exists
	for _, existingS3 := range sm.S3Directories {
		if existingS3.Name == s3.Name {
			return fmt.Errorf("S3 mapping already exists: %s", s3.Name)
		}
	}

	// Add the new S3 mapping
	sm.S3Directories = append(sm.S3Directories, s3)

	return nil
}

func (sm *StorageMapping) DropS3Mapping(s3 *S3Mapping) error {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Prevent dropping S3 mappings that are used in volume mappings
	for _, p := range sm.VolumeMappings {
		if p.SourceType == SourceS3 && p.SourceName == s3.Name {
			return fmt.Errorf("cannot drop S3 mapping %s, it is used in volume mappings", s3.Name)
		}
	}

	// Find the S3 mapping by name
	var index int = -1
	for i, existingS3 := range sm.S3Directories {
		if existingS3.Name == s3.Name {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("S3 mapping %s not found", s3.Name)
	}

	// Remove the S3 mapping
	sm.S3Directories = append(sm.S3Directories[:index], sm.S3Directories[index+1:]...)

	return nil
}

type PathMapping struct {
	DockerPath string         `mapstructure:"dockerpath" toml:"dockerpath" json:"dockerpath" groups:"apps"`
	SourceName string         `mapstructure:"srcname" toml:"srcname" json:"srcname" groups:"apps"`
	SourcePath string         `mapstructure:"srcpath" toml:"srcpath" json:"srcpath" groups:"apps"`
	Source     *VolumeMapping `mapstructure:"-" toml:"-" json:"-"`
}

// GetDockerMapping returns the Docker path mapping for the given PathMapping.
func (p PathMapping) GetDockerMapping() string {
	if p.Source == nil {
		return ""
	}

	return filepath.Join(p.Source.FullVolumePath(), p.SourcePath) + ":" + p.DockerPath
}

type PathMaps []*PathMapping

func (pm PathMaps) Sort() {
	// Sort the path mappings by source name and then by full host path
	sort.Slice(pm, func(i, j int) bool {
		if pm[i].Source == nil {
			return pm[i].SourceName < pm[j].SourceName
		}
		if pm[j].Source == nil {
			return pm[i].SourceName < pm[j].SourceName
		}
		return pm[i].Source.FullVolumePath() < pm[j].Source.FullVolumePath()
	})
}

type GitClones []*GitClone

type GitClone struct {
	Name      string `mapstructure:"name" toml:"name" json:"name" groups:"apps"`
	GitRepo   string `mapstructure:"repo" toml:"repo" json:"repo" groups:"apps"`
	GitBranch string `mapstructure:"branch" toml:"branch" json:"branch" groups:"apps"`
	VolumeDir string `mapstructure:"volumedir" toml:"volumedir" json:"volumedir" options:"etc|log|var" groups:"apps"`
	GitUser   string `mapstructure:"user" toml:"user" json:"user" groups:"apps"`
	GitPass   string `mapstructure:"pass" toml:"pass" json:"pass" groups:"apps"`
	Timeout   int    `mapstructure:"timeout" toml:"timeout" json:"timeout" groups:"apps"`
}

var gitVariableReplacer = strings.NewReplacer("-", "_", ".", "_", "/", "_")

const GitVarSuffixRepo = "REPO"
const GitVarSuffixBranch = "BRANCH"
const GitVarSuffixUser = "USER"
const GitVarSuffixPass = "PASS"

// GetVariablePrefix returns the environment variable prefix for the given key. If key is not empty, it must be one of the predefined suffixes.
func (gc *GitClone) GetVariablePrefix() string {
	return "GIT_" + strings.ToUpper(gitVariableReplacer.Replace(gc.Name)) + "_"
}

func GetGitEnvKeys() []string {
	return []string{
		GitVarSuffixRepo,
		GitVarSuffixBranch,
		GitVarSuffixUser,
	}
}

func GetGitSecretKeys() []string {
	return []string{
		GitVarSuffixPass,
	}
}

func (gc *GitClone) GetEnvVariables() map[string]string {
	envVars := make(map[string]string)
	envVars[GitVarSuffixRepo] = gc.GitRepo
	envVars[GitVarSuffixBranch] = gc.GitBranch
	envVars[GitVarSuffixUser] = gc.GitUser
	return envVars
}

func (gc *GitClone) GetSecretVariables() map[string]string {
	secretVars := make(map[string]string)
	secretVars[GitVarSuffixPass] = gc.GitPass
	return secretVars
}

func (gc *GitClone) GetVariableKeys(appName string, vartype string) string {
	result := make([]string, 0)
	prefix := gc.GetVariablePrefix()
	if vartype == "env" {
		for _, key := range GetGitEnvKeys() {
			result = append(result, appName+"/"+prefix+key)
		}
	} else if vartype == "secret" {
		for _, key := range GetGitSecretKeys() {
			result = append(result, appName+"/"+prefix+key)
		}
	} else {
		// If vartype is not env or secret, return an empty string
		return ""
	}

	return strings.Join(result, " ")
}

type Volumes []*Volume

type Volume struct {
	Name      string `mapstructure:"name" toml:"name" json:"name" groups:"apps"`
	PoolName  string `mapstructure:"poolname" toml:"poolname" json:"poolname" groups:"apps"`
	VolumeDir string `mapstructure:"volumedir" toml:"volumedir" json:"volumedir" options:"etc|log|var|data" groups:"apps"`
}

func (vs Volumes) GroupByPool() map[string]map[string]string {
	result := make(map[string]map[string]string)
	for _, v := range vs {
		if _, exists := result[v.PoolName]; !exists {
			result[v.PoolName] = make(map[string]string)
		}
		result[v.PoolName][v.Name] = v.VolumeDir
	}
	return result
}

type SourceType string

const (
	SourceVolume SourceType = "volume"
	SourceGit    SourceType = "git"
	SourceS3     SourceType = "s3"
)

func IsValidSourceType(sourceType string) bool {
	switch SourceType(sourceType) {
	case SourceVolume, SourceGit, SourceS3:
		return true
	default:
		return false
	}
}

type VolumeMappings []*VolumeMapping

func (vm VolumeMappings) Sort() {
	// Sort the volume mappings by source name and then by full host path
	sort.Slice(vm, func(i, j int) bool {
		if vm[i].Volume == nil || vm[j].Volume == nil {
			return vm[i].SourceName < vm[j].SourceName
		}

		if vm[i].Volume.PoolName == vm[j].Volume.PoolName {
			return vm[i].FullVolumePath() < vm[j].FullVolumePath()
		}

		return vm[i].Volume.PoolName < vm[j].Volume.PoolName
	})
}

func (vm VolumeMappings) GetVolumePaths() map[string]string {
	// Create a map of volume names to their full host paths
	volumePaths := make(map[string]string)
	for _, mapping := range vm {
		if mapping.Volume == nil {
			continue
		}
		fullPath := mapping.FullVolumePath()
		if fullPath == "" {
			continue
		}
		if mpath, exists := volumePaths[mapping.Volume.Name]; !exists {
			volumePaths[mapping.Volume.Name] = mapping.FullVolumePath()
		} else {
			// If the volume already exists, we can append the subpath to the existing path
			volumePaths[mapping.Volume.Name] = mpath + " " + mapping.FullVolumePath()
		}
	}
	return volumePaths
}

type VolumeMapping struct {
	Name       string `mapstructure:"name" toml:"name" json:"name" groups:"apps"`
	ParentName string `mapstructure:"parent" toml:"parent" json:"parent" groups:"apps"`
	VolumeName string `mapstructure:"volume" toml:"volume" json:"volume" groups:"apps"`
	SubPath    string `mapstructure:"subPath" toml:"subPath" json:"subPath" groups:"apps"`

	// Source information
	SourceType SourceType `mapstructure:"sourceType" toml:"sourceType" json:"sourceType" options:"git|s3" groups:"apps"`
	SourceName string     `mapstructure:"sourceName" toml:"sourceName" json:"sourceName" groups:"apps"`

	// Resolved information
	Volume    *Volume        `mapstructure:"-" toml:"-" json:"-"`
	SourceGit *GitClone      `mapstructure:"-" toml:"-" json:"-"`
	SourceS3  *S3Mapping     `mapstructure:"-" toml:"-" json:"-"`
	Parent    *VolumeMapping `mapstructure:"-" toml:"-" json:"-"`
}

func (p *VolumeMapping) FullVolumePath() string {
	// If this mapping has a parent, use the parent's full path
	if p.Parent != nil {
		return filepath.Join(p.Parent.FullVolumePath(), p.SubPath)
	} else if p.Volume != nil {
		return filepath.Join(p.Volume.VolumeDir, p.SubPath)
	} else {
		return "" // If no parent or volume is set, return an empty string
	}
}

func (sp *VolumeMapping) Validate() error {
	if sp.Name == "" {
		return errors.New("mapping name is required")
	}
	if sp.VolumeName == "" && sp.ParentName == "" {
		return fmt.Errorf("either volumeName or parentName is required for mapping %q", sp.Name)
	}
	if sp.SourceType != SourceVolume && sp.SourceName == "" {
		return fmt.Errorf("sourceName is required for mapping %q with sourceType %q", sp.Name, sp.SourceType)
	}
	if sp.SubPath == "" || strings.Contains(sp.SubPath, "..") {
		return fmt.Errorf("invalid subPath %q for mapping %q", sp.SubPath, sp.Name)
	}
	return nil
}

func (sp *VolumeMapping) Resolve(volumes Volumes, gitClones GitClones, s3Dirs S3Mappings, placements VolumeMappings) error {
	// Resolve parent mapping if it exists
	if sp.ParentName != "" {
		for _, p := range placements {
			if p.Name == sp.ParentName {
				sp.Parent = p
				break
			}
		}
		if sp.Parent == nil {
			return fmt.Errorf("parent mapping %s not found for placement %s", sp.ParentName, sp.Name)
		}
	}

	// resolve volume
	for _, v := range volumes {
		if v.Name == sp.VolumeName {
			sp.Volume = v
			break
		}
	}
	if sp.Volume == nil {
		return fmt.Errorf("volume %s not found for placement %s", sp.VolumeName, sp.Name)
	}

	switch sp.SourceType {
	case SourceGit:
		for _, g := range gitClones {
			if g.Name == sp.SourceName {
				sp.SourceGit = g
				return nil
			}
		}
		return fmt.Errorf("git source %s not found for placement %s", sp.SourceName, sp.Name)

	case SourceS3:
		for _, s := range s3Dirs {
			if s.Name == sp.SourceName {
				sp.SourceS3 = s
				return nil
			}
		}
		return fmt.Errorf("s3 source %s not found for placement %s", sp.SourceName, sp.Name)

	default:
		return fmt.Errorf("invalid source type %q for placement %s", sp.SourceType, sp.Name)
	}
}

// DetectCycle checks for cyclic parent relationships.
func (vm *VolumeMapping) DetectCycle(visited map[string]bool, mappings map[string]*VolumeMapping) error {
	if visited[vm.Name] {
		return fmt.Errorf("cycle detected at volume mapping %q", vm.Name)
	}
	visited[vm.Name] = true

	if vm.ParentName != "" {
		parent, ok := mappings[vm.ParentName]
		if !ok {
			return fmt.Errorf("parent mapping %q not found for %q", vm.ParentName, vm.Name)
		}
		return parent.DetectCycle(visited, mappings)
	}

	return nil
}

func (vm *VolumeMapping) UpdateVolumeMapping(field string, value interface{}) error {
	switch field {
	case "name":
		return fmt.Errorf("name cannot be changed for volume mapping %q", vm.Name)

	default:
		return fmt.Errorf("unknown field %q", field)
	}
	return nil
}

type S3Mappings []*S3Mapping

type S3Mapping struct {
	Name   string `mapstructure:"name" toml:"name" json:"name" groups:"apps"`
	Bucket string `mapstructure:"bucket" toml:"bucket" json:"bucket" groups:"apps"`
	Region string `mapstructure:"region" toml:"region" json:"region" groups:"apps"`

	// Optional fields for authentication
	AccessKey string `mapstructure:"accessKey" toml:"accessKey" json:"accessKey" groups:"apps"`
	SecretKey string `mapstructure:"secretKey" toml:"secretKey" json:"secretKey" groups:"apps"`
	Endpoint  string `mapstructure:"endpoint" toml:"endpoint" json:"endpoint" groups:"apps"`
}
