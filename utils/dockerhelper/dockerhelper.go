package dockerhelper

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
)

// GetDirectoryFromImageRef retrieves the directory path from the image reference.
func GetDirectoryFromImageRef(cacheDir, imageRef, dir string) ([]byte, error) {

	result, err := LoadFileListFromCache(cacheDir, imageRef, dir)
	if err == nil {
		// If the cache is hit, return the cached result
		return result, nil
	}

	// If not cached, pull the image and list files in the specified directory
	result, err = ListFilesInImageDir(cacheDir, imageRef, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to list files in image directory: %w", err)
	}

	return result, nil
}

// ListFilesInImageDir lists all unique file paths under the given dir
// in the image identified by imageRef (e.g., "ubuntu:20.04").
// The paths are returned with leading slashes, e.g., "/usr/bin/bash".
func ListFilesInImageDir(cacheDir, imageRef, targetDir string, options ...crane.Option) ([]byte, error) {
	if targetDir == "" {
		return nil, fmt.Errorf("dir cannot be empty")
	}

	// Normalize dir
	dir := strings.TrimPrefix(targetDir, "/")
	if dir != "" && !strings.HasSuffix(dir, "/") {
		dir += "/"
	}

	img, err := crane.Pull(imageRef, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to pull image: %w", err)
	}

	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("failed to get image layers: %w", err)
	}

	seen := make(map[string]struct{})
	var results []string = make([]string, 0)

	for _, layer := range layers {
		rc, err := layer.Uncompressed()
		if err != nil {
			return nil, fmt.Errorf("failed to read layer: %w", err)
		}
		// You must not defer in a loop — close immediately
		tr := tar.NewReader(rc)

		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("tar read error: %w", err)
			}

			// If dir is empty ("/"), include everything
			if dir == "" || strings.HasPrefix(hdr.Name, dir) {
				if _, exists := seen[hdr.Name]; !exists {
					seen[hdr.Name] = struct{}{}
					results = append(results, "/"+hdr.Name)
				}
			}
		}

		rc.Close() // close stream now that we're done with this layer
	}

	go WriteToCacheFile(cacheDir, imageRef, targetDir, results)

	response, err := json.Marshal(FileListResponse{
		Files:    results,
		IsCached: false,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return response, nil
}

type FileListResponse struct {
	Files    []string `mapstructure:"files" json:"files"`
	IsCached bool     `mapstructure:"isCached" json:"isCached"`
}

var replacer = strings.NewReplacer(":", "_", "/", "_", "\\", "_", ".", "_")

func WriteToCacheFile(cacheDir, imageRef, dir string, results []string) {
	content := FileListResponse{
		Files:    results,
		IsCached: true,
	}

	// Cache the result
	imageRef = replacer.Replace(imageRef)
	imageCacheDir := filepath.Join(cacheDir, imageRef)
	cacheFile := filepath.Join(imageCacheDir, dir+".json")
	if _, err := os.Stat(imageCacheDir); os.IsNotExist(err) {
		os.MkdirAll(imageCacheDir, 0755)
	}

	file, err := os.Create(cacheFile)
	if err != nil {
		fmt.Printf("failed to create cache file %s: %v\n", cacheFile, err)
		return
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "\t")
	_ = enc.Encode(content)
}

func LoadFileListFromCache(cacheDir, imageRef, dir string) ([]byte, error) {
	imageRef = replacer.Replace(imageRef)
	cacheFile := filepath.Join(cacheDir, imageRef, dir+".json")
	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("cache file does not exist: %s", cacheFile)
	}
	content, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}

	return content, nil
}
