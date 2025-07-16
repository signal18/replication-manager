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
	PrimaryRoute Route          `mapstructure:"-"  toml:"-" json:"primaryRoute" groups:"apps"`
	Routes       Routes         `mapstructure:"routes"  toml:"routes" json:"routes" groups:"apps"`
	Storages     StorageMapping `mapstructure:"storages"  toml:"storages" json:"storages" groups:"apps"`
	Paths        PathMaps       `mapstructure:"paths"  toml:"paths" json:"paths" groups:"apps"`
	Variables    VariableMaps   `mapstructure:"variables"  toml:"variables" json:"variables" groups:"apps"`

	// Use sync.RWMutex to protect concurrent access to Volumes and VolumeMappings
	Mutex sync.RWMutex
}

func NewDeploymentConfig() *Deployment {
	return &Deployment{
		Routes:    Routes{},
		Variables: VariableMaps{},
		Paths:     PathMaps{},
	}
}

func (d *Deployment) GetVolumeByName(name string) (*Volume, error) {
	// Use a mutex to protect concurrent access
	d.Mutex.RLock()
	defer d.Mutex.RUnlock()

	for _, v := range d.Storages.Volumes {
		if v.Name == name {
			return v, nil
		}
	}
	return nil, fmt.Errorf("volume %s not found", name)
}

func (d *Deployment) InsertVolume(v *Volume) error {
	// Use a mutex to protect concurrent access
	d.Mutex.Lock()
	defer d.Mutex.Unlock()

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
	for _, existingVolume := range d.Storages.Volumes {
		if existingVolume.Name == v.Name {
			return fmt.Errorf("volume already exists: %s", v.Name)
		}

		if existingVolume.VolumeDir == v.VolumeDir && existingVolume.PoolName == v.PoolName {
			return fmt.Errorf("volume with same directory and pool already exists: %s", v.VolumeDir)
		}
	}

	// Add the new volume
	d.Storages.Volumes = append(d.Storages.Volumes, v)
	return nil
}

