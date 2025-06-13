package githelper

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-github/v72/github"
	"github.com/signal18/replication-manager/utils/treehelper"
)

type GitHubClient struct {
	GitClient
	Client *github.Client
}

// NewGitHubClient creates a new GitHub client with the provided base URL and token.
func NewGithubClient(token string) (*GitHubClient, error) {
	// Initialize the GitHub client with the provided base URL and token.
	// This is a placeholder; you would typically use a library like
	// "github.com/google/go-github/v53/github" to create the client.
	var client *github.Client
	if token == "" {
		client = github.NewClient(nil)
	} else {
		// If a token is provided, create a client with authentication.
		client = github.NewClient(nil).WithAuthToken(token)
	}

	return &GitHubClient{
		Client: client,
	}, nil
}

// GetDirectoryFromRepository
func (g *GitHubClient) GetDirectoryFromRepository(cacheDir, projectID, branch, dir string, timeout time.Duration) (*treehelper.FileTreeCache, error) {
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

func (g *GitHubClient) GetRepositoryTree(cacheDir, projectID, branch string, timeout time.Duration) (*treehelper.FileTreeCache, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Split the path into components
	parts := strings.SplitN(projectID, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid project ID format: %s", projectID)
	}
	owner, reponame := parts[0], parts[1]

	commit, _, err := g.Client.Repositories.GetCommit(ctx, owner, reponame, branch, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit for branch %s: %w", branch, err)
	}

	cache := g.LoadTreeFromCache(cacheDir, projectID, commit.GetSHA())
	if cache != nil {
		return cache, nil
	}

	tree, _, err := g.Client.Git.GetTree(ctx, owner, reponame, branch, true)
	if err != nil {
		return nil, err
	}

	root := &treehelper.FileEntry{
		Name:     "root",
		Type:     "directory",
		Children: make([]*treehelper.FileEntry, 0),
	}

	root.Children = append(root.Children, &treehelper.FileEntry{
		Name:     "/",
		Path:     "/",
		Type:     "directory",
		ID:       commit.GetSHA(),
		Children: make([]*treehelper.FileEntry, 0),
	})

	for _, entry := range tree.Entries {
		treehelper.AddToFileTree(root, strings.Split(entry.GetPath(), "/"), entry.GetSHA(), entry.GetType())
	}

	cache = &treehelper.FileTreeCache{
		Tree:       root,
		Reference:  projectID,
		Layers:     []string{commit.GetSHA()},
		IsCached:   true,
		LastUpdate: time.Now(),
	}

	// Save the cache to the specified directory
	treehelper.WriteToCacheFile(cacheDir, projectID, cache)

	cache.IsCached = false

	return cache, nil
}

// GetProjectID retrieves the project ID for a given project path in GitLab.
func (g *GitHubClient) GetProjectID(projectPath string, timeout time.Duration) (int, error) {
	return 0, nil
}

func ParseGitHubURL(input string) (apiURL, projectID string, err error) {
	apiURL = "https://api.github.com"

	// Handle SSH format
	if strings.HasPrefix(input, "git@github.com:") {
		trimmed := strings.TrimPrefix(input, "git@github.com:")
		trimmed = strings.TrimSuffix(trimmed, ".git")
		projectID = trimmed
		return apiURL, projectID, nil
	}

	if !strings.HasPrefix(input, "https://") {
		input = "https://" + input
	}

	// Handle HTTPS format
	parsedURL, err := url.Parse(input)
	if err != nil {
		return "", "", err
	}

	if !strings.Contains(parsedURL.Host, "github.com") {
		return "", "", fmt.Errorf("not a GitHub URL")
	}

	path := strings.Trim(parsedURL.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	projectID = path

	return apiURL, projectID, nil
}
