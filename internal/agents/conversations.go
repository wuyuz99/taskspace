package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// ClaudeConversations moves Claude Code's history for a directory: the
// folder under its projects directory named after the directory's path.
type ClaudeConversations struct{}

// claudeProjectUnsafe is what Claude Code replaces with "-" in a directory's
// path to name its project folder.
var claudeProjectUnsafe = regexp.MustCompile(`[^A-Za-z0-9]`)

// claudeProjectMax is the longest folder name Claude Code uses as is. It
// shortens longer ones its own way, which tspace does not guess at.
const claudeProjectMax = 200

// ClaudeProject is the name of Claude Code's project folder for dir, or ""
// for a path too long to name exactly.
func ClaudeProject(dir string) string {
	name := claudeProjectUnsafe.ReplaceAllString(dir, "-")
	if len(name) > claudeProjectMax {
		return ""
	}
	return name
}

// claudeProjectsDir is where Claude Code keeps its project folders.
func claudeProjectsDir() (string, error) {
	dir, err := ClaudeConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "projects"), nil
}

// Carry implements workspace.ConversationCarrier. It renames the folder,
// and refuses to merge into one the new path already has.
func (ClaudeConversations) Carry(from, to string) (bool, error) {
	dir, err := claudeProjectsDir()
	if err != nil {
		return false, err
	}
	oldName := ClaudeProject(from)
	if oldName == "" {
		return false, nil
	}
	src := filepath.Join(dir, oldName)
	if _, err := os.Lstat(src); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	newName := ClaudeProject(to)
	if newName == "" {
		return false, fmt.Errorf("%s is too long a path to name Claude Code's folder for it", to)
	}
	if newName == oldName {
		return true, nil
	}
	dst := filepath.Join(dir, newName)
	if _, err := os.Lstat(dst); err == nil {
		return false, fmt.Errorf("%s already exists; merge %s into it by hand", dst, src)
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.Rename(src, dst); err != nil {
		return false, err
	}
	return true, nil
}
