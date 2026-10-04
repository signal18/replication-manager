package cluster

import (
	"encoding/base64"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/share"
)

// The xtrabackup bundle (prov-db-docker-xtrabackup-img) gives the database jobs container the tools the official
// MySQL and Percona Server images do not ship: xtrabackup, xbstream and socat. An init container of the
// operator-chosen official xtrabackup image runs share/scripts/xtrabackup_bundle.sh, which copies them with their
// libraries and loader into a volume, and checks at that moment that each one runs and that xtrabackup is of the
// server's series; the jobs container mounts the volume read-only at xtrabackupBundleMount and its definition puts the
// bin directory last on PATH, so a tool the image ships itself always wins. All of it is Docker level: nothing depends
// on dbjobs_new.sh, which needs socat to run at all. Empty setting: nothing is rendered, the services are exactly what
// they were. See
// doc/implementation/cluster/XTRABACKUP_BUNDLE_INJECTION.md.

// xtrabackupBundleMount is where the jobs container sees the bundle volume.
const xtrabackupBundleMount = "/opt/xtrabackup"

// xtrabackupBundleSizeLimit bounds the Kubernetes emptyDir; the script refuses a bundle over the same size.
const xtrabackupBundleSizeLimit = "256Mi"

// xtrabackupImageAuto asks repman to derive the xtrabackup image from the database image.
const xtrabackupImageAuto = "auto"

// xtrabackupAutoTagRe reads the server series (major.minor) from the start of an image tag.
var xtrabackupAutoTagRe = regexp.MustCompile(`^(\d+)\.(\d+)`)

// xtrabackupImageRepo is the official xtrabackup image the injection derives from (the "xtrabackup" repository of the
// image catalog, share/repo/repos.json, built by scripts/updaterepo.sh like the proxysql or haproxy ones).
const xtrabackupImageRepo = "percona/percona-xtrabackup"

// xtrabackupCatalogName is the name of that repository in the catalog.
const xtrabackupCatalogName = "xtrabackup"

// xtrabackupSeriesTag is the tag of the xtrabackup image that backs up a server series. The tag is the series itself
// ("8.4" for a MySQL or Percona Server 8.4), except for 5.7, which xtrabackup 2.4 backs up.
func xtrabackupSeriesTag(series string) string {
	if series == "5.7" {
		return "2.4"
	}
	return series
}

// xtrabackupKnownSeries are the series answered for when the catalog cannot say which xtrabackup tags exist (a
// catalog pushed by the back office that does not list the xtrabackup repository yet).
var xtrabackupKnownSeries = map[string]bool{"5.7": true, "8.0": true, "8.4": true}

// xtrabackupCatalog returns the repositories of the image catalog: the back-office file when one was pushed, the one
// embedded in the binary otherwise (config.GetDockerRepos). A variable so that the tests do not depend on either.
var xtrabackupCatalog = func(conf *config.Config) []config.DockerRepo {
	repos, _ := conf.GetDockerRepos("", false)
	return repos
}

// xtrabackupPublishedTags is the set of tags of the xtrabackup repository in the catalog, nil when the catalog does not
// list that repository. The answer is kept for a while: a render asks several times and the catalog is large.
func (cluster *Cluster) xtrabackupPublishedTags() map[string]bool {
	xtrabackupCatalogMu.Lock()
	defer xtrabackupCatalogMu.Unlock()
	if time.Now().Before(xtrabackupCatalogUntil) {
		return xtrabackupCatalogTags
	}
	var tags map[string]bool
	for _, repo := range xtrabackupCatalog(cluster.Conf) {
		if repo.Name != xtrabackupCatalogName {
			continue
		}
		tags = make(map[string]bool, len(repo.Tags.Results))
		for _, tag := range repo.Tags.Results {
			tags[tag.Name] = true
		}
	}
	xtrabackupCatalogTags, xtrabackupCatalogUntil = tags, time.Now().Add(xtrabackupCatalogTTL)
	return tags
}

const xtrabackupCatalogTTL = 5 * time.Minute

var (
	xtrabackupCatalogMu    sync.Mutex
	xtrabackupCatalogTags  map[string]bool
	xtrabackupCatalogUntil time.Time
)

