package githelper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/sideband"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gitclient "github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gogitmem "github.com/go-git/go-git/v5/storage/memory"

	"github.com/signal18/replication-manager/utils/treehelper"
)

const (
	genericMaxFiles       = 5000
	genericDefaultTimeout = 30 * time.Second
)

// GitClientInterface is the common interface for all git provider clients.
type GitClientInterface interface {
	GetDirectoryFromRepository(cacheDir, projectID, branch, dir string, timeout time.Duration, refresh bool) (*treehelper.FileTreeCache, error)
	GetRepositoryTree(cacheDir, projectID, branch string, timeout time.Duration, refresh bool) (*treehelper.FileTreeCache, error)
	DownloadFileFromRepo(projectID, branch, filePath string, timeout time.Duration) ([]byte, error)
}

// GitClient provides shared helpers for git client implementations.
type GitClient struct{}

// LoadTreeFromCache returns a cached FileTreeCache if its stored commit SHA matches commitSHA.
func (gc GitClient) LoadTreeFromCache(cacheDir, gitRef, commitSHA string) *treehelper.FileTreeCache {
	cache := treehelper.TryReadFileTreeCache(cacheDir, gitRef)
	if cache != nil && cache.Tree != nil && cache.Layers != nil && cache.Layers[0] == commitSHA {
		return cache
	}
	return nil
}

// GenericGitClient uses go-git (pure Go) to interact with any Git repository.
// Credentials are passed as Go struct values — they never appear in process
// arguments (/proc/<pid>/cmdline) and no git binary is required on the host.
type GenericGitClient struct {
	GitClient
	User string
	Pass string
}

// NewGenericGitClient creates a GenericGitClient for any HTTPS or SSH repository.
func NewGenericGitClient(user, pass string) *GenericGitClient {
	return &GenericGitClient{User: user, Pass: pass}
}

// normalizeRepoURL ensures the URL has a scheme (defaults to https://).
// It does NOT add a .git suffix — servers that require it are handled by
// openUploadPackSession which retries with .git on 404.
// SSH (git@host:path) and local paths are returned unchanged.
func normalizeRepoURL(repoURL string) string {
	repoURL = strings.TrimSpace(repoURL)

	if strings.HasPrefix(repoURL, "git@") ||
		strings.HasPrefix(repoURL, "/") ||
		strings.HasPrefix(repoURL, "./") ||
		strings.HasPrefix(repoURL, "../") {
		return repoURL
	}

	if !strings.Contains(repoURL, "://") {
		repoURL = "https://" + repoURL
	}

	return repoURL
}

// openUploadPackSession opens a single git upload-pack session and fetches
// the server's advertised references. This gives us:
//   - The commit SHA for the requested branch (for cache validation)
//   - The server's capabilities (shallow, filter, sideband) for the upload-pack request
//
// If the server returns 404 and the URL doesn't already end in .git, the call
// is retried with .git appended. GitLab requires .git; GitHub and most others
// accept both forms. This avoids unconditionally appending .git which can
// break servers that don't recognise the suffix.
//
// Returns the open session (caller must Close()), the advertised refs, the
// commit hash for branch, and the effective URL used (may differ from repoURL
// when the .git retry succeeded).
func (g *GenericGitClient) openUploadPackSession(ctx context.Context, repoURL, branch string) (
	transport.UploadPackSession, *packp.AdvRefs, plumbing.Hash, string, error,
) {
	sess, ar, err := g.tryOpenSession(ctx, repoURL)
	if err != nil {
		// Retry with .git for servers that require it (e.g. some GitLab configs).
		if errors.Is(err, transport.ErrRepositoryNotFound) && !strings.HasSuffix(repoURL, ".git") {
			sess, ar, err = g.tryOpenSession(ctx, repoURL+".git")
			if err == nil {
				repoURL = repoURL + ".git"
			}
		}
		if err != nil {
			return nil, nil, plumbing.ZeroHash, repoURL, classifyGitError(err)
		}
	}

	branchRef := plumbing.NewBranchReferenceName(branch)
	commitHash, ok := ar.References[branchRef.String()]
	if !ok {
		sess.Close()
		return nil, nil, plumbing.ZeroHash, repoURL, fmt.Errorf("branch %q not found in repository", branch)
	}

	return sess, ar, commitHash, repoURL, nil
}

