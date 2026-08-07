// Package fsutil provides filesystem helpers shared across the scripts.
//
// The primary motivation is resilient removal: on shared or networked
// volumes (e.g. an SMB-mounted "/Volumes/My Shared Files"), a single
// "operation not permitted" (EPERM) or "permission denied" (EACCES) on one
// directory should not abort an entire batch cleanup. These helpers treat
// permission-class errors as non-fatal — logging them if an [io.Writer] is
// supplied — while still surfacing genuine I/O errors.
package fsutil

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// IsPermission reports whether err is a permission-class error that callers
// may safely treat as non-fatal. It covers:
//   - [os.ErrPermission] (EPERM and EACCES on all platforms),
//   - the wrapped syscall errno via errors.Is, and
//   - the literal English substrings "operation not permitted" and
//     "permission denied", which some network/POSIX layers surface without a
//     clean errno wrapping (notably SMB shares on macOS).
func IsPermission(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		if errors.Is(pathErr.Err, os.ErrPermission) {
			return true
		}
	}
	s := err.Error()
	return strings.Contains(s, "operation not permitted") || strings.Contains(s, "permission denied")
}

// IsNotExist reports whether err indicates the path is already gone, which
// for removal purposes is success. It wraps errors.Is(os.ErrNotExist) for
// call-site brevity.
func IsNotExist(err error) bool {
	return err != nil && errors.Is(err, os.ErrNotExist)
}

// RemoveAll removes path like [os.RemoveAll]. It never returns a fatal error
// for permission-class failures: EPERM/EACCES and wrapped
// "operation not permitted" errors are written to errOut (if non-nil) and
// discarded. A not-exist error is success and is not logged. Any other error
// is returned to the caller.
//
// Use this in batch removers so one protected directory does not abort the
// whole batch.
func RemoveAll(path string, errOut io.Writer) error {
	err := os.RemoveAll(path)
	switch {
	case err == nil:
		return nil
	case IsNotExist(err):
		return nil
	case IsPermission(err):
		if errOut != nil {
			fmt.Fprintf(errOut, "[warn] skipping %s: %v\n", path, err)
		}
		return nil
	default:
		return err
	}
}