// xtrabackupAutoImage derives the xtrabackup image from the database image for prov-db-docker-xtrabackup-img=auto.
// It only answers for what it can read without guessing: the official MySQL (`mysql`) and Percona Server
// (`percona-server`) repositories with a tag that starts with a series. A MariaDB image ships mariabackup and needs
// nothing (empty image, no error). Anything else (digest, latest, a private name) is an error and the injection stays
// off: the operator then sets the image explicitly.
//
// The tag of the xtrabackup image is the series (xtrabackupSeriesTag). `published` is the set of tags the catalog lists
// for the xtrabackup repository: the series is answered for when its tag is in it, so a new series is supported as soon
// as the catalog lists its xtrabackup tag, with no change of the code. A nil set (the catalog does not list the
// repository) falls back to xtrabackupKnownSeries.
func xtrabackupAutoImage(dbImage string, published map[string]bool) (string, error) {
	ref := strings.TrimSpace(dbImage)
	if strings.Contains(ref, "@") {
		return "", fmt.Errorf("the database image %q is pinned by digest, its server series cannot be read", ref)
	}
	repo, tag := ref, ""
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		repo, tag = ref[:i], ref[i+1:]
	}
	switch name := strings.ToLower(path.Base(repo)); name {
	case "mariadb":
		return "", nil
	case "mysql", "percona-server":
		m := xtrabackupAutoTagRe.FindStringSubmatch(tag)
		if m == nil {
			return "", fmt.Errorf("the tag of the database image %q does not start with a server series such as 8.4", ref)
		}
		series := m[1] + "." + m[2]
		xbTag := xtrabackupSeriesTag(series)
		known := xtrabackupKnownSeries[series]
		if published != nil {
			known = published[xbTag]
		}
		if !known {
			return "", fmt.Errorf("no official xtrabackup image is known for the server series %s (database image %q)", series, ref)
		}
		return xtrabackupImageRepo + ":" + xbTag, nil
	default:
		return "", fmt.Errorf("the database image %q is not an official MySQL or Percona Server image", ref)
	}
}

// isMariaDBImage tells whether a database image reference is a MariaDB one (its repository ends in mariadb).
func isMariaDBImage(ref string) bool {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return path.Base(ref) == "mariadb"
}

// xtrabackupBundleImage is the xtrabackup image to inject from, "" when the injection is off: the setting itself,
// or, for "auto", the one derived from the database image. A database image that is not an official one, an invalid
// reference, or a derivation that fails is logged once and leaves the injection off, like an empty setting.
func (cluster *Cluster) xtrabackupBundleImage() string {
	image := cluster.xtrabackupBundleImageSetting()
	// a helper image the registry refuses would keep the pod from starting (xtrabackupHelperPullable)
	if image != "" && !cluster.xtrabackupHelperPullable(image) {
		return ""
	}
	return image
}

// xtrabackupBundleImageSetting is the helper image the configuration asks for, before the registry is asked.
func (cluster *Cluster) xtrabackupBundleImageSetting() string {
	value := strings.TrimSpace(cluster.Conf.ProvDbDockerXtrabackupImg)
	if value == "" {
		return ""
	}
	// The injection is for the official images only: it needs their default PATH (the environment of a container
	// replaces the PATH of its image as a whole) and their series. For any other image (a mirror, a custom build) it
	// renders nothing at all, helper, volume, mount and PATH alike: a bundle that is copied but never on the PATH of
	// the jobs container would be a half-done injection. MariaDB images ship their own tools: silently off.
	if !IsStockMySQLImage(cluster.Conf.ProvDbImg) {
		if !isMariaDBImage(cluster.Conf.ProvDbImg) {
			cluster.logInvalidDBIdentityOnce("prov-db-docker-xtrabackup-img", cluster.Conf.ProvDbImg, fmt.Errorf("prov-db-docker-xtrabackup-img is ignored: the database image is not an official MySQL (mysql) or Percona Server (percona/percona-server) image"))
		}
		return ""
	}
	if value != xtrabackupImageAuto {
		// The settings API validates the value, a TOML file or a flag does not: the reference goes into a shell
		// command and a service definition, so it is checked here, whichever way it was written.
		if err := ValidateXtrabackupImage(value); err != nil {
			cluster.logInvalidDBIdentityOnce("prov-db-docker-xtrabackup-img", value, err)
			return ""
		}
		cluster.dbIdentityLog.valid("prov-db-docker-xtrabackup-img")
		return value
	}
	image, err := xtrabackupAutoImage(cluster.Conf.ProvDbImg, cluster.xtrabackupPublishedTags())
	if err != nil {
		cluster.logInvalidDBIdentityOnce("prov-db-docker-xtrabackup-img", cluster.Conf.ProvDbImg, fmt.Errorf("prov-db-docker-xtrabackup-img=auto: %w", err))
		return ""
	}
	cluster.dbIdentityLog.valid("prov-db-docker-xtrabackup-img")
	return image
}

