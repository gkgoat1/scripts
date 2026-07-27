// archive-repack expands archives below a directory directly into one or more
// new archives. It never writes expanded members to disk.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	dsbzip2 "github.com/dsnet/compress/bzip2"
	"github.com/ulikunitz/xz"
)

const copyBufferSize = 1024 * 1024

type repackError struct{ message string }

func (e *repackError) Error() string { return e.message }

func failf(format string, args ...any) error {
	return &repackError{message: fmt.Sprintf(format, args...)}
}

type archiveFormat struct {
	name   string
	suffix string
}

var outputFormats = []archiveFormat{
	{name: "tar.gz", suffix: ".tar.gz"},
	{name: "tar.bz2", suffix: ".tar.bz2"},
	{name: "tar.xz", suffix: ".tar.xz"},
	{name: "tar.gz", suffix: ".tgz"},
	{name: "tar.bz2", suffix: ".tbz2"},
	{name: "tar.bz2", suffix: ".tbz"},
	{name: "tar.xz", suffix: ".txz"},
	{name: "zip", suffix: ".zip"},
	{name: "tar", suffix: ".tar"},
}

var tarSuffixes = []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar", ".tgz", ".tbz2", ".tbz", ".txz"}

func formatForDestination(path string) (archiveFormat, error) {
	lower := strings.ToLower(path)
	for _, format := range outputFormats {
		if strings.HasSuffix(lower, format.suffix) {
			return format, nil
		}
	}
	return archiveFormat{}, failf("destination must end in .zip, .tar, .tar.gz, .tgz, .tar.bz2, .tbz, .tar.xz, or .txz")
}

func inputFormat(path string) string {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".zip") {
		return "zip"
	}
	for _, suffix := range tarSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return "tar"
		}
	}
	if strings.HasSuffix(lower, ".gz") {
		return "gz"
	}
	return ""
}

func archiveStem(path, kind string) (string, error) {
	lower := strings.ToLower(path)
	if kind == "gz" {
		return path[:len(path)-len(".gz")], nil
	}
	if kind == "zip" && strings.HasSuffix(lower, ".zip") {
		return path[:len(path)-len(".zip")], nil
	}
	if kind == "tar" {
		for _, suffix := range tarSuffixes {
			if strings.HasSuffix(lower, suffix) {
				return path[:len(path)-len(suffix)], nil
			}
		}
	}
	return "", fmt.Errorf("unknown archive suffix for %q", path)
}

func safeMemberPath(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") {
		return "", failf("unsafe archive member path: %q", name)
	}
	parts := make([]string, 0, strings.Count(name, "/")+1)
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", failf("unsafe archive member path: %q", name)
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 || strings.Contains(parts[0], ":") {
		return "", failf("unsafe archive member path: %q", name)
	}
	return strings.Join(parts, "/"), nil
}

type fileSource struct {
	name  string
	size  int64
	mtime time.Time
	open  func() (io.ReadCloser, error)
}

type combinedReadCloser struct {
	io.Reader
	closers []io.Closer
}

func (r *combinedReadCloser) Close() error {
	var errs []error
	for i := len(r.closers) - 1; i >= 0; i-- {
		if err := r.closers[i].Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func regularFileSource(path, name string) (fileSource, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileSource{}, err
	}
	return fileSource{name: name, size: info.Size(), mtime: info.ModTime(), open: func() (io.ReadCloser, error) {
		return os.Open(path)
	}}, nil
}

func openGzip(path string) (io.ReadCloser, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	reader, err := gzip.NewReader(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	return &combinedReadCloser{Reader: reader, closers: []io.Closer{file, reader}}, nil
}

func gzipFileSource(path, name string, needSize bool) (fileSource, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileSource{}, err
	}
	open := func() (io.ReadCloser, error) { return openGzip(path) }
	size := int64(0)
	if needSize {
		size, err = streamSize(open)
		if err != nil {
			return fileSource{}, err
		}
	}
	return fileSource{name: name, size: size, mtime: info.ModTime(), open: open}, nil
}

func streamSize(open func() (io.ReadCloser, error)) (int64, error) {
	stream, err := open()
	if err != nil {
		return 0, err
	}
	defer stream.Close()
	var total int64
	buffer := make([]byte, copyBufferSize)
	for {
		n, readErr := stream.Read(buffer)
		total += int64(n)
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return 0, readErr
		}
	}
}