func (g *GenericGitClient) tryOpenSession(ctx context.Context, repoURL string) (transport.UploadPackSession, *packp.AdvRefs, error) {
	ep, err := transport.NewEndpoint(repoURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid repository URL: %w", err)
	}

	c, err := gitclient.NewClient(ep)
	if err != nil {
		return nil, nil, fmt.Errorf("unsupported protocol %q", ep.Protocol)
	}

	sess, err := c.NewUploadPackSession(ep, g.basicAuth())
	if err != nil {
		return nil, nil, err
	}

	ar, err := sess.AdvertisedReferencesContext(ctx)
	if err != nil {
		sess.Close()
		return nil, nil, err
	}

	return sess, ar, nil
}

func (g *GenericGitClient) basicAuth() *githttp.BasicAuth {
	if g.User == "" && g.Pass == "" {
		return nil
	}
	return &githttp.BasicAuth{Username: g.User, Password: g.Pass}
}

// CheckRepo validates that the repository is reachable and the branch exists.
// Uses Remote.List (equivalent to git ls-remote) — no objects downloaded.
func (g *GenericGitClient) CheckRepo(repoURL, branch string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = genericDefaultTimeout
	}

	repoURL = normalizeRepoURL(repoURL)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	rem := gogit.NewRemote(gogitmem.NewStorage(), &gogitcfg.RemoteConfig{
		Name: "origin",
		URLs: []string{repoURL},
	})

	refs, err := rem.ListContext(ctx, &gogit.ListOptions{Auth: g.basicAuth()})
	if err != nil {
		return "", classifyGitError(err)
	}

	target := plumbing.NewBranchReferenceName(branch)
	for _, ref := range refs {
		if ref.Name() == target {
			return fmt.Sprintf("repository reachable and branch %q exists", branch), nil
		}
	}

	return "", fmt.Errorf("branch %q not found in repository", branch)
}

// GetRepositoryTree retrieves the full file tree for a branch.
//
// A single upload-pack session is opened. AdvertisedReferences gives the commit
// SHA (used for cache validation) without downloading any objects. If the cache
// is valid, the session is closed immediately — no upload-pack request is made.
// On a cache miss the same session continues to the upload-pack phase.
//
// If the server advertises "filter", blob:none is requested so only commit and
// tree objects are transferred. Tree walking uses tree entries (not tree.Files())
// so blob objects are never decompressed even when present in the pack.
//
// If the initial connection returns 404 and the URL has no .git suffix, the
// call is retried with .git appended. GitLab requires it; GitHub and most others
// accept both forms.
func (g *GenericGitClient) GetRepositoryTree(cacheDir, repoURL, branch string, timeout time.Duration, refresh bool) (*treehelper.FileTreeCache, error) {
	if timeout <= 0 {
		timeout = genericDefaultTimeout
	}

	repoURL = normalizeRepoURL(repoURL)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// One session open: AdvertisedReferences gives the commit SHA for cache
	// validation without downloading any objects.
	sess, ar, commitHash, repoURL, err := g.openUploadPackSession(ctx, repoURL, branch)
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	commitSHA := commitHash.String()
	cacheRef := sanitizeCacheRef(repoURL)

	if !refresh {
		if cache := g.LoadTreeFromCache(cacheDir, cacheRef, commitSHA); cache != nil {
			return cache, nil // session closes via defer, no upload-pack sent
		}
	}

	// Cache miss: send the upload-pack request on the already-open session.
	s, err := g.doUploadPack(ctx, sess, ar, commitHash)
	if err != nil {
		return nil, err
	}

	commit, err := object.GetCommit(s, commitHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit object: %w", err)
	}

	gitTree, err := object.GetTree(s, commit.TreeHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get root tree: %w", err)
	}

	root, truncated, err := buildTreeByWalkingEntries(s, gitTree, commitSHA)
	if err != nil {
		return nil, err
	}

	cache := &treehelper.FileTreeCache{
		Tree:       root,
		Truncated:  truncated,
		Reference:  cacheRef,
		Layers:     []string{commitSHA},
		IsCached:   true,
		LastUpdate: time.Now(),
	}

	treehelper.WriteToCacheFile(cacheDir, cacheRef, cache)
	cache.IsCached = false

	return cache, nil
}