// xtrabackupStockPath is the default PATH of the official mysql and percona-server images (both have the same one).
// The environment of a container replaces the PATH of its image as a whole, so the bundle can be added to it only for
// an image whose default is known.
const xtrabackupStockPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// IsStockMySQLImage tells whether a database image reference is one of the two official images: Docker Hub's `mysql`
// (also written library/mysql) and `percona/percona-server`, with an optional Docker Hub host, a tag or a digest. It is
// deliberately strict: an image the operator built or mirrored under another name or registry may have a PATH of its
// own, and the injection must never change what it does. For such an image nothing is added to its environment (the
// bundle is then not on its PATH) and no series is checked.
func IsStockMySQLImage(ref string) bool {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i:], "/") {
		ref = ref[:i]
	}
	for _, host := range []string{"docker.io/", "index.docker.io/", "registry-1.docker.io/"} {
		ref = strings.TrimPrefix(ref, host)
	}
	switch ref {
	case "mysql", "library/mysql", "percona/percona-server":
		return true
	}
	return false
}

// xtrabackupBundlePath is the PATH given to the jobs container so that xtrabackup, xbstream and socat are found by
// whatever runs there, the launcher and any version of dbjobs_new.sh included: the bundle's bin directory, appended
// last (a tool the image ships itself wins). It is empty, and the container keeps its own PATH, when the injection is
// off or the database image is not a stock one (its default PATH is not known, so the bundle is then not on the PATH).
//
// Why not dbjobs_new.sh: a stock image has no socat, and the script reaches repman through it. A jobs container
// that runs an older script cannot call the API, so it can never fetch the script that would add the bundle to the PATH.
func (cluster *Cluster) xtrabackupBundlePath() string {
	return cluster.xtrabackupBundlePathForImage(cluster.xtrabackupBundleImage())
}

// xtrabackupBundlePathForImage is xtrabackupBundlePath for an image already resolved by a provisioning render.
// A template must use one verdict for its helper, volume, mount and PATH rather than ask the registry again midway.
func (cluster *Cluster) xtrabackupBundlePathForImage(image string) string {
	if image == "" || !IsStockMySQLImage(cluster.Conf.ProvDbImg) {
		return ""
	}
	return xtrabackupStockPath + ":" + xtrabackupBundleMount + "/current/bin"
}

// xtrabackupBundleEnabled tells whether the injection is on.
func (cluster *Cluster) xtrabackupBundleEnabled() bool {
	return cluster.xtrabackupBundleImage() != ""
}

// xtrabackupBundleScript reads share/scripts/xtrabackup_bundle.sh: the copy embedded in the binary first, like the
// configurator does for dbjobs_new.sh (a server build always carries it, whatever WithEmbed says), then the share
// directory.
func (cluster *Cluster) xtrabackupBundleScript() ([]byte, error) {
	const template = "scripts/xtrabackup_bundle.sh"
	if script, err := share.EmbededDbModuleFS.ReadFile(template); err == nil {
		return script, nil
	}
	return os.ReadFile(cluster.Conf.ShareDir + "/" + template)
}

// xtrabackupBundleCommand is the shell pipeline that runs the bundle script in the helper image. The script is
// carried base64-encoded: it needs no network, no file in the image and no quoting inside an OpenSVC INI value or a
// Kubernetes argument (xtrabackupBundleImage only returns a reference that ValidateXtrabackupImage accepts, so it
// holds no shell metacharacter, whether the setting came from the API, a TOML file or a flag).
func (cluster *Cluster) xtrabackupBundleCommand() (string, error) {
	return cluster.xtrabackupBundleCommandForImage(cluster.xtrabackupBundleImage())
}

// xtrabackupBundleCommandForImage builds the helper command for an image already resolved by a provisioning render.
func (cluster *Cluster) xtrabackupBundleCommandForImage(image string) (string, error) {
	script, err := cluster.xtrabackupBundleScript()
	if err != nil {
		return "", fmt.Errorf("cannot read the xtrabackup bundle script: %w", err)
	}
	script = []byte(strings.ReplaceAll(string(script), "@MOUNT@", xtrabackupBundleMount))
	env := "XB_IMAGE=" + image
	if series := cluster.xtrabackupServerSeries(); series != "" {
		env += " XB_SERIES=" + series
	}
	return "echo " + base64.StdEncoding.EncodeToString(script) + " | base64 -d | " + env + " sh", nil
}

