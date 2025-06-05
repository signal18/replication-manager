package treehelper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileNode represents a node in a file/directory tree.
type FileNode struct {
	Name     string               `json:"name"`
	IsFile   bool                 `json:"isFile"`
	Children map[string]*FileNode `json:"children,omitempty"`
}

type FileTreeCache struct {
	ImageRef string    `json:"imageRef"`
	Layers   []string  `json:"layers"`
	Tree     *FileNode `json:"tree"`
	IsCached bool      `json:"isCached"`
}

func AddToFileNodeTree(node *FileNode, parts []string, isDir bool) {
	if len(parts) == 0 || (len(parts) == 1 && parts[0] == "") {
		return
	}

	head := parts[0]
	if node.Children == nil {
		node.Children = make(map[string]*FileNode)
	}

	child, exists := node.Children[head]
	if !exists {
		child = &FileNode{Name: head, IsFile: len(parts) == 1 && !isDir}
		node.Children[head] = child
	}

	if len(parts) > 1 {
		AddToFileNodeTree(child, parts[1:], isDir)
	}
}

func TryReadFileTreeCache(cacheDir, imageRef string) *FileTreeCache {
	path := filepath.Join(cacheDir, strings.ReplaceAll(imageRef, "/", "_")+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cache FileTreeCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}
	return &cache
}

var replacer = strings.NewReplacer(":", "_", "/", "_", "\\", "_", ".", "_")

func WriteToCacheFile(cacheDir, imageRef string, results *FileTreeCache) {
	// Cache the result
	imageRef = replacer.Replace(imageRef)
	cacheFile := filepath.Join(cacheDir, imageRef+".json")
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		os.MkdirAll(cacheDir, 0755)
	}

	file, err := os.Create(cacheFile)
	if err != nil {
		fmt.Printf("failed to create cache file %s: %v\n", cacheFile, err)
		return
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "\t")
	_ = enc.Encode(results)
}
