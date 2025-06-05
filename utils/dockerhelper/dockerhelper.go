package dockerhelper

import (
	"archive/tar"
	"fmt"
	"io"
	"path"
	"reflect"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/signal18/replication-manager/utils/treehelper"
)

func GetDirectoryFromImageRef(cacheDir, imageRef, dir string, options ...crane.Option) (*treehelper.FileNode, error) {
	if dir == "" {
		return nil, fmt.Errorf("directory cannot be empty")
	}

	cache, err := GetFileTreeCache(cacheDir, imageRef, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to get file tree cache: %w", err)
	}

	if cache == nil || cache.Tree == nil {
		return nil, fmt.Errorf("no file tree found for image reference %s", imageRef)
	}

	return TraverseFileTree(cache.Tree, dir, true)
}

func TraverseFileTree(root *treehelper.FileNode, path string, isDir bool) (*treehelper.FileNode, error) {
	if path == "" || path == "/" {
		return root, nil
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	current := root

	for _, part := range parts {
		if child, exists := current.Children[part]; exists {
			current = child
		} else {
			return nil, fmt.Errorf("directory %s not found in file tree", path)
		}
	}

	if isDir && !current.IsFile {
		return nil, fmt.Errorf("directory %s is not a directory", path)
	}

	if !isDir && current.IsFile {
		return nil, fmt.Errorf("path %s is a file, not a directory", path)
	}

	return current, nil
}

// GetFileTreeCache retrieves the file tree cache for the specified image reference.
func GetFileTreeCache(cacheDir, imageRef string, options ...crane.Option) (*treehelper.FileTreeCache, error) {
	img, err := crane.Pull(imageRef, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to pull image: %w", err)
	}

	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("failed to get image layers: %w", err)
	}

	var digests []string
	for _, layer := range layers {
		d, err := layer.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to get digest: %w", err)
		}
		digests = append(digests, d.String())
	}

	cached := treehelper.TryReadFileTreeCache(cacheDir, imageRef)
	if cached != nil && reflect.DeepEqual(cached.Layers, digests) {
		return cached, nil
	}

	seen := map[string]struct{}{}
	deleted := map[string]struct{}{}
	root := &treehelper.FileNode{Name: "/", IsFile: false, Children: map[string]*treehelper.FileNode{}}

	for _, layer := range layers {
		rc, err := layer.Uncompressed()
		if err != nil {
			return nil, fmt.Errorf("uncompress failed: %w", err)
		}
		tr := tar.NewReader(rc)

		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("tar read error: %w", err)
			}

			base := path.Base(hdr.Name)
			parent := path.Dir(hdr.Name)

			if strings.HasPrefix(base, ".wh.") {
				whPath := path.Join(parent, strings.TrimPrefix(base, ".wh."))
				deleted[whPath] = struct{}{}
				continue
			}

			if _, deleted := deleted[hdr.Name]; deleted || seen[hdr.Name] != struct{}{} {
				continue
			}
			seen[hdr.Name] = struct{}{}
			treehelper.AddToFileNodeTree(root, strings.Split(hdr.Name, "/"), hdr.FileInfo().IsDir())
		}
		rc.Close()
	}

	cache := &treehelper.FileTreeCache{
		ImageRef: imageRef,
		Layers:   digests,
		Tree:     root,
	}
	treehelper.WriteToCacheFile(cacheDir, imageRef, cache)

	return cache, nil
}
