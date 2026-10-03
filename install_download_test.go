package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallDownloadVerified(t *testing.T) {
	root := t.TempDir()
	release := filepath.Join(root, "releases", "v-test")
	if err := os.MkdirAll(release, 0755); err != nil {
		t.Fatal(err)
	}
	latest := filepath.Join(root, "releases", "latest")
	if err := os.MkdirAll(latest, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(latest, "version.txt"), []byte("v-test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	binary := []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TZRO_TEST_CALLS\"\n")
	artifact := "tzro-darwin-arm64"
	if err := os.WriteFile(filepath.Join(release, artifact), binary, 0755); err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(binary), artifact)
	if err := os.WriteFile(filepath.Join(release, "SHA256SUMS"), []byte(checksum), 0644); err != nil {
		t.Fatal(err)
	}
	stubDir := filepath.Join(root, "tools")
	if err := os.MkdirAll(stubDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stubDir, "uname"), []byte("#!/bin/sh\ncase $1 in -s) echo Darwin;; -m) echo arm64;; esac\n"), 0755); err != nil {
		t.Fatal(err)
	}
	installer, err := filepath.Abs("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "custom install")
	calls := filepath.Join(root, "calls")
	env := append(os.Environ(), "HOME="+root, "SHELL=/bin/sh", "PATH="+stubDir+":"+os.Getenv("PATH"), "TZRO_INSTALL_DIR="+dest, "TZRO_VERSION=latest", "TZRO_SOURCE_BIN=", "TZRO_DOWNLOAD_BASE_URL=file://"+filepath.Join(root, "releases"), "TZRO_NO_MODIFY_PATH=1", "TZRO_TEST_CALLS="+calls)
	script, err := os.ReadFile(installer)
	if err != nil {
		t.Fatal(err)
	}
	run := func() ([]byte, error) {
		// Like curl | sh: the shell reads the script through standard input.
		cmd := exec.Command("sh")
		cmd.Stdin = strings.NewReader(string(script))
		cmd.Dir = root
		cmd.Env = env
		return cmd.CombinedOutput()
	}
	output, err := run()
	if err != nil {
		t.Fatalf("standalone install: %v\n%s", err, output)
	}
	installed, err := os.ReadFile(filepath.Join(dest, "bin", "tzro"))
	if err != nil || string(installed) != string(binary) {
		t.Fatalf("installed binary differs: %v", err)
	}
	called, err := os.ReadFile(calls)
	if err != nil || !strings.Contains(string(called), "init --hooks auto") {
		t.Fatalf("automatic setup missing: %s, %v", called, err)
	}
	if err := os.WriteFile(filepath.Join(release, artifact), []byte("corrupt"), 0755); err != nil {
		t.Fatal(err)
	}
	output, err = run()
	if err == nil {
		t.Fatalf("corrupt artifact accepted: %s", output)
	}
	installed, err = os.ReadFile(filepath.Join(dest, "bin", "tzro"))
	if err != nil || string(installed) != string(binary) {
		t.Fatalf("working binary replaced on failure: %v", err)
	}
	if err := os.Remove(filepath.Join(release, artifact)); err != nil {
		t.Fatal(err)
	}
	if output, err = run(); err == nil || !strings.Contains(string(output), "binary download failed") {
		t.Fatalf("missing artifact accepted: %v %s", err, output)
	}
	installed, err = os.ReadFile(filepath.Join(dest, "bin", "tzro"))
	if err != nil || string(installed) != string(binary) {
		t.Fatalf("working binary replaced after failed download: %v", err)
	}
}

