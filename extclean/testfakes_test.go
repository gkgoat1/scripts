package main

import "fmt"

type fakePathChecker struct {
	existing     map[string]bool
	nonEmptyDirs map[string]bool
}

func newFakePathChecker() *fakePathChecker {
	return &fakePathChecker{existing: map[string]bool{}, nonEmptyDirs: map[string]bool{}}
}

func (f *fakePathChecker) Exists(path string) bool        { return f.existing[path] }
func (f *fakePathChecker) IsNonEmptyDir(path string) bool  { return f.nonEmptyDirs[path] }
func (f *fakePathChecker) setExists(path string)           { f.existing[path] = true }
func (f *fakePathChecker) setNonEmptyDir(path string)      { f.nonEmptyDirs[path] = true; f.existing[path] = true }

type fakePathResolver struct {
	paths map[string]string
}

func newFakePathResolver() *fakePathResolver {
	return &fakePathResolver{paths: map[string]string{}}
}

func (f *fakePathResolver) LookPath(name string) (string, error) {
	if p, ok := f.paths[name]; ok {
		return p, nil
	}
	return "", fmt.Errorf("exec: %q: executable file not found in $PATH", name)
}

func (f *fakePathResolver) setResolves(name, path string) { f.paths[name] = path }

type fakeFileReader struct {
	files map[string][]byte
}

func newFakeFileReader() *fakeFileReader {
	return &fakeFileReader{files: map[string][]byte{}}
}

func (f *fakeFileReader) ReadFile(path string) ([]byte, error) {
	if b, ok := f.files[path]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("open %s: no such file or directory", path)
}

func (f *fakeFileReader) setFile(path, content string) { f.files[path] = []byte(content) }

type fakeInstalledChecker struct {
	claude, cursor, codex, pi bool
}

func (f fakeInstalledChecker) ClaudeCodeInstalled() bool { return f.claude }
func (f fakeInstalledChecker) CursorInstalled() bool     { return f.cursor }
func (f fakeInstalledChecker) CodexInstalled() bool      { return f.codex }
func (f fakeInstalledChecker) PiInstalled() bool         { return f.pi }

type fakeTomlReader struct {
	jsonByPath map[string][]byte
	errByPath  map[string]error
}

func newFakeTomlReader() *fakeTomlReader {
	return &fakeTomlReader{jsonByPath: map[string][]byte{}, errByPath: map[string]error{}}
}

func (f *fakeTomlReader) ReadTomlAsJSON(path string) ([]byte, error) {
	if err, ok := f.errByPath[path]; ok {
		return nil, err
	}
	if b, ok := f.jsonByPath[path]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("no fixture for %s", path)
}

func (f *fakeTomlReader) setJSON(path, json string) { f.jsonByPath[path] = []byte(json) }