// destinationWriteError marks errors emitted by an output archive, as opposed
// to an error while reading an input archive.
type destinationWriteError struct{ err error }

func (e *destinationWriteError) Error() string { return "destination write: " + e.err.Error() }
func (e *destinationWriteError) Unwrap() error { return e.err }

type destinationWriter struct{ writer io.Writer }

func (w destinationWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	if err != nil {
		return n, &destinationWriteError{err: err}
	}
	return n, nil
}

func isFileTooLarge(err error) bool {
	var outputErr *destinationWriteError
	if !errors.As(err, &outputErr) {
		return false
	}
	if errors.Is(err, syscall.EFBIG) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "file too large")
}

type output struct {
	path       string
	format     archiveFormat
	file       *os.File
	zip        *zip.Writer
	tar        *tar.Writer
	compressor io.WriteCloser
	closed     bool
}

func newOutput(path string, format archiveFormat) (*output, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return nil, err
	}
	out := &output{path: path, format: format, file: file}
	writer := io.Writer(destinationWriter{writer: file})
	if format.name == "zip" {
		out.zip = zip.NewWriter(writer)
		return out, nil
	}
	if format.name == "tar.gz" {
		out.compressor = gzip.NewWriter(writer)
		writer = out.compressor
	} else if format.name == "tar.bz2" {
		out.compressor, err = dsbzip2.NewWriter(writer, nil)
		if err != nil {
			file.Close()
			os.Remove(path)
			return nil, err
		}
		writer = out.compressor
	} else if format.name == "tar.xz" {
		out.compressor, err = xz.NewWriter(writer)
		if err != nil {
			file.Close()
			os.Remove(path)
			return nil, err
		}
		writer = out.compressor
	}
	out.tar = tar.NewWriter(writer)
	return out, nil
}