func TestInstallCLIOnlyAndPartialFailure(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nexit 7\n"), 0755); err != nil {
		t.Fatal(err)
	}
	installer, err := filepath.Abs("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "install with ' quote")
	env := append(os.Environ(), "HOME="+root, "SHELL=/bin/sh", "TZRO_SOURCE_BIN="+source, "TZRO_INSTALL_DIR="+dest, "TZRO_NO_MODIFY_PATH=0")
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command("sh", append([]string{installer}, args...)...)
		cmd.Env = env
		cmd.Dir = root
		return cmd.CombinedOutput()
	}
	if out, err := run("--cli-only"); err != nil {
		t.Fatalf("CLI-only install: %v\n%s", err, out)
	}
	profile := filepath.Join(root, ".profile")
	before, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := run("--cli-only"); err != nil {
		t.Fatalf("repeat install: %v\n%s", err, out)
	}
	after, _ := os.ReadFile(profile)
	if string(before) != string(after) {
		t.Fatal("PATH entry duplicated")
	}
	lookup := exec.Command("sh", "-c", `. "$1"; command -v tzro`, "sh", profile)
	lookup.Env = env
	canonicalDest, err := filepath.EvalSymlinks(dest)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := lookup.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != filepath.Join(canonicalDest, "bin", "tzro") {
		t.Fatalf("invalid PATH quoting: %v\n%s", err, out)
	}
	if out, err := run(); err == nil || !strings.Contains(string(out), "agent setup is incomplete") || strings.Contains(string(out), "INSTALLATION COMPLETE") {
		t.Fatalf("setup failure hidden: %v\n%s", err, out)
	}
}

func TestInstallPlatformSelection(t *testing.T) {
	root := t.TempDir()
	release := filepath.Join(root, "releases", "v-test")
	tools := filepath.Join(root, "tools")
	for _, dir := range []string{release, tools} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	var sums strings.Builder
	for _, platform := range []string{"darwin-arm64", "darwin-amd64", "linux-amd64"} {
		data := []byte("#!/bin/sh\n# " + platform + "\nexit 0\n")
		if err := os.WriteFile(filepath.Join(release, "tzro-"+platform), data, 0755); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sums, "%x  tzro-%s\n", sha256.Sum256(data), platform)
	}
	if err := os.WriteFile(filepath.Join(release, "SHA256SUMS"), []byte(sums.String()), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "uname"), []byte("#!/bin/sh\ncase $1 in -s) echo \"$INSTALL_TEST_OS\";; -m) echo \"$INSTALL_TEST_ARCH\";; esac\n"), 0755); err != nil {
		t.Fatal(err)
	}
	installer, err := filepath.Abs("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ os, arch, want string }{{"Darwin", "arm64", "darwin-arm64"}, {"Darwin", "x86_64", "darwin-amd64"}, {"Linux", "x86_64", "linux-amd64"}, {"Linux", "aarch64", ""}} {
		t.Run(tc.os+tc.arch, func(t *testing.T) {
			dest := t.TempDir()
			cmd := exec.Command("sh", installer, "--cli-only", "--no-modify-path")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "HOME="+dest, "TZRO_INSTALL_DIR="+dest, "TZRO_SOURCE_BIN=", "TZRO_VERSION=v-test", "TZRO_DOWNLOAD_BASE_URL=file://"+filepath.Join(root, "releases"), "PATH="+tools+":"+os.Getenv("PATH"), "INSTALL_TEST_OS="+tc.os, "INSTALL_TEST_ARCH="+tc.arch)
			out, err := cmd.CombinedOutput()
			if tc.want == "" {
				if err == nil || !strings.Contains(string(out), "unsupported platform") {
					t.Fatalf("unsupported platform accepted: %v %s", err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("install: %v %s", err, out)
			}
			data, err := os.ReadFile(filepath.Join(dest, "bin", "tzro"))
			if err != nil || !strings.Contains(string(data), tc.want) {
				t.Fatalf("wrong artifact: %s %v", data, err)
			}
		})
	}
}

func TestInstallBashLoginPath(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, ".bash_profile")
	original := "# preserve login setup\n"
	if err := os.WriteFile(profile, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	installer, err := filepath.Abs("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", installer, "--cli-only")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "HOME="+root, "SHELL=/bin/bash", "TZRO_SOURCE_BIN="+source, "TZRO_INSTALL_DIR="+filepath.Join(root, "install"), "TZRO_NO_MODIFY_PATH=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install: %v %s", err, out)
	}
	for _, name := range []string{".bashrc", ".bash_profile"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !strings.Contains(string(data), "# tzro installer") {
			t.Fatalf("PATH not persisted for %s: %s %v", name, data, err)
		}
		if name == ".bash_profile" && !strings.HasPrefix(string(data), original) {
			t.Fatal("login configuration lost")
		}
	}
}
