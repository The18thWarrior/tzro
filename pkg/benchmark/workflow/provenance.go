package workflow

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Capture tracked and non-ignored untracked source bytes, including dirty files.
// Git revision and diff hashes alone cannot reproduce untracked fixtures.
func snapshotSource(ctx context.Context, repo, destination string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("enumerate source snapshot: %w", err)
	}
	paths := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	sort.Strings(paths)
	path := filepath.Join(destination, "source.tar.gz")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, name := range paths {
		if name == "" {
			continue
		}
		absolute := filepath.Join(repo, name)
		info, err := os.Lstat(absolute)
		if os.IsNotExist(err) {
			continue
		} // A deleted tracked file is absent in this tree.
		if err != nil {
			return "", "", err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(absolute)
		}
		if err != nil {
			return "", "", err
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return "", "", err
		}
		header.Name, header.ModTime, header.AccessTime, header.ChangeTime = name, time.Time{}, time.Time{}, time.Time{}
		header.Uid, header.Gid, header.Uname, header.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(header); err != nil {
			return "", "", err
		}
		if info.Mode().IsRegular() {
			body, err := os.ReadFile(absolute)
			if err != nil {
				return "", "", err
			}
			if _, err := tw.Write(body); err != nil {
				return "", "", err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return "", "", err
	}
	if err := gz.Close(); err != nil {
		return "", "", err
	}
	if err := f.Close(); err != nil {
		return "", "", err
	}
	hash, err := fileDigest(path)
	return path, hash, err
}

func saveProgress(dir string, report *Report) error {
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "progress.json")
	if err := os.WriteFile(path+".tmp", append(body, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
