package vfs

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// VirtualFilesystem represents an in-memory POSIX-like filesystem simulation.
type VirtualFilesystem struct {
	CWD     string
	DirTree map[string]interface{}
	mutex   sync.Mutex
}

func NewVirtualFilesystem(config map[string]interface{}) *VirtualFilesystem {
	vfs := &VirtualFilesystem{
		CWD:     "/",
		DirTree: make(map[string]interface{}),
	}
	if config == nil {
		return vfs
	}
	// Dynamically find and unwrap GorillaFileSystem configurations
	if gfs, ok := config["GorillaFileSystem"].(map[string]interface{}); ok {
		for k, v := range gfs {
			vfs.DirTree[k] = v
		}
	} else {
		// Fallback: check if the whole map itself is the tree
		for k, v := range config {
			vfs.DirTree[k] = v
		}
	}
	return vfs
}

func (v *VirtualFilesystem) resolveNode(path string) (map[string]interface{}, bool) {
	cleanPath := filepath.Clean(path)
	if cleanPath == "/" || cleanPath == "." || cleanPath == "" {
		return v.DirTree, true
	}

	parts := strings.Split(strings.Trim(cleanPath, "/"), "/")
	curr := v.DirTree

	for _, part := range parts {
		if part == "" {
			continue
		}

		var next interface{}
		var ok bool

		if contents, hasContents := curr["contents"].(map[string]interface{}); hasContents {
			next, ok = contents[part]
		} else if rootNode, hasRoot := curr["root"].(map[string]interface{}); hasRoot && part == "root" {
			next = rootNode
			ok = true
		} else {
			next, ok = curr[part]
		}

		if !ok {
			return nil, false
		}

		nextMap, ok := next.(map[string]interface{})
		if !ok {
			return nil, false
		}
		curr = nextMap
	}
	return curr, true
}

func (v *VirtualFilesystem) RenderEnvironmentBlock() string {
	v.mutex.Lock()
	defer v.mutex.Unlock()

	node, ok := v.resolveNode(v.CWD)
	var lines []string
	lines = append(lines, "[Active Environment State]")
	lines = append(lines, fmt.Sprintf("CWD: %s", v.CWD))
	lines = append(lines, "Visible Files & Folders in CWD:")

	if ok && node != nil {
		contents, hasContents := node["contents"].(map[string]interface{})
		if !hasContents {
			contents = node
		}

		count := 0
		for k, val := range contents {
			if k == "type" || k == "contents" || k == "content" {
				continue
			}
			item, ok := val.(map[string]interface{})
			if !ok {
				continue
			}
			t, _ := item["type"].(string)
			if t == "" {
				t = "directory"
			}
			lines = append(lines, fmt.Sprintf("- %s (%s)", k, t))
			count++
		}
		if count == 0 {
			lines = append(lines, "(empty directory)")
		}
	} else {
		lines = append(lines, "(unreachable CWD)")
	}

	return strings.Join(lines, "\n")
}

func getStringArg(args map[string]interface{}, key string) string {
	val, _ := args[key].(string)
	if val == "" {
		if slice, ok := args[key].([]interface{}); ok && len(slice) > 0 {
			val, _ = slice[0].(string)
		}
	}
	return val
}

func (v *VirtualFilesystem) Mutate(toolName string, args map[string]interface{}) {
	v.mutex.Lock()
	defer v.mutex.Unlock()

	// Strip out dynamic class prefix (e.g. GorillaFileSystem.cd -> cd)
	cleanName := toolName
	if idx := strings.Index(cleanName, "."); idx != -1 {
		cleanName = cleanName[idx+1:]
	}

	switch cleanName {
	case "cd":
		folder := getStringArg(args, "folder")
		if folder == "" {
			return
		}

		var targetPath string
		if folder == "/" {
			targetPath = "/"
		} else if folder == ".." {
			targetPath = filepath.Dir(v.CWD)
		} else {
			targetPath = filepath.Join(v.CWD, folder)
		}
		targetPath = filepath.Clean(targetPath)

		if _, ok := v.resolveNode(targetPath); ok {
			v.CWD = targetPath
		}
	case "mkdir":
		dirName := getStringArg(args, "dir_name")
		if dirName == "" {
			return
		}

		node, ok := v.resolveNode(v.CWD)
		if ok && node != nil {
			contents, hasContents := node["contents"].(map[string]interface{})
			if !hasContents {
				if _, hasType := node["type"]; !hasType {
					node["contents"] = make(map[string]interface{})
					contents = node["contents"].(map[string]interface{})
				} else {
					contents = node
				}
			}
			contents[dirName] = map[string]interface{}{
				"type":     "directory",
				"contents": make(map[string]interface{}),
			}
		}
	case "rm":
		fileName := getStringArg(args, "file_name")
		if fileName == "" {
			return
		}

		node, ok := v.resolveNode(v.CWD)
		if ok && node != nil {
			contents, hasContents := node["contents"].(map[string]interface{})
			if !hasContents {
				contents = node
			}
			delete(contents, fileName)
		}
	case "rmdir":
		dirName := getStringArg(args, "dir_name")
		if dirName == "" {
			return
		}

		node, ok := v.resolveNode(v.CWD)
		if ok && node != nil {
			contents, hasContents := node["contents"].(map[string]interface{})
			if !hasContents {
				contents = node
			}
			delete(contents, dirName)
		}
	case "touch":
		fileName := getStringArg(args, "file_name")
		if fileName == "" {
			return
		}

		node, ok := v.resolveNode(v.CWD)
		if ok && node != nil {
			contents, hasContents := node["contents"].(map[string]interface{})
			if !hasContents {
				if _, hasType := node["type"]; !hasType {
					node["contents"] = make(map[string]interface{})
					contents = node["contents"].(map[string]interface{})
				} else {
					contents = node
				}
			}
			contents[fileName] = map[string]interface{}{
				"type":    "file",
				"content": "",
			}
		}
	}
}
