package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// ApplyJSONRemoval reads path, decodes it as arbitrary JSON (an object or a
// bare array at the root), applies remove (which returns the mutated root,
// whether anything was actually removed, and an error), re-encodes, and
// atomically replaces path. It fails rather than silently no-op'ing if
// remove reports nothing was found (e.g. the file changed since the scan).
func ApplyJSONRemoval(fr FileReader, path string, remove func(root any) (newRoot any, removed bool, err error)) error {
	data, err := fr.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}

	newRoot, removed, err := remove(root)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !removed {
		return fmt.Errorf("%s: entry not found (file may have changed since scan)", path)
	}

	out, err := json.MarshalIndent(newRoot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	out = append(out, '\n')

	return atomicWriteFile(path, out)
}

// atomicWriteFile writes data to path via a temp file + rename, so a reader
// never observes a partially-written file.
func atomicWriteFile(path string, data []byte) error {
	tmp := path + ".extclean.tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// navigateMap walks path from root (root itself if path is empty) and
// returns the map found there.
func navigateMap(root any, path []string) (map[string]any, error) {
	cur := root
	for i, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected object at %v, got %T", path[:i], cur)
		}
		next, ok := m[key]
		if !ok {
			return nil, fmt.Errorf("key %q not found (path %v)", key, path[:i+1])
		}
		cur = next
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object at %v, got %T", path, cur)
	}
	return m, nil
}

// removeMapKey deletes key from the map reached by walking rootPath from
// root, reporting whether the key was present.
func removeMapKey(root any, rootPath []string, key string) (bool, error) {
	m, err := navigateMap(root, rootPath)
	if err != nil {
		return false, err
	}
	if _, ok := m[key]; !ok {
		return false, nil
	}
	delete(m, key)
	return true, nil
}

// filterOutFirst returns arr with the first element matching match removed.
func filterOutFirst(arr []any, match func(el any) bool) ([]any, bool) {
	out := make([]any, 0, len(arr))
	removed := false
	for _, el := range arr {
		if !removed && match(el) {
			removed = true
			continue
		}
		out = append(out, el)
	}
	return out, removed
}

// removeArrayElement removes the first element of the array reached by
// walking rootPath from root (root itself, if rootPath is empty, for a
// bare top-level array) matching match. Returns the (possibly new, for a
// top-level array) root and whether an element was removed.
func removeArrayElement(root any, rootPath []string, match func(el any) bool) (any, bool, error) {
	if len(rootPath) == 0 {
		arr, ok := root.([]any)
		if !ok {
			return root, false, fmt.Errorf("expected array at root, got %T", root)
		}
		newArr, removed := filterOutFirst(arr, match)
		return newArr, removed, nil
	}

	parentPath := rootPath[:len(rootPath)-1]
	key := rootPath[len(rootPath)-1]
	parent, err := navigateMap(root, parentPath)
	if err != nil {
		return root, false, err
	}
	val, ok := parent[key]
	if !ok {
		return root, false, fmt.Errorf("key %q not found", key)
	}
	arr, ok := val.([]any)
	if !ok {
		return root, false, fmt.Errorf("expected array at %v, got %T", rootPath, val)
	}
	newArr, removed := filterOutFirst(arr, match)
	parent[key] = newArr
	return root, removed, nil
}