// GetDirectoryFromRepository returns a subtree for the given directory path.
func (g *GenericGitClient) GetDirectoryFromRepository(cacheDir, repoURL, branch, dir string, timeout time.Duration, refresh bool) (*treehelper.FileTreeCache, error) {
	if dir == "" {
		return nil, fmt.Errorf("directory cannot be empty")
	}

	cache, err := g.GetRepositoryTree(cacheDir, repoURL, branch, timeout, refresh)
	if cache == nil || cache.Tree == nil {
		return nil, err
	}

	subtree, err := treehelper.TraverseFileTree(cache.Tree, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to traverse file tree: %w", err)
	}

	cache.Tree = subtree
	return cache, nil
}

// DownloadFileFromRepo downloads a single file from the repository.
// Uses a depth=1 shallow in-memory clone; blob content for the requested file
// is fetched on demand via file.Contents().
func (g *GenericGitClient) DownloadFileFromRepo(repoURL, branch, filePath string, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		timeout = genericDefaultTimeout
	}

	repoURL = normalizeRepoURL(repoURL)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	repo, err := gogit.CloneContext(ctx, gogitmem.NewStorage(), nil, &gogit.CloneOptions{
		URL:           repoURL,
		Auth:          g.basicAuth(),
		Depth:         1,
		NoCheckout:    true,
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
	})
	if err != nil {
		return nil, classifyGitError(err)
	}

	ref, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve HEAD: %w", err)
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit object: %w", err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree: %w", err)
	}

	f, err := tree.File(filePath)
	if err != nil {
		return nil, fmt.Errorf("file %q not found: %w", filePath, err)
	}

	contents, err := f.Contents()
	if err != nil {
		return nil, fmt.Errorf("failed to read file %q: %w", filePath, err)
	}

	return []byte(contents), nil
}

// fetchTreeObjects uses the raw git upload-pack protocol to fetch only commit
// and tree objects. If the server supports the "filter" capability it sends
// FilterBlobNone(), otherwise it falls back to a standard depth=1 pack.
// doUploadPack sends the upload-pack request on an already-open session whose
// AdvertisedReferences have already been fetched. The session must stay open
// until this function returns.
func (g *GenericGitClient) doUploadPack(ctx context.Context, sess transport.UploadPackSession, ar *packp.AdvRefs, commitHash plumbing.Hash) (storer.EncodedObjectStorer, error) {
	req := packp.NewUploadPackRequestFromCapabilities(ar.Capabilities)
	req.Wants = []plumbing.Hash{commitHash}

	// Shallow clone when the server supports it — only adds req.Capabilities.
	if ar.Capabilities.Supports(capability.Shallow) {
		req.Capabilities.Set(capability.Shallow)
		req.Depth = packp.DepthCommits(1)
	}

	// blob:none filter: server sends only commit + tree objects, no blobs.
	if ar.Capabilities.Supports(capability.Filter) {
		req.Capabilities.Set(capability.Filter)
		req.Filter = packp.FilterBlobNone()
	}

	resp, err := sess.UploadPack(ctx, req)
	if err != nil {
		return nil, classifyGitError(err)
	}
	defer resp.Close()

	packReader := demuxSideband(req.Capabilities, resp)

	s := gogitmem.NewStorage()
	if err := packfile.UpdateObjectStorage(s, packReader); err != nil {
		return nil, fmt.Errorf("failed to unpack objects: %w", err)
	}

	return s, nil
}

// buildTreeByWalkingEntries recursively walks tree.Entries without ever
// accessing blob objects. Returns the root entry and a truncated flag that is
// true when genericMaxFiles was reached before the full tree was traversed.
func buildTreeByWalkingEntries(s storer.EncodedObjectStorer, tree *object.Tree, commitSHA string) (*treehelper.FileEntry, bool, error) {
	root := &treehelper.FileEntry{
		Name:     "root",
		Type:     "directory",
		Children: make([]*treehelper.FileEntry, 0),
	}
	root.Children = append(root.Children, &treehelper.FileEntry{
		Name:     "/",
		Path:     "/",
		Type:     "directory",
		ID:       commitSHA,
		Children: make([]*treehelper.FileEntry, 0),
	})

	count := 0
	if err := walkEntries(s, tree, "", root, &count); err != nil {
		return nil, false, err
	}

	return root, count >= genericMaxFiles, nil
}

