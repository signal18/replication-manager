package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

func (d *Deployment) GetVolumePaths(volumename string) []*PathMapping {
	// Use a mutex to protect concurrent access
	d.Mutex.RLock()
	defer d.Mutex.RUnlock()

	var paths []*PathMapping
	for _, p := range d.Paths {
		if p.VolumeName == volumename {
			paths = append(paths, p)
		}
	}

	return paths
}

func (d *Deployment) ResolveVolumePaths(volumename string) {
	paths := d.GetVolumePaths(volumename)
	for _, p := range paths {
		d.ResolvePath(p)
	}
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

func (d *Deployment) HasDuplicateGitVolumePath(gitname, volumename, volumedir string) bool {
	// Use a mutex to protect concurrent access
	d.Mutex.RLock()
	defer d.Mutex.RUnlock()

	for _, g := range d.Storages.GitClones {
		if g.Name != gitname && g.VolumeName == volumename && g.VolumeDir == volumedir {
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

func (d *Deployment) GetGitPaths(name string) []*PathMapping {
	// Use a mutex to protect concurrent access
	d.Mutex.RLock()
	defer d.Mutex.RUnlock()

	var paths []*PathMapping
	for _, p := range d.Paths {
		if p.SourceType == SourceGit && p.SourceName == name {
			paths = append(paths, p)
		}
	}

	return paths
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

func (d *Deployment) ResolveGitPaths(gitname string) {
	paths := d.GetGitPaths(gitname)
	for _, p := range paths {
		d.ResolvePath(p)
	}
}

func (d *Deployment) ResolvePath(p *PathMapping) error {
	err := p.ResolvePointers(d.Storages.Volumes, d.Storages.GitClones, d.Storages.S3Mounts, d.Paths)
	if err != nil {
		return err
	}

	if p.Parent != nil {
		if p.VolumeName == "" {
			p.VolumeName = p.Parent.VolumeName // Inherit volume name from parent if no source is specified
		}
	}

	// If the path is not resolved, log a warning
	if p.SourceType != "" && p.Source == nil {
		return fmt.Errorf("source %s not found for path mapping %s", p.SourceName, p.DockerPath)
	}

	return nil
}

func (d *Deployment) ResolvePaths() []error {
	var errs []error
	// Use a mutex to protect concurrent access
	d.Mutex.Lock()
	defer d.Mutex.Unlock()

	for _, p := range d.Paths {
		// Resolve pointers for each path mapping
		err := p.ResolvePointers(d.Storages.Volumes, d.Storages.GitClones, d.Storages.S3Mounts, d.Paths)
		if err != nil {
			if errs == nil {
				errs = make([]error, 0)
			}
			errs = append(errs, err)
			continue
		}

		if p.Parent != nil {
			if p.VolumeName == "" {
				p.VolumeName = p.Parent.VolumeName // Inherit volume name from parent if no source is specified
			}
		}

		// If the path is not resolved, log a warning
		if p.SourceType != "" && p.Source == nil {
			if errs == nil {
				errs = make([]error, 0)
			}
			errs = append(errs, fmt.Errorf("source %s not found for path mapping %s", p.SourceName, p.DockerPath))
		}
	}
	return errs
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

		if existingS3.Endpoint == s3.Endpoint && existingS3.Bucket == s3.Bucket {
			return fmt.Errorf("S3 mapping with same endpoint and bucket already exists: %s", s3.Name)
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

func (d *Deployment) GetS3MountPaths(name string) []*PathMapping {
	// Use a mutex to protect concurrent access
	d.Mutex.RLock()
	defer d.Mutex.RUnlock()

	var paths []*PathMapping
	for _, p := range d.Paths {
		if p.SourceType == SourceS3 && p.SourceName == name {
			paths = append(paths, p)
		}
	}

	return paths
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

func (d *Deployment) ResolveS3MountPaths(s3name string) {
	paths := d.GetS3MountPaths(s3name)
	for _, p := range paths {
		d.ResolvePath(p)
	}
}

func (d *Deployment) GetVariableByName(name string, lock bool) (*VariableMapping, error) {
	// Use a mutex to protect concurrent access
	if lock {
		d.Mutex.RLock()
		defer d.Mutex.RUnlock()
	}

	for _, v := range d.Variables {
		if v.Name == name {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("variable %s not found", name)
}

type Routes []Route

// RouteMonitor holds optional per-route monitoring customization for HTTP/HTTPS routes.
type RouteMonitor struct {
	Path          string `mapstructure:"path" toml:"path" json:"path,omitempty" groups:"apps"`
	AuthType      string `mapstructure:"auth-type" toml:"auth-type" json:"authType,omitempty" groups:"apps"`
	AuthUser      string `mapstructure:"auth-user" toml:"auth-user" json:"authUser,omitempty" groups:"apps"`
	AuthSecretVar string `mapstructure:"auth-secret-var" toml:"auth-secret-var" json:"authSecretVar,omitempty" groups:"apps"`
	ExpectStatus  string `mapstructure:"expect-status" toml:"expect-status" json:"expectStatus,omitempty" groups:"apps"`
}

// Normalize fills in defaults and canonicalizes all fields.  It is idempotent
// and must be called before Validate.
func (m *RouteMonitor) Normalize() {
	m.Path = strings.TrimSpace(m.Path)
	m.AuthType = strings.ToLower(strings.TrimSpace(m.AuthType))
	m.AuthUser = strings.TrimSpace(m.AuthUser)
	m.AuthSecretVar = strings.TrimSpace(m.AuthSecretVar)
	m.ExpectStatus = strings.TrimSpace(m.ExpectStatus)

	if m.AuthType == "none" {
		m.AuthType = ""
	}
	if m.Path == "" {
		m.Path = "/"
	} else if !strings.HasPrefix(m.Path, "/") {
		m.Path = "/" + m.Path
	}
	if m.ExpectStatus == "" {
		m.ExpectStatus = "200"
	}
}

// Validate returns an error when the monitor config is structurally invalid.
// Call Normalize before Validate.
func (m *RouteMonitor) Validate() error {
	switch m.AuthType {
	case "", "basic", "bearer":
	default:
		return fmt.Errorf("auth-type must be 'none', 'basic', or 'bearer', got %q", m.AuthType)
	}
	if m.AuthType == "basic" {
		if m.AuthUser == "" {
			return errors.New("basic auth requires auth-user")
		}
		if m.AuthSecretVar == "" {
			return errors.New("basic auth requires auth-secret-var")
		}
	}
	if m.AuthType == "bearer" && m.AuthSecretVar == "" {
		return errors.New("bearer auth requires auth-secret-var")
	}
	if m.Path != "" && !strings.HasPrefix(m.Path, "/") {
		return fmt.Errorf("path must start with '/', got %q", m.Path)
	}
	if m.ExpectStatus != "" {
		if _, err := ParseExpectStatus(m.ExpectStatus); err != nil {
			return fmt.Errorf("expect-status: %w", err)
		}
	}
	return nil
}

// ValidateSecretRef checks that auth-secret-var references an existing secret variable.
// It is nil-safe: a nil receiver returns nil immediately.
func (m *RouteMonitor) ValidateSecretRef(variables VariableMaps) error {
	if m == nil || m.AuthSecretVar == "" {
		return nil
	}
	for _, v := range variables {
		if v.Name == m.AuthSecretVar {
			if v.Type != VariableTypeSecret {
				return fmt.Errorf("auth-secret-var %q must reference a variable of type 'secret', got %q", m.AuthSecretVar, v.Type)
			}
			return nil
		}
	}
	return fmt.Errorf("auth-secret-var %q references a variable that does not exist", m.AuthSecretVar)
}

// ParseExpectStatus parses a comma-separated list of HTTP status codes.
// Each code must be a valid integer in the range 100-599.  Duplicates are
// silently deduplicated.
func ParseExpectStatus(s string) ([]int, error) {
	parts := strings.Split(s, ",")
	seen := make(map[int]bool)
	var codes []int
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("empty element in expect-status")
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid HTTP status code", part)
		}
		if n < 100 || n > 599 {
			return nil, fmt.Errorf("%d is not a valid HTTP status code (must be 100–599)", n)
		}
		if !seen[n] {
			seen[n] = true
			codes = append(codes, n)
		}
	}
	return codes, nil
}

type Route struct {
	Name string `mapstructure:"name" toml:"name" json:"name" groups:"apps"`

	// Existing host-route fields kept for backward compatibility.
	CName    string `mapstructure:"cname" toml:"cname" json:"cname" groups:"apps"`
	Port     string `mapstructure:"port" toml:"port" json:"port,omitempty" groups:"apps"`
	Protocol string `mapstructure:"protocol" toml:"protocol" json:"protocol" groups:"apps"`
	Primary  bool   `mapstructure:"primary" toml:"primary" json:"primary" groups:"apps"`

	// Explicit source/destination fields.
	Mode            string `mapstructure:"mode" toml:"mode" json:"mode" groups:"apps"`            // host | port
	SourcePort      string `mapstructure:"sourceport" toml:"sourceport" json:"sourcePort" groups:"apps"`
	DestinationPort string `mapstructure:"destport" toml:"destport" json:"destPort" groups:"apps"`

	// Optional per-route monitoring customization.  Nil means no monitor block
	// was configured; legacy routes keep nil so no defaults are injected.
	Monitor *RouteMonitor `mapstructure:"monitor" toml:"monitor" json:"monitor,omitempty" groups:"apps"`
}

// Clone returns a deep copy of the Route, duplicating the Monitor pointer so
// mutations to the copy do not affect the original.
func (r Route) Clone() Route {
	if r.Monitor != nil {
		m := *r.Monitor
		r.Monitor = &m
	}
	return r
}

// Label returns a compact human-readable identifier for the route.
// Host routes: "cname:destPort". Port routes: "cname:sourcePort -> destPort".
func (r Route) Label() string {
	if r.Mode == "port" {
		return r.CName + ":" + r.SourcePort + " -> " + r.DestinationPort
	}
	return r.CName + ":" + r.DestinationPort
}

// Normalize fills in defaults and copies legacy fields so the Route is in
// canonical form.  It is idempotent and must be called before Validate.
func (r *Route) Normalize() {
	r.Mode = strings.ToLower(strings.TrimSpace(r.Mode))
	r.Protocol = strings.ToLower(strings.TrimSpace(r.Protocol))
	r.CName = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(r.CName)), ".")
	if r.Monitor != nil {
		r.Monitor.Normalize()
	}

	if r.Mode == "" {
		// Legacy compatibility: a route saved with protocol=tcp but no explicit
		// mode is a TCP port-forward, not a host-mode HTTP route.
		if r.Protocol == "tcp" {
			r.Mode = "port"
		} else {
			r.Mode = "host"
		}
	}

	if r.Mode == "host" {
		if r.Protocol == "" {
			r.Protocol = "https"
		}
		// Legacy: port means destination port for host routes.
		if r.DestinationPort == "" && r.Port != "" {
			r.DestinationPort = r.Port
		}
		// SourcePort is intentionally left empty for host routes because the
		// shared gateway frontend owns the bind port.
		if r.DestinationPort != "" {
			r.Port = r.DestinationPort
		}
	}

	if r.Mode == "port" {
		sourceFromPort := r.Port
		destinationFromPort := r.Port
		if idx := strings.Index(r.Port, ":"); idx >= 0 {
			sourceFromPort = r.Port[:idx]
			destinationFromPort = r.Port[idx+1:]
		}
		if r.Port != "" && r.SourcePort == "" && r.DestinationPort == "" {
			// Legacy "9000:9001" means asymmetric port-forward; "9000" means symmetric.
			r.SourcePort = sourceFromPort
			r.DestinationPort = destinationFromPort
		} else {
			if r.SourcePort == "" && r.Port != "" {
				r.SourcePort = sourceFromPort
			}
			if r.DestinationPort == "" && r.Port != "" {
				r.DestinationPort = destinationFromPort
			}
		}
		if r.SourcePort != "" || r.DestinationPort != "" {
			if r.SourcePort == r.DestinationPort {
				r.Port = r.SourcePort
			} else {
				r.Port = r.SourcePort + ":" + r.DestinationPort
			}
		}
	}
}

// Validate returns an error when the route is not in a legal state.
// Call Normalize before Validate.
func (r *Route) Validate() error {
	if r.Mode != "host" && r.Mode != "port" {
		return fmt.Errorf("route mode must be 'host' or 'port', got %q", r.Mode)
	}

	if r.SourcePort != "" {
		if strings.Contains(r.SourcePort, ":") {
			return fmt.Errorf("sourcePort must be a single port number, got %q", r.SourcePort)
		}
		if err := validateSinglePort(r.SourcePort); err != nil {
			return fmt.Errorf("sourcePort: %w", err)
		}
	}
	if r.DestinationPort != "" {
		if strings.Contains(r.DestinationPort, ":") {
			return fmt.Errorf("destPort must be a single port number, got %q", r.DestinationPort)
		}
		if err := validateSinglePort(r.DestinationPort); err != nil {
			return fmt.Errorf("destPort: %w", err)
		}
	}

	switch r.Mode {
	case "host":
		if r.Port != "" && strings.Contains(r.Port, ":") {
			return fmt.Errorf("host route port must be a single port number, got %q", r.Port)
		}
		if r.Protocol == "tcp" {
			return fmt.Errorf("host-mode tcp route must be migrated manually to mode=port")
		}
		if r.Protocol != "https" {
			return fmt.Errorf("host route protocol must be 'https' in phase 1, got %q", r.Protocol)
		}
		if r.CName == "" {
			return errors.New("host route requires a cname")
		}
		if strings.HasPrefix(r.CName, "*") {
			return fmt.Errorf("host route cname cannot start with '*', got %q", r.CName)
		}
		if r.DestinationPort == "" {
			return errors.New("host route requires a destination port")
		}
	case "port":
		if r.Protocol != "http" && r.Protocol != "tcp" {
			return fmt.Errorf("port route protocol must be 'http' or 'tcp' in phase 1, got %q", r.Protocol)
		}
		if r.CName == "" {
			return errors.New("port route requires a cname so the gateway listener can be resolved")
		}
		if strings.HasPrefix(r.CName, "*") {
			return fmt.Errorf("port route cname cannot start with '*', got %q", r.CName)
		}
		if r.SourcePort == "" {
			return errors.New("port route requires a source port")
		}
		if r.DestinationPort == "" {
			return errors.New("port route requires a destination port")
		}
	}

	if r.Monitor != nil {
		if err := r.Monitor.Validate(); err != nil {
			return fmt.Errorf("monitor: %w", err)
		}
	}

	return nil
}

func validateSinglePort(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%q is not a valid port number", s)
	}
	return nil
}

// NormalizeRoutes normalizes all routes in the deployment and enforces a
// single primary route.
func (d *Deployment) NormalizeRoutes() {
	for i := range d.Routes {
		d.Routes[i].Normalize()
	}
	d.EnforceSinglePrimary()
}

// ValidateRoutes validates all routes after normalization.  It returns the
// first error found and the index of the offending route.  It also validates
// monitor secret-var references against d.Variables so the check runs on every
// write path (API save, deployment load).
func (d *Deployment) ValidateRoutes() error {
	for i, r := range d.Routes {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("route[%d]: %w", i, err)
		}
		if r.Monitor != nil {
			if err := r.Monitor.ValidateSecretRef(d.Variables); err != nil {
				return fmt.Errorf("route[%d] monitor: %w", i, err)
			}
		}
	}
	return nil
}

// NormalizedCopy returns a new slice with every route cloned and normalized so
// that CheckGatewayConflicts sees canonical Mode/SourcePort values even for
// routes that were persisted before normalization was applied on write.
func NormalizedCopy(routes []Route) []Route {
	out := make([]Route, len(routes))
	for i, r := range routes {
		out[i] = r.Clone()
	}
	for i := range out {
		out[i].Normalize()
	}
	return out
}

// CheckGatewayConflicts checks for shared-gateway collisions.
// Host-route collision keys are (cname).
// Port-route collision keys are (cname, sourcePort).
func CheckGatewayConflicts(current []Route, others ...[]Route) error {
	seenCNames := make(map[string]bool)
	seenListeners := make(map[string]bool) // cname:sourcePort

	for _, routes := range append([][]Route{current}, others...) {
		for _, r := range routes {
			switch r.Mode {
			case "host":
				if r.CName != "" {
					if seenCNames[r.CName] {
						return fmt.Errorf("cname %q is already reserved by another route on the shared gateway", r.CName)
					}
					seenCNames[r.CName] = true
				}
			case "port":
				if r.SourcePort != "" {
					listenerKey := r.CName + ":" + r.SourcePort
					if seenListeners[listenerKey] {
						return fmt.Errorf("listener %q is already reserved by another route on the shared gateway", listenerKey)
					}
					seenListeners[listenerKey] = true
				}
			}
		}
	}
	return nil
}

// EnforceSinglePrimary ensures exactly one route is marked primary.  If
// multiple routes are already primary, the first wins and the rest are
// demoted.  If none is primary, the first route is promoted.
// PrimaryRoute is updated in sync with the flags.
func (d *Deployment) EnforceSinglePrimary() {
	foundPrimary := -1
	for i, r := range d.Routes {
		if r.Primary {
			if foundPrimary == -1 {
				foundPrimary = i
			} else {
				d.Routes[i].Primary = false
			}
		}
	}
	if foundPrimary == -1 && len(d.Routes) > 0 {
		d.Routes[0].Primary = true
		foundPrimary = 0
	}
	if foundPrimary >= 0 {
		d.PrimaryRoute = d.Routes[foundPrimary]
	} else {
		d.PrimaryRoute = Route{}
	}
}

var routeTokenSanitizerRe = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// sanitizeToken replaces non-alphanumeric/underscore characters with '_' and
// lowercases the result.
func sanitizeToken(s string) string {
	return strings.ToLower(routeTokenSanitizerRe.ReplaceAllString(s, "_"))
}

// shortHash returns an 8-character hex prefix of the SHA-256 hash of s.
func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:4])
}

// BuildRouteToken returns a stable per-route fragment identity string.
//
//	host route -> host_<destPort>_<shortHash(cname)>
//	port route -> port_<sourcePort>_<destPort>_<shortHash(cname+sourcePort+destPort)>
func BuildRouteToken(r Route) string {
	switch r.Mode {
	case "port":
		return sanitizeToken(fmt.Sprintf("port_%s_%s_%s", r.SourcePort, r.DestinationPort, shortHash(r.CName+r.SourcePort+r.DestinationPort)))
	default: // host
		return sanitizeToken(fmt.Sprintf("host_%s_%s", r.DestinationPort, shortHash(r.CName)))
	}
}

// BuildGlobalRouteToken returns the globally unique HAProxy object name prefix
// derived from cluster, app, and route identity.
func BuildGlobalRouteToken(clusterName, appName string, routeToken string) string {
	return sanitizeToken(clusterName + "_" + appName + "_" + routeToken)
}

// BuildRouteStateKey returns a stable monitoring/debounce key for the route.
// It includes mode, protocol, ports, and a hash of the CNAME so it survives
// route renames while remaining distinct from the HAProxy fragment identity
// (BuildRouteToken).  The key changes only when route identity or protocol
// changes, not on cosmetic name edits.
//
//	host route -> monitor_host_<protocol>_<destPort>_<shortHash(cname)>
//	port route -> monitor_port_<protocol>_<sourcePort>_<destPort>_<shortHash(cname)>
func BuildRouteStateKey(r Route) string {
	switch r.Mode {
	case "port":
		return sanitizeToken(fmt.Sprintf("monitor_port_%s_%s_%s_%s",
			r.Protocol, r.SourcePort, r.DestinationPort, shortHash(r.CName+r.SourcePort+r.DestinationPort)))
	default: // host
		return sanitizeToken(fmt.Sprintf("monitor_host_%s_%s_%s",
			r.Protocol, r.DestinationPort, shortHash(r.CName)))
	}
}

type RouteStatus struct {
	Route
	Status string `mapstructure:"status"  toml:"status" json:"status"`
}

const VariableTypeEnv = "env"
const VariableTypeSecret = "secret"

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
	if pmA == nil || pmB == nil {
		return pmA != nil
	}

	if pmA.Level != pmB.Level {
		return pmA.Level < pmB.Level
	}

	if pmA.ParentName != pmB.ParentName {
		return pmA.ParentName < pmB.ParentName
	}

	if pmA.DockerPath != pmB.DockerPath {
		return pmA.DockerPath < pmB.DockerPath
	}

	if pmA.SourceName != pmB.SourceName {
		return pmA.SourceName < pmB.SourceName
	}

	return pmA.Name < pmB.Name
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
	Level      int        `mapstructure:"level" toml:"level" json:"level" groups:"apps"`
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
			if parent.Name == pm.ParentName {
				pm.Parent = parent
				break
			}
		}
		if pm.Parent == nil {
			return fmt.Errorf("parent path %q not found for path mapping %s", pm.ParentName, pm.DockerPath)
		}
		if pm.Level <= pm.Parent.Level {
			pm.Level = pm.Parent.Level + 1
		}
	} else if pm.Level < 0 {
		pm.Level = 0
	}

	// Resolve Volume
	if pm.SourceType != "" && pm.SourceName == "" {
		return fmt.Errorf("source name is required for path mapping %s", pm.DockerPath)
	}

	if pm.SourceType != "" {
		if pm.SourcePath == "" {
			pm.SourcePath = "."
		} else if pm.SourcePath == "/" {
			return fmt.Errorf("invalid source path '/' for path mapping %s: use '.'", pm.DockerPath)
		}
	} else {
		return nil
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

// func (gc *GitClone) GetSecretVariables() map[string]string {
// 	secretVars := make(map[string]string)
// 	secretVars[GitVarSuffixPass] = gc.GitPass
// 	return secretVars
// }

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

type S3Node interface {
	GetS3Endpoint() string
}

type S3Mount struct {
	Name         string `mapstructure:"name" toml:"name" json:"name"`
	Endpoint     string `mapstructure:"endpoint" toml:"endpoint" json:"endpoint" groups:"apps"`
	Bucket       string `mapstructure:"bucket" toml:"bucket" json:"bucket" groups:"apps"`
	Region       string `mapstructure:"region" toml:"region" json:"region" groups:"apps"`
	AccessKey    string `mapstructure:"accesskey" toml:"accesskey" json:"accesskey" groups:"apps"`
	SecretKey    string `mapstructure:"secretkey" toml:"secretkey" json:"secretkey" groups:"apps"`
	MountDir     string `mapstructure:"mountdir" toml:"mountdir" json:"mountdir" groups:"apps"`
	VolumeName   string `mapstructure:"volumename" toml:"volumename" json:"volumename" groups:"apps"`
	VolumeDir    string `mapstructure:"volumedir" toml:"volumedir" json:"volumedir" groups:"apps"`
	ProviderName string `mapstructure:"providername" toml:"providername" json:"providerName,omitempty" groups:"apps"`

	Node   S3Node  `mapstructure:"-" toml:"-" json:"-"`
	Volume *Volume `mapstructure:"-" toml:"-" json:"-"`
}

const S3VarSuffixMountDir = "MOUNT_DIR"
const S3VarSuffixEndpoint = "ENDPOINT"
const S3VarSuffixBucket = "BUCKET"
const S3VarSuffixRegion = "REGION"
const S3VarSuffixAccessKey = "AWS_ACCESS_KEY"
const S3VarSuffixSecretKey = "AWS_SECRET_KEY"

func GetS3EnvKeys() []string {
	return []string{
		S3VarSuffixBucket,
		S3VarSuffixRegion,
		S3VarSuffixAccessKey,
		S3VarSuffixMountDir,
		S3VarSuffixEndpoint,
	}
}

func GetS3SecretKeys() []string {
	return []string{
		S3VarSuffixSecretKey,
	}
}

func (s *S3Mount) GetVariablePrefix() string {
	return strings.ToUpper(gitVariableReplacer.Replace(s.Name)) + "_"
}

func (s *S3Mount) GetVariableKeys(appName string, vartype string) string {
	result := make([]string, 0)
	prefix := s.GetVariablePrefix()
	if vartype == "env" {
		for _, key := range GetS3EnvKeys() {
			result = append(result, fmt.Sprintf("%s=%s/%s\n", key, appName, prefix+key))
		}
	} else if vartype == "secret" {
		for _, key := range GetS3SecretKeys() {
			result = append(result, fmt.Sprintf("%s=%s/%s\n", key, appName, prefix+key))
		}
	} else {
		// If vartype is not env or secret, return an empty string
		return ""
	}

	return strings.Join(result, " ")
}

func (s *S3Mount) GetEnvVariables() map[string]string {
	envVars := make(map[string]string)
	envVars[S3VarSuffixBucket] = s.Bucket
	envVars[S3VarSuffixRegion] = s.Region
	envVars[S3VarSuffixAccessKey] = s.AccessKey
	envVars[S3VarSuffixMountDir] = s.MountDir
	envVars[S3VarSuffixEndpoint] = s.Endpoint
	return envVars
}

func (s *S3Mount) GetSecretVariables() map[string]string {
	secretVars := make(map[string]string)
	secretVars[S3VarSuffixSecretKey] = s.SecretKey
	return secretVars
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

func (s *S3Mount) GetSourcePoolName() string {
	// Return the volume name associated with the S3 mount
	if s.Volume != nil {
		return s.Volume.PoolName
	}
	return "" // Return an empty string if the volume is not set
}
