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

// GetRepositoryTree retrieves the repository tree for a given project ID and path.
func (g *GitlabClient) GetRepositoryTree(projectID, path, sha string, timeout time.Duration) (*treehelper.FileNode, error) {

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	opt := &gitlab.ListTreeOptions{}
	if path != "" {
		opt.Path = &path
	}

	// Fetch the repository tree from GitLab
	root := &treehelper.FileNode{
		Name:     path,
		IsFile:   false,
		Children: make(map[string]*treehelper.FileNode),
	}

	tree, _, err := g.Client.Repositories.ListTree(projectID, opt, gitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get repository tree: %w", err)
	}

	for _, entry := range tree {
		parts := strings.Split(entry.Path, "/")
		current := root

		for i, part := range parts {
			// If the node doesn't exist yet, create it
			if _, ok := current.Children[part]; !ok {
				current.Children[part] = &treehelper.FileNode{
					Name:     part,
					IsFile:   entry.Type == "blob",
					Children: make(map[string]*treehelper.FileNode),
				}
			}
			current = current.Children[part]

			// If this is the last part, set IsFile based on the entry type
			if i == len(parts)-1 {
				current.IsFile = entry.Type == "blob"
			}

			// If this is a directory, ensure it has no children
			if entry.Type == "tree" {
				current.IsFile = false
				if current.Children == nil {
					current.Children = make(map[string]*treehelper.FileNode)
				}
			}
		}
	}

	return root, nil
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
