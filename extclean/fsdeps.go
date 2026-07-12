package main

import (
	"os"
	"os/exec"
)

// PathChecker reports whether a filesystem path exists.
type PathChecker interface {
	Exists(path string) bool
	IsNonEmptyDir(path string) bool
}

// PathResolver resolves a bare command name via PATH.
type PathResolver interface {
	LookPath(name string) (string, error)
}

// FileReader reads whole files.
type FileReader interface {
	ReadFile(path string) ([]byte, error)
}

type osPathChecker struct{}

func (osPathChecker) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (osPathChecker) IsNonEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

type osPathResolver struct{}

func (osPathResolver) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

type osFileReader struct{}

func (osFileReader) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
