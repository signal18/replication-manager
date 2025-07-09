package config

import (
	"errors"
	"fmt"
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
		GitClones: make(GitClones, 0),
		S3Mounts:  make(S3Mounts, 0),
		Volumes:   make(Volumes, 0),
		Paths:     make(PathMaps, 0),
		Mutex:     sync.RWMutex{},
	}
}

type StorageMapping struct {
	GitClones GitClones `mapstructure:"git-clones" toml:"git-clones" json:"gitClones" groups:"apps"`
	S3Mounts  S3Mounts  `mapstructure:"s3-mounts" toml:"s3-mounts" json:"s3Mounts" groups:"apps"`
	Volumes   Volumes   `mapstructure:"volumes" toml:"volumes" json:"volumes" groups:"apps"`
	Paths     PathMaps  `mapstructure:"paths"  toml:"paths" json:"paths" groups:"apps"`

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

	// Prevent dropping volumes that are used in PathMappings
	for _, p := range sm.Paths {
		if p.VolumeName == vol.Name {
			return fmt.Errorf("cannot drop volume %s, it is used in path mappings", vol.Name)
		}
	}

	// Remove the volume
	sm.Volumes = append(sm.Volumes[:vindex], sm.Volumes[vindex+1:]...)

	return nil
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

	// Prevent dropping git clones that are used in path mappings
	for _, p := range sm.Paths {
		if p.GitName == gc.Name {
			return fmt.Errorf("cannot drop git clone %s, it is used in path mappings", gc.Name)
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
		if existingPath.TargetPath == p.TargetPath {
			return fmt.Errorf("path mapping already exists for target path: %s", p.TargetPath)
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
		// Resolve pointers for each path mapping
		p.ResolvePointers(sm.Volumes, sm.GitClones, sm.S3Mounts)

		// Update the path mapping in the slice
		sm.Paths[i] = p

		// If the path is not resolved, log a warning
		if p.Volume == nil && p.GitSource == nil && p.S3Source == nil {
			fmt.Printf("Warning: Path mapping %s could not be resolved\n", p.TargetPath)
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
		if existingPath.TargetPath == p.TargetPath {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("path mapping %s not found", p.TargetPath)
	}

	// Remove the path mapping
	sm.Paths = append(sm.Paths[:index], sm.Paths[index+1:]...)

	return nil
}

func (sm *StorageMapping) GetPathMapping(targetPath string) (*PathMapping, error) {
	// Use a mutex to protect concurrent access
	sm.Mutex.RLock()
	defer sm.Mutex.RUnlock()

	for _, p := range sm.Paths {
		if p.TargetPath == targetPath {
			return p, nil
		}
	}
	return nil, fmt.Errorf("path mapping %s not found", targetPath)
}

func (sm *StorageMapping) InsertS3Mount(s3 *S3Mount) error {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Check if the S3 mapping already exists
	for _, existingS3 := range sm.S3Mounts {
		if existingS3.Name == s3.Name {
			return fmt.Errorf("S3 mapping already exists: %s", s3.Name)
		}
	}

	// Add the new S3 mapping
	sm.S3Mounts = append(sm.S3Mounts, s3)

	return nil
}

func (sm *StorageMapping) DropS3Mount(s3 *S3Mount) error {
	// Use a mutex to protect concurrent access
	sm.Mutex.Lock()
	defer sm.Mutex.Unlock()

	// Prevent dropping S3 mappings that are used in path mappings
	for _, p := range sm.Paths {
		if p.S3Name == s3.Name {
			return fmt.Errorf("cannot drop S3 mapping %s, it is used in path mappings", s3.Name)
		}
	}

	// Find the S3 mapping by name
	var index int = -1
	for i, existingS3 := range sm.S3Mounts {
		if existingS3.Name == s3.Name {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("S3 mapping %s not found", s3.Name)
	}

	// Remove the S3 mapping
	sm.S3Mounts = append(sm.S3Mounts[:index], sm.S3Mounts[index+1:]...)

	return nil
}

type PathMapping struct {
	TargetPath string `mapstructure:"targetpath" toml:"targetpath" json:"targetpath"`
	VolumeName string `mapstructure:"volumename" toml:"volumename" json:"volumename"`
	VolumePath string `mapstructure:"volumepath" toml:"volumepath" json:"volumepath"`
	GitName    string `mapstructure:"gitname" toml:"gitname" json:"gitname"`
	S3Name     string `mapstructure:"s3name" toml:"s3name" json:"s3name"`

	Volume    *Volume   `toml:"-" json:"-" mapstructure:"-"`
	GitSource *GitClone `toml:"-" json:"-" mapstructure:"-"`
	S3Source  *S3Mount  `toml:"-" json:"-" mapstructure:"-"`
}

// GetDockerMapping returns the Docker path mapping for the given PathMapping.
func (p PathMapping) GetDockerMapping() string {
	return p.VolumePath + ":" + p.TargetPath
}

type PathMaps []*PathMapping

func (pm PathMaps) Sort() {
	// Sort the path mappings by source name and then by full host path
	sort.Slice(pm, func(i, j int) bool {
		if pm[i].TargetPath == "" || pm[j].TargetPath == "" {
			if pm[i].VolumeName == pm[j].VolumeName {
				return pm[i].VolumePath < pm[j].VolumePath
			} else if pm[i].VolumeName != "" && pm[j].VolumeName != "" {
				return pm[i].VolumeName < pm[j].VolumeName
			} else if pm[i].VolumeName != "" {
				return true // pm[i] has a volume, pm[j] does not
			} else {
				return false // pm[j] has a volume, pm[i] does not
			}
		}

		return pm[i].TargetPath < pm[j].TargetPath
	})
}

func (pm *PathMapping) ResolvePointers(volumes Volumes, gits GitClones, s3s S3Mounts) {
	// Resolve Volume
	if pm.Volume == nil && pm.VolumeName != "" {
		for _, v := range volumes {
			if v.Name == pm.VolumeName {
				pm.Volume = v
				break
			}
		}
	}

	// Resolve Git
	if pm.GitSource == nil {
		for _, g := range gits {
			if g.Name == pm.GitName {
				pm.GitSource = g
				break
			}
		}
	}

	// Resolve S3
	if pm.S3Source == nil {
		for _, s := range s3s {
			if s.Name == pm.S3Name {
				pm.S3Source = s
				break
			}
		}
	}
}

func (pm *PathMaps) GetVolumePaths() map[string][]string {
	// Create a map to hold the volume paths
	volumePaths := make(map[string][]string)

	// Iterate through each path mapping
	for _, p := range *pm {
		if p.Volume == nil {
			continue // Skip if no volume is associated
		}

		// Get the volume name and path
		volName := p.Volume.Name
		path := p.VolumePath
		if path == "" {
			path = p.TargetPath // Use target path if volume path is not specified
		}

		// Initialize the slice for this volume if it doesn't exist
		if _, exists := volumePaths[volName]; !exists {
			volumePaths[volName] = make([]string, 0)
		}
		// Append the path to the volume's slice
		volumePaths[volName] = append(volumePaths[volName], path)
	}

	return volumePaths
}

type GitClones []*GitClone

type GitClone struct {
	Name       string  `mapstructure:"name" toml:"name" json:"name" groups:"apps"`
	GitRepo    string  `mapstructure:"repo" toml:"repo" json:"repo" groups:"apps"`
	GitBranch  string  `mapstructure:"branch" toml:"branch" json:"branch" groups:"apps"`
	VolumeName string  `mapstructure:"volumename" toml:"volumename" json:"volumename" groups:"apps"`
	VolumeDir  string  `mapstructure:"volumedir" toml:"volumedir" json:"volumedir" options:"etc|log|var" groups:"apps"`
	GitUser    string  `mapstructure:"user" toml:"user" json:"user" groups:"apps"`
	GitPass    string  `mapstructure:"pass" toml:"pass" json:"pass" groups:"apps"`
	Timeout    int     `mapstructure:"timeout" toml:"timeout" json:"timeout" groups:"apps"`
	Volume     *Volume `toml:"-" json:"-" mapstructure:"-"`
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

type S3Mounts []*S3Mount

type S3Mount struct {
	Name      string `mapstructure:"name" toml:"name" json:"name"`
	Bucket    string `mapstructure:"bucket" toml:"bucket" json:"bucket" groups:"apps"`
	Region    string `mapstructure:"region" toml:"region" json:"region" groups:"apps"`
	AccessKey string `mapstructure:"accessKey" toml:"accessKey" json:"accessKey" groups:"apps"`
	SecretKey string `mapstructure:"secretKey" toml:"secretKey" json:"secretKey" groups:"apps"`
	Endpoint  string `mapstructure:"endpoint" toml:"endpoint" json:"endpoint" groups:"apps"`
}
