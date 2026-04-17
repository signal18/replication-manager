package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/signal18/replication-manager/config"
)

type convertResult struct {
	FilePath                   string
	Changed                    bool
	UpdatedParentNames         int
	InferredParentNames        int
	UpdatedLevels              int
	UpdatedRootSourcePaths     int
	UpdatedEmptySourcePaths    int
	UnresolvedParentReferences []string
}

func main() {
	inPath := flag.String("in", "", "Input file or directory containing app templates")
	outPath := flag.String("out", "", "Output file path (single-file mode only)")
	inPlace := flag.Bool("write", false, "Write changes in-place")
	checkOnly := flag.Bool("check", false, "Check mode (no writes), exits non-zero when non-canonical templates are found")
	flag.Parse()

	if strings.TrimSpace(*inPath) == "" {
		exitf("missing required -in path")
	}

	files, err := gatherTemplateFiles(*inPath)
	if err != nil {
		exitf("failed to gather template files: %v", err)
	}
	if len(files) == 0 {
		exitf("no template files found under %q", *inPath)
	}

	if *outPath != "" && len(files) != 1 {
		exitf("-out can only be used with a single input file")
	}
	if *outPath != "" && *inPlace {
		exitf("-out and -write are mutually exclusive")
	}

	var changedCount int
	var unresolvedCount int

	for _, file := range files {
		res, output, err := convertTemplateFile(file)
		if err != nil {
			exitf("%s: %v", file, err)
		}

		if len(res.UnresolvedParentReferences) > 0 {
			unresolvedCount += len(res.UnresolvedParentReferences)
			for _, unresolved := range res.UnresolvedParentReferences {
				fmt.Fprintf(os.Stderr, "WARN %s: unresolved parentname %q (must reference path name)\n", file, unresolved)
			}
		}

		if res.Changed {
			changedCount++
			fmt.Printf("UPDATED %s (parentname=%d inferred-parent=%d level=%d srcpath'/'=%d srcpath-empty=%d)\n",
				file, res.UpdatedParentNames, res.InferredParentNames, res.UpdatedLevels, res.UpdatedRootSourcePaths, res.UpdatedEmptySourcePaths)
		} else {
			fmt.Printf("OK %s\n", file)
		}

		if *checkOnly {
			continue
		}

		if *outPath != "" {
			if err := os.WriteFile(*outPath, output, 0o644); err != nil {
				exitf("failed writing %s: %v", *outPath, err)
			}
			continue
		}

		if *inPlace && res.Changed {
			if err := os.WriteFile(file, output, 0o644); err != nil {
				exitf("failed writing %s: %v", file, err)
			}
		}
	}

	if *checkOnly && (changedCount > 0 || unresolvedCount > 0) {
		exitf("non-canonical templates detected (changed=%d unresolved-parent=%d)", changedCount, unresolvedCount)
	}
}

func gatherTemplateFiles(inPath string) ([]string, error) {
	info, err := os.Stat(inPath)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return []string{inPath}, nil
	}

	files := make([]string, 0)
	err = filepath.WalkDir(inPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if strings.HasSuffix(name, ".toml") || strings.HasSuffix(name, ".toml.sample") || strings.HasSuffix(name, ".sample") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func convertTemplateFile(filePath string) (convertResult, []byte, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return convertResult{}, nil, err
	}

	canonicalContent, canonicalRes, err := config.CanonicalizeAppTemplateTOML(content)
	if err != nil {
		return convertResult{}, nil, err
	}

	res := convertResult{
		FilePath:                   filePath,
		Changed:                    canonicalRes.Changed,
		UpdatedParentNames:         canonicalRes.UpdatedParentNames,
		InferredParentNames:        canonicalRes.InferredParentNames,
		UpdatedLevels:              canonicalRes.UpdatedLevels,
		UpdatedRootSourcePaths:     canonicalRes.UpdatedRootSourcePaths,
		UpdatedEmptySourcePaths:    canonicalRes.UpdatedEmptySourcePaths,
		UnresolvedParentReferences: canonicalRes.UnresolvedParentReferences,
	}

	if !res.Changed {
		return res, content, nil
	}
	return res, canonicalContent, nil
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
