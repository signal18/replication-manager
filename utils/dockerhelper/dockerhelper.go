package dockerhelper

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/signal18/replication-manager/utils/treehelper"
)

func GetDirectoryFromImageRef(cacheDir, imageRef, dir string, options ...crane.Option) (*treehelper.FileTreeCache, error) {
	if dir == "" {
		return nil, fmt.Errorf("directory cannot be empty")
	}

	cache, err := GetFileTreeCache(cacheDir, imageRef, options...)
	if err != nil {
		return nil, err
	}

	if cache == nil || cache.Tree == nil {
		return nil, fmt.Errorf("no file tree found for image reference %s", imageRef)
	}

	subtree, err := treehelper.TraverseFileTree(cache.Tree, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to traverse file tree: %w", err)
	}

	cache.Tree = subtree

	return cache, nil
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

	//Sort the digests to ensure consistent ordering
	if len(digests) > 1 {
		// Use a stable sort to ensure consistent ordering
		slices.SortStableFunc(digests, func(a, b string) int {
			return strings.Compare(a, b)
		})
	}

	cache := treehelper.TryReadFileTreeCache(cacheDir, imageRef)
	if cache != nil {
		cached := cache.Layers
		slices.SortStableFunc(cached, func(a, b string) int {
			return strings.Compare(a, b)
		})

		if slices.Equal(cached, digests) {
			return cache, nil
		}
	}

	seen := map[string]struct{}{}
	deleted := map[string]struct{}{}
	root := &treehelper.FileEntry{
		Name:     "/",
		Path:     "/",
		Type:     "directory",
		Children: make([]*treehelper.FileEntry, 0),
	}

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
			ftype := "file"
			mode := hdr.FileInfo().Mode()
			if mode.IsDir() {
				ftype = "directory"
			} else if mode&os.ModeSymlink != 0 {
				ftype = "symlink"
			}
			treehelper.AddToFileTree(root, strings.Split(hdr.Name, "/"), ftype)
		}
		rc.Close()
	}

	cache = &treehelper.FileTreeCache{
		Reference:  imageRef,
		Layers:     digests,
		Tree:       root,
		IsCached:   true,
		LastUpdate: time.Now(),
	}

	treehelper.WriteToCacheFile(cacheDir, imageRef, cache)

	cache.IsCached = false

	return cache, nil
}
