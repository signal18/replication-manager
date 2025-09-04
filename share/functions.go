package share

import (
	"io/fs"
	"os"
	"path/filepath"
)

func ReadFileFromSharedDir(withEmbed string, shareDir, filePath string) ([]byte, error) {
	if withEmbed == "ON" {
		return EmbededDbModuleFS.ReadFile(filePath)
	}
	return os.ReadFile(filepath.Join(shareDir, filePath))
}

func ListFilesInSharedDir(withEmbed string, shareDir, dirPath string) ([]string, error) {
	var entries []fs.DirEntry
	var err error

	if withEmbed == "ON" {
		entries, err = EmbededDbModuleFS.ReadDir(dirPath)
	} else {
		entries, err = os.ReadDir(filepath.Join(shareDir, dirPath))
	}
	if err != nil {
		return nil, err
	}

	var fileNames []string
	for _, entry := range entries {
		if !entry.IsDir() {
			fileNames = append(fileNames, entry.Name())
		}
	}
	return fileNames, nil
}
