package prtag

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// File is the parsed representation of a .prtag file.
//
// MetaHeader is the bracketed section name without brackets (e.g. "metadata"),
// and is only meaningful when a delimiter ("---") was present.
type File struct {
	Name       string
	Body       string
	MetaHeader string
}

var (
	ErrInvalidHeader     = errors.New("invalid prtag header")
	ErrInvalidDelimiter  = errors.New("invalid prtag delimiter section")
	ErrNonEmptyMetadata  = errors.New("metadata section must be empty")
	ErrInvalidMetaHeader = errors.New("invalid metadata header")
)

// Parse parses the .prtag format described in docs/prtag.md.
func Parse(b []byte) (File, error) {
	s := normalizeNewlines(string(b))

	r := bufio.NewReader(strings.NewReader(s))
	firstLine, err := readLine(r)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return File{}, fmt.Errorf("%w: empty file", ErrInvalidHeader)
		}
		return File{}, err
	}

	name, ok := parseNameHeader(firstLine)
	if !ok {
		return File{}, fmt.Errorf("%w: expected '<name>:' as first line", ErrInvalidHeader)
	}

	var body strings.Builder
	delimiterFound := false

	for {
		line, err := readLine(r)
		if err != nil && !errors.Is(err, io.EOF) {
			return File{}, err
		}

		if trimLineEnd(line) == "---" {
			delimiterFound = true
			break
		}

		body.WriteString(line)

		if errors.Is(err, io.EOF) {
			break
		}
	}

	if !delimiterFound {
		return File{Name: name, Body: body.String()}, nil
	}

	// After delimiter: next non-empty line must be a bracketed section header.
	metaLine, err := readNextNonEmptyLine(r)
	if err != nil {
		return File{}, fmt.Errorf("%w: expected bracketed metadata header after delimiter", ErrInvalidDelimiter)
	}

	metaHeader, ok := parseBracketHeader(metaLine)
	if !ok {
		return File{}, fmt.Errorf("%w: expected '[section]' after delimiter", ErrInvalidMetaHeader)
	}

	rest, err := io.ReadAll(r)
	if err != nil {
		return File{}, err
	}
	if strings.TrimSpace(string(rest)) != "" {
		return File{}, ErrNonEmptyMetadata
	}

	return File{
		Name:       name,
		Body:       body.String(),
		MetaHeader: metaHeader,
	}, nil
}

// Format writes a canonical .prtag representation.
//
// It always emits the delimiter and a bracketed metadata header line, even if
// File.MetaHeader is empty.
func Format(f File) ([]byte, error) {
	name := strings.TrimSpace(f.Name)
	if name == "" || strings.ContainsAny(name, "\r\n:") {
		return nil, fmt.Errorf("%w: invalid name %q", ErrInvalidHeader, f.Name)
	}

	meta := strings.TrimSpace(f.MetaHeader)
	if meta == "" {
		meta = "metadata"
	}
	if strings.ContainsAny(meta, "\r\n[]") {
		return nil, fmt.Errorf("%w: invalid metadata header %q", ErrInvalidMetaHeader, f.MetaHeader)
	}

	body := normalizeNewlines(f.Body)
	// Ensure delimiter starts on its own line.
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}

	out := strings.Builder{}
	out.Grow(len(name) + len(body) + len(meta) + 32)
	out.WriteString(name)
	out.WriteString(":\n")
	out.WriteString(body)
	out.WriteString("---\n")
	out.WriteString("[")
	out.WriteString(meta)
	out.WriteString("]\n")

	return []byte(out.String()), nil
}

func ReadFile(path string) (File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	return Parse(b)
}

func WriteFile(path string, f File, perm fs.FileMode) error {
	b, err := Format(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, perm)
}

func normalizeNewlines(s string) string {
	// Replace Windows newlines first so we don't double-convert.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err == nil {
		return line, nil
	}
	if errors.Is(err, io.EOF) {
		// Return the final partial line.
		return line, io.EOF
	}
	return "", err
}

func readNextNonEmptyLine(r *bufio.Reader) (string, error) {
	for {
		line, err := readLine(r)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		if strings.TrimSpace(line) != "" {
			return line, nil
		}
		if errors.Is(err, io.EOF) {
			return "", io.EOF
		}
	}
}

func parseNameHeader(line string) (string, bool) {
	line = trimLineEnd(line)
	if !strings.HasSuffix(line, ":") {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimSuffix(line, ":"))
	if name == "" {
		return "", false
	}
	if strings.ContainsAny(name, "\r\n:") {
		return "", false
	}
	return name, true
}

func parseBracketHeader(line string) (string, bool) {
	line = strings.TrimSpace(trimLineEnd(line))
	if len(line) < 2 || line[0] != '[' || line[len(line)-1] != ']' {
		return "", false
	}
	inner := strings.TrimSpace(line[1 : len(line)-1])
	if inner == "" {
		return "", false
	}
	if strings.ContainsAny(inner, "\r\n[]") {
		return "", false
	}
	return inner, true
}

func trimLineEnd(s string) string {
	return strings.TrimSuffix(s, "\n")
}