func (d *Deployment) DropVolume(vol *Volume) error {
	// Use a mutex to protect concurrent access
	d.Mutex.Lock()
	defer d.Mutex.Unlock()

	// Find the volume by name
	var volume *Volume
	var vindex int
	for i, v := range d.Storages.Volumes {
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
	for _, p := range d.Paths {
		if p.SourceType == SourceVolume && p.SourceName == vol.Name {
			return fmt.Errorf("cannot drop volume %s, it is used in path mappings", vol.Name)
		}
	}

	// Remove the volume
	d.Storages.Volumes = append(d.Storages.Volumes[:vindex], d.Storages.Volumes[vindex+1:]...)

	return nil
}

func (d *Deployment) InsertGitClone(gc *GitClone) error {
	// Use a mutex to protect concurrent access
	d.Mutex.Lock()
	defer d.Mutex.Unlock()

	// Check if the git clone already exists
	for _, existingGC := range d.Storages.GitClones {
		if existingGC.Name == gc.Name {
			return fmt.Errorf("git clone already exists: %s", gc.Name)
		}
	}

	// Add the new git clone
	d.Storages.GitClones = append(d.Storages.GitClones, gc)

	return nil
}

// DropGitClone removes a git clone by name.
func (d *Deployment) DropGitClone(gc *GitClone) error {
	// Use a mutex to protect concurrent access
	d.Mutex.Lock()
	defer d.Mutex.Unlock()

	// Prevent dropping git clones that are used in path mappings
	for _, p := range d.Paths {
		if p.SourceType == SourceGit && p.SourceName == gc.Name {
			return fmt.Errorf("cannot drop git clone %s, it is used in path mappings", gc.Name)
		}
	}

	// Find the git clone by name
	var index int = -1
	for i, g := range d.Storages.GitClones {
		if gc.Name == g.Name {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("git clone %s not found", gc.Name)
	}

	// Remove the git clone
	d.Storages.GitClones = append(d.Storages.GitClones[:index], d.Storages.GitClones[index+1:]...)
	return nil
}

func (d *Deployment) GetGitClone(name string) (*GitClone, error) {
	// Use a mutex to protect concurrent access
	d.Mutex.RLock()
	defer d.Mutex.RUnlock()

	for _, g := range d.Storages.GitClones {
		if g.Name == name {
			return g, nil
		}
	}
	return nil, fmt.Errorf("git clone %s not found", name)
}

func (d *Deployment) HasDuplicateGitVolumePath(volumename, volumedir string) bool {
	// Use a mutex to protect concurrent access
	d.Mutex.RLock()
	defer d.Mutex.RUnlock()

	for _, g := range d.Storages.GitClones {
		if g.VolumeName == volumename && g.VolumeDir == volumedir {
			return true
		}
	}
	return false
}

func (d *Deployment) ResolveGitVolume(name string) (*GitClone, error) {
	// Use a mutex to protect concurrent access
	d.Mutex.RLock()
	defer d.Mutex.RUnlock()

	gc, err := d.GetGitClone(name)
	if err == nil {
		if gc.Volume == nil {
			// If the volume is not set, try to resolve it
			if vol, err := d.GetVolumeByName(gc.VolumeName); err == nil {
				gc.Volume = vol
				return gc, nil
			} else {
				return nil, fmt.Errorf("volume for git clone %s not found: %v", name, err)
			}
		}
	}

	return nil, fmt.Errorf("git clone %s not found: %v", name, err)
}

func (d *Deployment) InsertPath(p PathMapping) error {
	// Use a mutex to protect concurrent access
	d.Mutex.Lock()
	defer d.Mutex.Unlock()

	// Check if the path already exists
	for _, existingPath := range d.Paths {
		if existingPath.DockerPath == p.DockerPath {
			return fmt.Errorf("path mapping already exists for target path: %s", p.DockerPath)
		}

		if existingPath.Name == p.ParentName {
			// If the parent name matches, set the parent pointer
			p.Parent = existingPath
		}
	}

	// Validate the path mapping
	p.ResolvePointers(d.Storages.Volumes, d.Storages.GitClones, d.Storages.S3Mounts, d.Paths)
	if p.SourceName != "" && p.Source == nil {
		return fmt.Errorf("source %s not found for path mapping %s", p.SourceName, p.DockerPath)
	} else if p.Source != nil {
		p.VolumeName = p.Source.GetSourceVolumeName() // Use the source's volume name if available
	} else {
		p.VolumeName = p.Parent.VolumeName // Inherit volume name from parent if no source is specified
	}

	// Add the new path mapping
	d.Paths = append(d.Paths, &p)

	return nil
}

func (d *Deployment) ResolvePaths() {
	// Use a mutex to protect concurrent access
	d.Mutex.Lock()
	defer d.Mutex.Unlock()

	for i, p := range d.Paths {
		// Resolve pointers for each path mapping
		p.ResolvePointers(d.Storages.Volumes, d.Storages.GitClones, d.Storages.S3Mounts, d.Paths)

		if p.Parent != nil {
			if p.VolumeName == "" {
				p.VolumeName = p.Parent.VolumeName // Inherit volume name from parent if no source is specified
			}
		}

		// Update the path mapping in the slice
		d.Paths[i] = p

		// If the path is not resolved, log a warning
		if p.SourceType != "" && p.Source == nil {
			fmt.Printf("Warning: Path mapping %s could not be resolved\n", p.DockerPath)
		}
	}
}

func (d *Deployment) SortPaths() {
	// Use a mutex to protect concurrent access
	d.Mutex.Lock()
	defer d.Mutex.Unlock()

	// Sort the paths by source name and then by full host path
	d.Paths.Sort()
}

func (d *Deployment) DropPath(p PathMapping) error {
	// Use a mutex to protect concurrent access
	d.Mutex.Lock()
	defer d.Mutex.Unlock()

	// Find the path mapping by docker path
	var index int = -1
	for i, existingPath := range d.Paths {
		if existingPath.DockerPath == p.DockerPath {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("path mapping %s not found", p.DockerPath)
	}

	// Remove the path mapping
	d.Paths = append(d.Paths[:index], d.Paths[index+1:]...)

	return nil
}

func (d *Deployment) GetPathMapping(path string) (*PathMapping, error) {
	// Use a mutex to protect concurrent access
	d.Mutex.RLock()
	defer d.Mutex.RUnlock()

	for _, p := range d.Paths {
		if p.DockerPath == path {
			return p, nil
		}
	}
	return nil, fmt.Errorf("path mapping %s not found", path)
}

func (d *Deployment) InsertS3Mount(s3 *S3Mount) error {
	// Use a mutex to protect concurrent access
	d.Mutex.Lock()
	defer d.Mutex.Unlock()

	// Check if the S3 mapping already exists
	for _, existingS3 := range d.Storages.S3Mounts {
		if existingS3.Name == s3.Name {
			return fmt.Errorf("S3 mapping already exists: %s", s3.Name)
		}
	}

	// Add the new S3 mapping
	d.Storages.S3Mounts = append(d.Storages.S3Mounts, s3)

	return nil
}

func (d *Deployment) DropS3Mount(s3 *S3Mount) error {
	// Use a mutex to protect concurrent access
	d.Mutex.Lock()
	defer d.Mutex.Unlock()

	// Prevent dropping S3 mappings that are used in path mappings
	for _, p := range d.Paths {
		if p.SourceType == SourceS3 && p.SourceName == s3.Name {
			return fmt.Errorf("cannot drop S3 mapping %s, it is used in path mappings", s3.Name)
		}
	}

	// Find the S3 mapping by name
	var index int = -1
	for i, existingS3 := range d.Storages.S3Mounts {
		if existingS3.Name == s3.Name {
			index = i
			break
		}
	}

	if index == -1 {
		return fmt.Errorf("S3 mapping %s not found", s3.Name)
	}

	// Remove the S3 mapping
	d.Storages.S3Mounts = append(d.Storages.S3Mounts[:index], d.Storages.S3Mounts[index+1:]...)

	return nil
}

func (d *Deployment) GetS3Mount(name string) (*S3Mount, error) {
	// Use a mutex to protect concurrent access
	d.Mutex.RLock()
	defer d.Mutex.RUnlock()

	for _, s3 := range d.Storages.S3Mounts {
		if s3.Name == name {
			return s3, nil
		}
	}
	return nil, fmt.Errorf("S3 mapping %s not found", name)
}

func (d *Deployment) HasDuplicateS3VolumePath(volumename, volumedir string) bool {
	// Use a mutex to protect concurrent access
	d.Mutex.RLock()
	defer d.Mutex.RUnlock()

	for _, s3 := range d.Storages.S3Mounts {
		if s3.VolumeName == volumename && s3.VolumeDir == volumedir {
			return true
		}
	}
	return false
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
	}
}

type StorageMapping struct {
	GitClones GitClones `mapstructure:"git-clones" toml:"git-clones" json:"gitClones" groups:"apps"`
	S3Mounts  S3Mounts  `mapstructure:"s3-mounts" toml:"s3-mounts" json:"s3Mounts" groups:"apps"`
	Volumes   Volumes   `mapstructure:"volumes" toml:"volumes" json:"volumes" groups:"apps"`
}

type PathMaps []*PathMapping

func (pm PathMaps) Sort() {
	// Sort the path mappings by source name and then by full host path
	sort.Slice(pm, func(i, j int) bool {
		return SorterFunc(pm[i], pm[j])
	})
}

func SorterFunc(pmA, pmB *PathMapping) bool {
	if pmA.Parent == nil || pmB.Parent == nil {
		if pmA.Parent == nil && pmB.Parent == nil {
			return pmA.DockerPath < pmB.DockerPath
		} else if pmA.Parent == nil {
			return true // nil parent comes first
		} else {
			return false // nil parent comes last
		}
	} else {
		parentI := pmA.Parent
		parentJ := pmB.Parent
		if parentI.DockerPath == parentJ.DockerPath {
			return SorterFunc(parentI, parentJ)
		} else {
			return parentI.DockerPath < parentJ.DockerPath
		}
	}
}

func (pms *PathMaps) GetVolumeDirs() map[string][]string {
	// Create a map to hold the volume paths
	SourcePaths := make(map[string][]string)

	// Iterate through each path mapping
	for _, p := range *pms {
		// Get the volume name and path
		path := p.SourcePath
		if path == "" {
			path = p.DockerPath // Use target path if volume path is not specified
		}

		// Initialize the slice for this volume if it doesn't exist
		if _, exists := SourcePaths[p.VolumeName]; !exists {
			SourcePaths[p.VolumeName] = make([]string, 0)
		}
		// Append the path to the volume's slice
		SourcePaths[p.VolumeName] = append(SourcePaths[p.VolumeName], path)
	}

	return SourcePaths
}

type SourceInterface interface {
	// GetSourcePath returns the source path for the source type.
	GetSourceName() string
	GetSourceVolumeName() string
	GetSourcePath() string
}

type PathMapping struct {
	Name       string     `mapstructure:"name" toml:"name" json:"name" groups:"apps"`
	ParentName string     `mapstructure:"parentname" toml:"parentname" json:"parentname" groups:"apps"`
	DockerPath string     `mapstructure:"dockerpath" toml:"dockerpath" json:"dockerpath" groups:"apps"`
	SourceType SourceType `mapstructure:"srctype" toml:"srctype" json:"srctype" options:"volume|git|s3" groups:"apps"`
	SourceName string     `mapstructure:"srcname" toml:"srcname" json:"srcname" groups:"apps"`
	SourcePath string     `mapstructure:"srcpath" toml:"srcpath" json:"srcpath" groups:"apps"`
	VolumeName string     `mapstructure:"volumename" toml:"volumename" json:"volumename" groups:"apps"`

	Parent *PathMapping    `mapstructure:"-" toml:"-" json:"-"`
	Source SourceInterface `mapstructure:"-" toml:"-" json:"-"`
}

// GetDockerMapping returns the Docker path mapping for the given PathMapping.
func (p PathMapping) GetDockerMapping(volname string) string {
	return filepath.Join(volname, p.SourcePath) + ":" + p.DockerPath
}

func (pm *PathMapping) ResolvePointers(volumes Volumes, gits GitClones, s3s S3Mounts, parents PathMaps) error {
	// Resolve Parent
	if pm.ParentName != "" {
		for _, parent := range parents {
			if parent.DockerPath == pm.ParentName {
				pm.Parent = parent
				break
			}
		}
	}

	// Resolve Volume
	if pm.SourceType != "" && pm.SourceName == "" {
		return fmt.Errorf("source name is required for path mapping %s", pm.DockerPath)
	}

	switch pm.SourceType {
	case SourceVolume:
		for _, v := range volumes {
			if v.Name == pm.SourceName {
				pm.Source = v
				break
			}
		}
	case SourceGit:
		for _, g := range gits {
			if g.Name == pm.SourceName {
				pm.Source = g
				break
			}
		}
	case SourceS3:
		for _, s := range s3s {
			if s.Name == pm.SourceName {
				pm.Source = s
				break
			}
		}
	default:
		// If the source type is not recognized, return an error
		return fmt.Errorf("invalid source type: %s", pm.SourceType)
	}

	return nil
}

type GitClones []*GitClone

type GitClone struct {
	Name       string `mapstructure:"name" toml:"name" json:"name" groups:"apps"`
	GitRepo    string `mapstructure:"repo" toml:"repo" json:"repo" groups:"apps"`
	GitBranch  string `mapstructure:"branch" toml:"branch" json:"branch" groups:"apps"`
	VolumeName string `mapstructure:"volumename" toml:"volumename" json:"volumename" groups:"apps"`
	VolumeDir  string `mapstructure:"volumedir" toml:"volumedir" json:"volumedir" groups:"apps"`
	GitUser    string `mapstructure:"user" toml:"user" json:"user" groups:"apps"`
	GitPass    string `mapstructure:"pass" toml:"pass" json:"pass" groups:"apps"`
	Timeout    int    `mapstructure:"timeout" toml:"timeout" json:"timeout" groups:"apps"`

	Volume *Volume `toml:"-" json:"-" mapstructure:"-"`
}

func (gc *GitClone) GetSourcePath() string {
	// Ensure the volume directory starts with a slash
	if !strings.HasPrefix(gc.VolumeDir, "/") {
		return "/" + gc.VolumeDir
	}
	return gc.VolumeDir
}

func (gc *GitClone) GetSourceName() string {
	// Return the volume name as the source name
	return gc.Name
}

func (gc *GitClone) GetSourceVolumeName() string {
	// Return the volume name associated with the git clone
	if gc.Volume != nil {
		return gc.Volume.Name
	}
	return gc.VolumeName
}

func (gc *GitClone) GetSourcePoolName() string {
	// Return the volume name associated with the git clone
	if gc.Volume != nil {
		return gc.Volume.PoolName
	}
	return ""
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

func (v *Volume) GetSourcePath() string {
	// Ensure the volume directory starts with a slash
	if !strings.HasPrefix(v.VolumeDir, "/") {
		return "/" + v.VolumeDir
	}
	return v.VolumeDir
}

func (v *Volume) GetSourceName() string {
	// Return the volume name as the source name
	return v.Name
}

func (v *Volume) GetSourceVolumeName() string {
	// Return the volume name as the source volume name
	return v.Name
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
	Name       string `mapstructure:"name" toml:"name" json:"name"`
	Bucket     string `mapstructure:"bucket" toml:"bucket" json:"bucket" groups:"apps"`
	VolumeName string `mapstructure:"volumename" toml:"volumename" json:"volumename" groups:"apps"`
	VolumeDir  string `mapstructure:"volumedir" toml:"volumedir" json:"volumedir" groups:"apps"`

	Volume *Volume `mapstructure:"-" toml:"-" json:"-"`
}

func (s *S3Mount) GetSourcePath() string {
	// Ensure the volume path starts with a slash
	if !strings.HasPrefix(s.VolumeDir, "/") {
		return "/" + s.VolumeDir
	}
	return s.VolumeDir
}

func (s *S3Mount) GetSourceName() string {
	// Return the volume name as the source name
	return s.Name
}

func (s *S3Mount) GetSourceVolumeName() string {
	// Return the volume name associated with the S3 mount
	if s.Volume != nil {
		return s.Volume.Name
	}
	return s.VolumeName
}