func (o *output) close() error {
	if o.closed {
		return nil
	}
	o.closed = true
	var errs []error
	if o.zip != nil {
		if err := o.zip.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if o.tar != nil {
		if err := o.tar.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if o.compressor != nil {
		if err := o.compressor.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := o.file.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (o *output) addDir(name string, mtime time.Time) error {
	if o.zip != nil {
		header := &zip.FileHeader{Name: name + "/", Method: zip.Store}
		header.SetModTime(mtime)
		_, err := o.zip.CreateHeader(header)
		return err
	}
	return o.tar.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755, ModTime: mtime})
}

func copyStream(source io.Reader, destination io.Writer) error {
	buffer := make([]byte, copyBufferSize)
	for {
		n, readErr := source.Read(buffer)
		if n > 0 {
			written, writeErr := destination.Write(buffer[:n])
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (o *output) addFile(source fileSource) error {
	if o.zip != nil {
		header := &zip.FileHeader{Name: source.name, Method: zip.Deflate}
		header.SetModTime(source.mtime)
		target, err := o.zip.CreateHeader(header)
		if err != nil {
			return err
		}
		stream, err := source.open()
		if err != nil {
			return err
		}
		defer stream.Close()
		return copyStream(stream, target)
	}
	if err := o.tar.WriteHeader(&tar.Header{Name: source.name, Mode: 0o644, Size: source.size, ModTime: source.mtime, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	stream, err := source.open()
	if err != nil {
		return err
	}
	defer stream.Close()
	return copyStream(stream, o.tar)
}

type claims struct{ entries map[string]string }

func newClaims() *claims { return &claims{entries: make(map[string]string)} }

func (c *claims) canAdd(name, kind string) (bool, error) {
	for parent := filepath.ToSlash(filepath.Dir(name)); parent != "." && parent != "/"; parent = filepath.ToSlash(filepath.Dir(parent)) {
		if c.entries[parent] == "file" {
			return false, failf("path conflict: %q is below file %q", name, parent)
		}
	}
	if previous, ok := c.entries[name]; ok {
		if previous == "dir" && kind == "dir" {
			return false, nil
		}
		return false, failf("duplicate archive path: %q", name)
	}
	if kind == "file" {
		for existing := range c.entries {
			if strings.HasPrefix(existing, name+"/") {
				return false, failf("path conflict: file %q would replace an existing directory", name)
			}
		}
	}
	return true, nil
}

func (c *claims) add(name, kind string) { c.entries[name] = kind }

type outputEntry struct {
	dir    bool
	name   string
	mtime  time.Time
	source fileSource
}

type archiveSet struct {
	destination string
	format      archiveFormat
	current     *output
	claims      *claims
	part        int
	entries     []outputEntry
	paths       map[string]struct{}
}

func newArchiveSet(destination string, format archiveFormat) (*archiveSet, error) {
	out, err := newOutput(destination, format)
	if err != nil {
		return nil, err
	}
	return &archiveSet{destination: destination, format: format, current: out, claims: newClaims(), part: 1, paths: map[string]struct{}{destination: {}}}, nil
}

func (s *archiveSet) nextPath() string {
	if s.part == 1 {
		return s.destination
	}
	return s.destination[:len(s.destination)-len(s.format.suffix)] + fmt.Sprintf("-%d", s.part) + s.destination[len(s.destination)-len(s.format.suffix):]
}

func (s *archiveSet) rollover(cause error) error {
	// A failed write can leave a truncated member in the current archive. Rebuild
	// that archive from entries which completed before the failure, then retry the
	// failed entry in a new archive. Completed parts stay independently readable.
	path := s.current.path
	entries := s.entries
	_ = s.current.close()
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove incomplete archive %q: %w", path, err)
	}
	rebuilt, err := newOutput(path, s.format)
	if err != nil {
		return fmt.Errorf("rebuild archive %q after %w: %w", path, cause, err)
	}
	for _, entry := range entries {
		if entry.dir {
			err = rebuilt.addDir(entry.name, entry.mtime)
		} else {
			err = rebuilt.addFile(entry.source)
		}
		if err != nil {
			rebuilt.close()
			return fmt.Errorf("rebuild archive %q after %w: %w", path, cause, err)
		}
	}
	if err := rebuilt.close(); err != nil {
		return fmt.Errorf("finish rebuilt archive %q after %w: %w", path, cause, err)
	}
	s.part++
	continuation := s.nextPath()
	out, err := newOutput(continuation, s.format)
	if err != nil {
		return fmt.Errorf("create continuation archive %q after %w: %w", continuation, cause, err)
	}
	s.current = out
	s.paths[continuation] = struct{}{}
	s.entries = nil
	return nil
}

func (s *archiveSet) addDir(name string, mtime time.Time) error {
	name, err := safeMemberPath(name)
	if err != nil {
		return err
	}
	add, err := s.claims.canAdd(name, "dir")
	if err != nil || !add {
		return err
	}
	if err := s.current.addDir(name, mtime); err != nil {
		if !isFileTooLarge(err) || len(s.entries) == 0 {
			return err
		}
		if err := s.rollover(err); err != nil {
			return err
		}
		if err := s.current.addDir(name, mtime); err != nil {
			return err
		}
	}
	s.claims.add(name, "dir")
	s.entries = append(s.entries, outputEntry{dir: true, name: name, mtime: mtime})
	return nil
}

func (s *archiveSet) addFile(source fileSource) error {
	name, err := safeMemberPath(source.name)
	if err != nil {
		return err
	}
	source.name = name
	add, err := s.claims.canAdd(name, "file")
	if err != nil || !add {
		return err
	}
	if err := s.current.addFile(source); err != nil {
		if !isFileTooLarge(err) || len(s.entries) == 0 {
			return err
		}
		if err := s.rollover(err); err != nil {
			return err
		}
		if err := s.current.addFile(source); err != nil {
			return err
		}
	}
	s.claims.add(name, "file")
	s.entries = append(s.entries, outputEntry{name: name, mtime: source.mtime, source: source})
	return nil
}

func writeEntries(path string, format archiveFormat, entries []outputEntry) error {
	out, err := newOutput(path, format)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.dir {
			err = out.addDir(entry.name, entry.mtime)
		} else {
			err = out.addFile(entry.source)
		}
		if err != nil {
			out.close()
			return err
		}
	}
	return out.close()
}

func (s *archiveSet) close() error {
	if err := s.current.close(); err == nil || !isFileTooLarge(err) {
		return err
	}
	// Some formats (notably zip and compressed tar) can defer writes until
	// Close. Repack completed entries into the largest valid prefixes instead of
	// leaving a final oversized archive behind.
	path := s.current.path
	entries := s.entries
	if err := os.Remove(path); err != nil {
		return err
	}
	for len(entries) > 0 {
		count := 0
		var lastErr error
		for candidate := len(entries); candidate > 0; candidate-- {
			lastErr = writeEntries(path, s.format, entries[:candidate])
			if lastErr == nil {
				count = candidate
				break
			}
			_ = os.Remove(path)
			if !isFileTooLarge(lastErr) {
				return lastErr
			}
		}
		if count == 0 {
			return lastErr
		}
		entries = entries[count:]
		if len(entries) == 0 {
			return nil
		}
		s.part++
		path = s.nextPath()
		s.paths[path] = struct{}{}
	}
	return nil
}

func (s *archiveSet) isOutputPath(path string) bool {
	_, ok := s.paths[path]
	return ok
}

func zipMemberSource(path string, index int, name string, size int64, mtime time.Time) fileSource {
	return fileSource{name: name, size: size, mtime: mtime, open: func() (io.ReadCloser, error) {
		archive, err := zip.OpenReader(path)
		if err != nil {
			return nil, err
		}
		if index >= len(archive.File) {
			archive.Close()
			return nil, fmt.Errorf("zip member %d disappeared from %q", index, path)
		}
		stream, err := archive.File[index].Open()
		if err != nil {
			archive.Close()
			return nil, err
		}
		return &combinedReadCloser{Reader: stream, closers: []io.Closer{archive, stream}}, nil
	}}
}

func tarReader(path string) (*tar.Reader, []io.Closer, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	closers := []io.Closer{file}
	var reader io.Reader = file
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			file.Close()
			return nil, nil, err
		}
		reader = gzipReader
		closers = append(closers, gzipReader)
	case strings.HasSuffix(lower, ".tar.bz2") || strings.HasSuffix(lower, ".tbz") || strings.HasSuffix(lower, ".tbz2"):
		reader = bzip2.NewReader(file)
	case strings.HasSuffix(lower, ".tar.xz") || strings.HasSuffix(lower, ".txz"):
		xzReader, err := xz.NewReader(file)
		if err != nil {
			file.Close()
			return nil, nil, err
		}
		reader = xzReader
	}
	return tar.NewReader(reader), closers, nil
}

func tarMemberSource(path string, index int, name string, size int64, mtime time.Time) fileSource {
	return fileSource{name: name, size: size, mtime: mtime, open: func() (io.ReadCloser, error) {
		reader, closers, err := tarReader(path)
		if err != nil {
			return nil, err
		}
		for i := 0; ; i++ {
			_, nextErr := reader.Next()
			if nextErr != nil {
				for _, closer := range closers {
					closer.Close()
				}
				return nil, nextErr
			}
			if i == index {
				return &combinedReadCloser{Reader: reader, closers: closers}, nil
			}
		}
	}}
}

func addZip(path, prefix string, output *archiveSet) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer archive.Close()
	for index, member := range archive.File {
		memberName, err := safeMemberPath(member.Name)
		if err != nil {
			return err
		}
		name := prefix + "/" + memberName
		if member.FileInfo().IsDir() {
			if err := output.addDir(name, member.ModTime()); err != nil {
				return err
			}
			continue
		}
		if err := output.addFile(zipMemberSource(path, index, name, int64(member.UncompressedSize64), member.ModTime())); err != nil {
			return err
		}
	}
	return nil
}

func addTar(path, prefix string, output *archiveSet) error {
	reader, closers, err := tarReader(path)
	if err != nil {
		return err
	}
	defer func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i].Close()
		}
	}()
	for index := 0; ; index++ {
		member, nextErr := reader.Next()
		if nextErr == io.EOF {
			return nil
		}
		if nextErr != nil {
			return nextErr
		}
		memberName, err := safeMemberPath(member.Name)
		if err != nil {
			return err
		}
		name := prefix + "/" + memberName
		switch member.Typeflag {
		case tar.TypeDir:
			if err := output.addDir(name, member.ModTime); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := output.addFile(tarMemberSource(path, index, name, member.Size, member.ModTime)); err != nil {
				return err
			}
		default:
			return failf("unsupported tar member type for %q", member.Name)
		}
	}
}