// xtrabackupServerSeries is the series (major.minor, "8.4") the tag of a stock database image names, "" when it
// cannot be read from there: the bundle script then skips its series check.
func (cluster *Cluster) xtrabackupServerSeries() string {
	ref := strings.TrimSpace(cluster.Conf.ProvDbImg)
	if !IsStockMySQLImage(ref) || strings.Contains(ref, "@") {
		return ""
	}
	i := strings.LastIndex(ref, ":")
	if i < 0 || i < strings.LastIndex(ref, "/") {
		return ""
	}
	if m := xtrabackupAutoTagRe.FindStringSubmatch(ref[i+1:]); m != nil {
		return m[1] + "." + m[2]
	}
	return ""
}

// OpenSVCGetXtrabackupBundleContainerSection is the blocking init container of the bundle (container#03, between the
// bootstrap container#02 and container#db). It is optional like the bootstrap: a failure never prevents the database
// from starting, the script reports it in the bundle status file.
func (cluster *Cluster) OpenSVCGetXtrabackupBundleContainerSection() map[string]string {
	return cluster.openSVCGetXtrabackupBundleContainerSection(cluster.xtrabackupBundleImage())
}

// openSVCGetXtrabackupBundleContainerSection renders the helper from a verdict resolved once for this template.
func (cluster *Cluster) openSVCGetXtrabackupBundleContainerSection(image string) map[string]string {
	svccontainer := make(map[string]string)
	if image == "" {
		return svccontainer
	}
	command, err := cluster.xtrabackupBundleCommandForImage(image)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s; the xtrabackup bundle is not rendered", err)
		return svccontainer
	}
	svccontainer["detach"] = "false"
	svccontainer["type"] = cluster.Conf.ProvType
	svccontainer["image"] = image
	svccontainer["rm"] = "true"
	svccontainer["start_timeout"] = "120s"
	svccontainer["optional"] = "true"
	// the helper images run as a non-root user by default; the volume directory is root's to fill
	svccontainer["run_args"] = "--user 0:0"
	svccontainer["entrypoint"] = "/bin/sh"
	svccontainer["command"] = `-c "` + command + `"`
	svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro {name}/xtrabackup:/bundle"
	if cluster.Conf.ProvOpensvcImageForcePull {
		svccontainer["image_pull_policy"] = "always"
	}
	return svccontainer
}

// k8sAddXtrabackupBundle adds, when the injection is on, the bundle emptyDir, the helper init container (the only one
// that can write it) and the read-only mount in the dbjobs sidecar. Called on the finished Deployment so that the
// pod without the setting is untouched.
func (cluster *Cluster) k8sAddXtrabackupBundle(dep *appsv1.Deployment, s *ServerMonitor) {
	image := cluster.xtrabackupBundleImage()
	if image == "" {
		return
	}
	command, err := cluster.xtrabackupBundleCommandForImage(image)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s; the xtrabackup bundle is not rendered", err)
		return
	}
	volume := s.Name + "-xtrabackup"
	limit := resource.MustParse(xtrabackupBundleSizeLimit)
	root := int64(0)
	spec := &dep.Spec.Template.Spec
	spec.Volumes = append(spec.Volumes, apiv1.Volume{
		Name:         volume,
		VolumeSource: apiv1.VolumeSource{EmptyDir: &apiv1.EmptyDirVolumeSource{SizeLimit: &limit}},
	})
	spec.InitContainers = append(spec.InitContainers, apiv1.Container{
		Name:            s.Name + "-xtrabackup",
		Image:           image,
		ImagePullPolicy: k8sImagePullPolicy(cluster),
		Command:         []string{"/bin/sh", "-c", command},
		SecurityContext: &apiv1.SecurityContext{RunAsUser: &root, RunAsGroup: &root},
		VolumeMounts:    []apiv1.VolumeMount{{Name: volume, MountPath: "/bundle"}},
	})
	for i := range spec.Containers {
		if spec.Containers[i].Name == s.Name+"-dbjobs" {
			spec.Containers[i].VolumeMounts = append(spec.Containers[i].VolumeMounts,
				apiv1.VolumeMount{Name: volume, MountPath: xtrabackupBundleMount, ReadOnly: true})
			if bundlePath := cluster.xtrabackupBundlePathForImage(image); bundlePath != "" {
				spec.Containers[i].Env = append(spec.Containers[i].Env, apiv1.EnvVar{Name: "PATH", Value: bundlePath})
			}
		}
	}
}
