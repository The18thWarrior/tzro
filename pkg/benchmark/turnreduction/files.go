package turnreduction

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func treeHashes(root string) (map[string]string, error) {
	hashes := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected symlink: %s", rel)
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "dist" || info.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected non-regular input: %s", rel)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected non-regular input: %s", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hashes[filepath.ToSlash(rel)] = fmt.Sprintf("%o:%x", info.Mode().Perm(), sha256.Sum256(data))
		return nil
	})
	return hashes, err
}

func protectedChanges(before, after map[string]string, editable []string) []string {
	allowed := map[string]bool{}
	for _, path := range editable {
		allowed[path] = true
	}
	var changed []string
	for path, hash := range before {
		if !allowed[path] && after[path] != hash {
			changed = append(changed, "protected input changed: "+path)
		}
	}
	sort.Strings(changed)
	return changed
}

// copyDirRecursive copies all files from src to dst, including subdirectories.
func copyDirRecursive(src, dst string) error {
	return copyDirContext(context.Background(), src, dst)
}

func copyDirContext(ctx context.Context, src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		if err := ctx.Err(); err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected symlink: %s", rel)
		}

		if info.IsDir() {
			// Skip .git and __pycache__ entirely.
			base := filepath.Base(path)
			if base == ".git" || base == "__pycache__" || base == "dist" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, info.Mode())
		}

		if !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected non-regular input: %s", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