// walkEntries recurses through tree entries, building the FileEntry hierarchy.
// Directories → recurse into subtree object. Files → add to tree by path parts.
func walkEntries(s storer.EncodedObjectStorer, tree *object.Tree, prefix string, root *treehelper.FileEntry, count *int) error {
	for _, entry := range tree.Entries {
		if *count >= genericMaxFiles {
			return nil
		}

		fullPath := prefix + entry.Name

		switch entry.Mode {
		case filemode.Dir:
			subtree, err := object.GetTree(s, entry.Hash)
			if err != nil {
				return fmt.Errorf("failed to get subtree %q: %w", fullPath, err)
			}
			if err := walkEntries(s, subtree, fullPath+"/", root, count); err != nil {
				return err
			}

		case filemode.Submodule:
			// Submodules are listed as directory-like entries; record path but don't recurse.
			parts := strings.Split(fullPath, "/")
			treehelper.AddToFileTree(root, parts, "submodule")

		default:
			// Regular file, executable, symlink — record the path.
			parts := strings.Split(fullPath, "/")
			treehelper.AddToFileTree(root, parts, "blob")
			*count++
		}
	}

	return nil
}

// demuxSideband strips the sideband framing from the upload-pack response so
// packfile.UpdateObjectStorage receives a plain pack stream.
// The sideband protocol multiplexes pack data and progress messages; the
// demuxer separates them. If neither sideband capability is negotiated, the
// reader is returned as-is.
func demuxSideband(caps *capability.List, r io.Reader) io.Reader {
	switch {
	case caps.Supports(capability.Sideband64k):
		d := sideband.NewDemuxer(sideband.Sideband64k, r)
		return d
	case caps.Supports(capability.Sideband):
		d := sideband.NewDemuxer(sideband.Sideband, r)
		return d
	default:
		return r
	}
}

// classifyGitError annotates go-git transport errors with a category prefix
// while preserving the original error message (which includes the server's HTTP
// response body — useful for diagnosing credential and permission problems).
//
// HTTP mapping (errors.Is works because go-git uses %w):
//
//	401 → ErrAuthenticationRequired  e.g. "authentication required: Bad credentials"
//	403 → ErrAuthorizationFailed     e.g. "authorization failed: insufficient scope"
//	404 → ErrRepositoryNotFound      e.g. "repository not found: Not Found"
func classifyGitError(err error) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, transport.ErrAuthenticationRequired):
		// 401: wrong or missing credentials. Return original — it contains the
		// server's response body which explains why auth failed.
		return fmt.Errorf("authentication required: %w", err)

	case errors.Is(err, transport.ErrAuthorizationFailed):
		// 403: credentials valid but token lacks the required scope/permission.
		return fmt.Errorf("access forbidden: %w", err)

	case errors.Is(err, transport.ErrRepositoryNotFound):
		// 404: repo does not exist, OR private repo with missing/wrong credentials
		// (servers return 404 to avoid confirming private repo existence).
		return fmt.Errorf("repository not found (private repos also return this when credentials are wrong): %w", err)

	case errors.Is(err, transport.ErrEmptyUploadPackRequest):
		return fmt.Errorf("repository is empty: %w", err)
	}

	// String fallback for connection-level errors that don't use sentinel types.
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return fmt.Errorf("timeout: %w", err)
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "no route to host"):
		return fmt.Errorf("host unreachable: %w", err)
	}

	return err
}

// sanitizeCacheRef converts a repo URL into a credential-free cache key.
func sanitizeCacheRef(repoURL string) string {
	if strings.HasPrefix(repoURL, "git@") {
		s := strings.TrimPrefix(repoURL, "git@")
		s = strings.Replace(s, ":", "/", 1)
		return strings.TrimSuffix(s, ".git")
	}
	if strings.HasPrefix(repoURL, "/") || strings.HasPrefix(repoURL, "./") {
		return strings.TrimSuffix(repoURL, ".git")
	}
	if !strings.Contains(repoURL, "://") {
		repoURL = "https://" + repoURL
	}
	// Strip userinfo (credentials) from the cache key.
	if i := strings.Index(repoURL, "://"); i != -1 {
		rest := repoURL[i+3:]
		if at := strings.LastIndex(rest, "@"); at != -1 {
			repoURL = repoURL[:i+3] + rest[at+1:]
		}
	}
	repoURL = strings.TrimSuffix(repoURL, ".git")
	if i := strings.Index(repoURL, "://"); i != -1 {
		repoURL = repoURL[i+3:]
	}
	return strings.Trim(repoURL, "/")
}
