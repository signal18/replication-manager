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

func (g *GitHubClient) GetRepositoryTree(projectID, path, sha string, timeout time.Duration) (*treehelper.FileNode, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Split the path into components
	parts := strings.SplitN(projectID, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid project ID format: %s", projectID)
	}
	owner, repo := parts[0], parts[1]

	tree, _, err := g.Client.Git.GetTree(ctx, owner, repo, sha, false)
	if err != nil {
		return nil, err
	}

	root := &treehelper.FileNode{
		Name:     path,
		IsFile:   false,
		Children: make(map[string]*treehelper.FileNode),
	}

	for _, entry := range tree.Entries {
		parts := strings.Split(entry.GetPath(), "/")
		current := root

		for i, part := range parts {
			// If the node doesn't exist yet, create it
			if _, ok := current.Children[part]; !ok {
				current.Children[part] = &treehelper.FileNode{
					Name:     part,
					IsFile:   false,
					Children: make(map[string]*treehelper.FileNode),
				}
			}

			// Descend into the next level
			current = current.Children[part]

			// If we're at the last component, set file status
			if i == len(parts)-1 {
				current.IsFile = entry.GetType() == "blob"
			}
			// If the entry is a directory, ensure it has an empty children map
			if entry.GetType() == "tree" {
				current.Children = make(map[string]*treehelper.FileNode)
			}
		}
	}

	return root, nil
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