func walkSource(source, destination string, output *archiveSet) error {
	rootName, err := safeMemberPath(filepath.Base(source))
	if err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if err := output.addDir(rootName, info.ModTime()); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		if output.isOutputPath(path) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return failf("refusing to follow symlink: %s", path)
		}
		metadata, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		archiveName := rootName + "/" + filepath.ToSlash(relative)
		if entry.IsDir() {
			return output.addDir(archiveName, metadata.ModTime())
		}
		kind := inputFormat(path)
		switch kind {
		case "zip", "tar":
			prefix, err := archiveStem(archiveName, kind)
			if err != nil {
				return err
			}
			if err := output.addDir(prefix, metadata.ModTime()); err != nil {
				return err
			}
			if kind == "zip" {
				return addZip(path, prefix, output)
			}
			return addTar(path, prefix, output)
		case "gz":
			name, err := archiveStem(archiveName, kind)
			if err != nil {
				return err
			}
			source, err := gzipFileSource(path, name, output.format.name != "zip")
			if err != nil {
				return err
			}
			return output.addFile(source)
		default:
			source, err := regularFileSource(path, archiveName)
			if err != nil {
				return err
			}
			return output.addFile(source)
		}
	})
}

func run(sourceArg, destinationArg string) error {
	source, err := filepath.Abs(sourceArg)
	if err != nil {
		return err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return failf("source is not a directory: %s", sourceArg)
	}
	destination, err := filepath.Abs(destinationArg)
	if err != nil {
		return err
	}
	format, err := formatForDestination(destination)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return failf("destination already exists (refusing to overwrite): %s", destinationArg)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	output, err := newArchiveSet(destination, format)
	if err != nil {
		return err
	}
	if err := walkSource(source, destination, output); err != nil {
		output.close()
		// The current part may contain a partial member. Earlier finalized parts,
		// if any, remain available for diagnosis and recovery.
		_ = os.Remove(output.current.path)
		return err
	}
	if err := output.close(); err != nil {
		_ = os.Remove(output.current.path)
		return err
	}
	return nil
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: archive-repack SOURCE DESTINATION\\n\\n")
		fmt.Fprintln(flag.CommandLine.Output(), "Recursively expand .zip, .tar*, and single-file .gz inputs into new archives without extracting to disk.")
	}
	flag.Parse()
	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(flag.Arg(0), flag.Arg(1)); err != nil {
		fmt.Fprintf(os.Stderr, "archive-repack: %v\\n", err)
		os.Exit(2)
	}
}
