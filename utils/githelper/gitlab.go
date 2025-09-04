package githelper

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/treehelper"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// GitlabClient is a client for interacting with GitLab repositories.
type GitlabClient struct {
	GitClient
	Client *gitlab.Client
}

// NewGitlabClient creates a new GitLab client with the provided base URL and token.
func NewGitlabClient(baseURL, token string) (*GitlabClient, error) {
	client, err := gitlab.NewClient(token, gitlab.WithBaseURL(baseURL))
	if err != nil {
		return nil, fmt.Errorf("failed to create GitLab client: %w", err)
	}
	return &GitlabClient{
		Client: client,
	}, nil
}

// GetDirectoryFromRepository
func (g *GitlabClient) GetDirectoryFromRepository(cacheDir, projectID, branch, dir string, timeout time.Duration) (*treehelper.FileTreeCache, error) {
	if dir == "" {
		return nil, fmt.Errorf("directory cannot be empty")
	}

	cache, err := g.GetRepositoryTree(cacheDir, projectID, branch, timeout)
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

// GetRepositoryTree retrieves the repository tree for a given project ID and branch or commit ID.
func (g *GitlabClient) GetRepositoryTree(cacheDir, projectID, branch string, timeout time.Duration) (*treehelper.FileTreeCache, error) {
	var recursive bool = true

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	opt := &gitlab.ListTreeOptions{
		Recursive: &recursive,
	}

	// Fetch the repository tree from GitLab
	root := &treehelper.FileEntry{
		Name:     "root",
		Type:     "directory",
		Path:     "/",
		Children: make([]*treehelper.FileEntry, 0),
	}

	commit, _, err := g.Client.Commits.GetCommit(projectID, branch, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit for branch %s: %w", branch, err)
	}

	cache := g.LoadTreeFromCache(cacheDir, projectID, commit.ID)
	if cache != nil {
		return cache, nil
	}

	tree, _, err := g.Client.Repositories.ListTree(projectID, opt, gitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get repository tree: %w", err)
	}

	root.Children = append(root.Children, &treehelper.FileEntry{
		Name:     "/",
		Path:     "/",
		Type:     "directory",
		ID:       commit.ID,
		Children: make([]*treehelper.FileEntry, 0),
	})

	for _, entry := range tree {
		treehelper.AddToFileTree(root, strings.Split(entry.Path, "/"), entry.Type)
	}

	cache = &treehelper.FileTreeCache{
		Tree:       root,
		Reference:  projectID,
		Layers:     []string{commit.ID},
		IsCached:   true,
		LastUpdate: time.Now(),
	}

	treehelper.WriteToCacheFile(cacheDir, projectID, cache)

	cache.IsCached = false

	return cache, nil
}

// GetProjectID retrieves the project ID for a given project path in GitLab.
func (g *GitlabClient) GetProjectID(projectPath string, timeout time.Duration) (int, error) {
	opt := &gitlab.GetProjectOptions{}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	project, _, err := g.Client.Projects.GetProject(projectPath, opt, gitlab.WithContext(ctx))
	if err != nil {
		return 0, fmt.Errorf("failed to get project ID: %w", err)
	}

	return project.ID, nil
}

func (g *GitlabClient) DownloadFileFromRepo(projectID, branch, filePath string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Split the path into components
	pathComponents := strings.Split(filePath, "/")
	if len(pathComponents) == 0 {
		return nil, fmt.Errorf("file path cannot be empty")
	}

	// Get the file content from GitLab
	content, _, err := g.Client.RepositoryFiles.GetRawFile(projectID, filePath, nil, gitlab.WithContext(ctx))
	return content, err
}

func ParseGitLabURL(input string) (apiURL, projectID string, err error) {
	parsedURL, err := url.Parse(input)
	if err != nil {
		return "", "", err
	}

	// Construct base API URL with /api/v4
	apiURL = "https://" + parsedURL.Host + "/api/v4"

	// Clean project path
	path := strings.Trim(parsedURL.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	projectID = path

	return apiURL, projectID, nil
}
