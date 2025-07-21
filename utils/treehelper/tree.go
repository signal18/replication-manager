package treehelper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileEntry represents an entry in the file tree.
// It can represent a file, a symlink, or a directory.
// The ID field is used to uniquely identify the node, which can be useful for caching or referencing.
type FileEntry struct {
	ID       string       `json:"id,omitempty"` // Unique identifier for the file entry
	Name     string       `json:"name"`
	Path     string       `json:"path"`
	Type     string       `json:"type"` // "file" or "symlink" or "directory"
	Children []*FileEntry `json:"children,omitempty"`
}

type FileTreeCache struct {
	IsCached   bool       `json:"isCached"`
	Layers     []string   `json:"layers"`
	Tree       *FileEntry `json:"tree"`
	Reference  string     `json:"reference,omitempty"`  // Reference to the image or repository
	LastUpdate time.Time  `json:"lastUpdate,omitempty"` // Last update time of the cache
}

func AddToFileTree(parent *FileEntry, parts []string, fileID string, fileType string) {
	// If parts is empty or contains only an empty string, return
	if len(parts) == 0 || (len(parts) == 1 && parts[0] == "") {
		return
	}

	head := parts[0]
	if parent.Children == nil {
		parent.Children = make([]*FileEntry, 0)
	}

	var child *FileEntry
	for _, c := range parent.Children {
		if c.Name == head {
			child = c
			break
		}
	}

	if child == nil {
		child = &FileEntry{Name: head, Path: filepath.Join("/", parent.Path, head)}
		parent.Children = append(parent.Children, child)
	}

	if len(parts) > 1 {
		child.Type = "directory" // Ensure the child is a directory if there are more parts
		AddToFileTree(child, parts[1:], fileID, fileType)
	} else {
		// If this is the last part, set the ID and type
		child.ID = fileID
		if child.Type == "" {
			child.Type = fileType
		}
	}
}

var replacer = strings.NewReplacer(":", "_", "/", "_", "\\", "_", ".", "_")

// TryReadFileTreeCache attempts to read the file tree cache from the specified directory.
// It returns a pointer to FileTreeCache if successful, or nil if the cache file does not exist or cannot be read.
func TryReadFileTreeCache(cacheDir, imageRef string) *FileTreeCache {
	imageRef = replacer.Replace(imageRef)
	cacheFile := filepath.Join(cacheDir, imageRef+".json")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil
	}
	var cache FileTreeCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}
	return &cache
}

// WriteToCacheFile writes the FileTreeCache to a JSON file in the specified cache directory.
// It creates the directory if it does not exist
func WriteToCacheFile(cacheDir, imageRef string, results *FileTreeCache) error {
	// Cache the result
	imageRef = replacer.Replace(imageRef)
	cacheFile := filepath.Join(cacheDir, imageRef+".json")
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		os.MkdirAll(cacheDir, 0755)
	}

	file, err := os.Create(cacheFile)
	if err != nil {
		return fmt.Errorf("failed to create cache file %s: %w", cacheFile, err)
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "\t")
	err = enc.Encode(results)
	if err != nil {
		return fmt.Errorf("failed to encode cache file %s: %w", cacheFile, err)
	}
	return nil
}

func TraverseFileTree(root *FileEntry, path string) (*FileEntry, error) {
	if path == "" || path == "/" {
		return root, nil
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	current := root

	for _, part := range parts {
		found := false
		for _, child := range current.Children {
			if child.Name == part {
				current = child
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("path %s not found in file tree", path)
		}
	}

	return current, nil
}
