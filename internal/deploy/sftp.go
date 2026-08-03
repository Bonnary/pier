package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pkg/sftp"
)

// SyncDir copies the local directory tree to remote over SFTP,
// reusing the already-open SSH connection. Paths matched by excludes
// (see pathExcluded) are skipped; excluded directories are pruned.
// Remote parent directories are created on demand and file modes and
// modification times are preserved. ctx is honored between files; a
// single large file copy is not interruptible mid-stream.
func (c *Client) SyncDir(ctx context.Context, local, remote string, excludes []string) error {
	sc, err := sftp.NewClient(c.conn)
	if err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
	defer sc.Close()
	return filepath.WalkDir(local, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(local, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if pathExcluded(rel, excludes) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return putSFTPFile(sc, path, filepath.ToSlash(filepath.Join(remote, rel)), info)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return putSFTPLink(sc, path, filepath.ToSlash(filepath.Join(remote, rel)))
		}
		return nil
	})
}

// ReadFile reads a remote file over SFTP on the already-open SSH
// connection. Returns (nil, nil) when the path does not exist.
func (c *Client) ReadFile(ctx context.Context, remotePath string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sc, err := sftp.NewClient(c.conn)
	if err != nil {
		return nil, fmt.Errorf("sftp: %w", err)
	}
	defer sc.Close()
	f, err := sc.Open(remotePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sftp open %s: %w", remotePath, err)
	}
	defer f.Close()
	return io.ReadAll(f)
}

// WriteFile writes bytes to a remote file over SFTP, creating parent
// directories. The content lands in a sibling .tmp file first and is
// renamed into place, so a reader never observes a partial write.
func (c *Client) WriteFile(ctx context.Context, remotePath string, b []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sc, err := sftp.NewClient(c.conn)
	if err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
	defer sc.Close()
	if err := sc.MkdirAll(filepath.ToSlash(filepath.Dir(remotePath))); err != nil {
		return fmt.Errorf("sftp mkdir %s: %w", filepath.Dir(remotePath), err)
	}
	tmp := remotePath + ".tmp"
	f, err := sc.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("sftp open %s: %w", tmp, err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return fmt.Errorf("sftp write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("sftp close %s: %w", tmp, err)
	}
	if err := sc.Rename(tmp, remotePath); err != nil {
		// OpenSSH's sftp-server implements the draft literally: the
		// standard rename request fails when the target already
		// exists, so overwriting a file (e.g. a second deploy writing
		// .pier/state.json) errors with SSH_FX_FAILURE. Prefer the
		// posix-rename@openssh.com extension, which replaces the
		// target atomically; servers without the extension
		// (SSH_FX_OP_UNSUPPORTED) get a remove-then-rename fallback.
		if err := sc.PosixRename(tmp, remotePath); err != nil {
			var se *sftp.StatusError
			if !errors.As(err, &se) || se.Code != uint32(sftp.ErrSSHFxOpUnsupported) {
				return fmt.Errorf("sftp rename %s: %w", remotePath, err)
			}
			if err := sc.Remove(remotePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("sftp remove %s: %w", remotePath, err)
			}
			if err := sc.Rename(tmp, remotePath); err != nil {
				return fmt.Errorf("sftp rename %s: %w", remotePath, err)
			}
		}
	}
	return nil
}

// putSFTPLink recreates a local symlink on the remote side, creating
// parent directories first. The target is preserved verbatim so
// relative links stay relative. It is idempotent: an existing remote
// symlink with the same target is left untouched, and a stale regular
// file at the link path is replaced. Symlinked directories are not
// walked into (filepath.WalkDir never follows symlinks).
func putSFTPLink(sc *sftp.Client, localPath, remotePath string) error {
	target, err := os.Readlink(localPath)
	if err != nil {
		return fmt.Errorf("readlink %s: %w", localPath, err)
	}
	if err := sc.MkdirAll(filepath.ToSlash(filepath.Dir(remotePath))); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(remotePath), err)
	}
	existing, err := sc.ReadLink(remotePath)
	switch {
	case err == nil && existing == target:
		return nil
	case err == nil:
		if err := sc.Remove(remotePath); err != nil {
			return fmt.Errorf("remove stale symlink %s: %w", remotePath, err)
		}
	case errors.Is(err, os.ErrNotExist):
		// Remote path is free; create the link below.
	default:
		// The path exists as a non-symlink (e.g. a stale regular file
		// or directory from a layout before this fix). Distinguish so
		// a directory conflict surfaces as a clear error rather than
		// being recursively deleted.
		info, serr := sc.Stat(remotePath)
		if serr != nil {
			return fmt.Errorf("stat %s: %w", remotePath, serr)
		}
		if info.IsDir() {
			return fmt.Errorf("symlink conflict: %s exists as a directory; remove it manually", remotePath)
		}
		if err := sc.Remove(remotePath); err != nil {
			return fmt.Errorf("remove stale file %s: %w", remotePath, err)
		}
	}
	if err := sc.Symlink(target, remotePath); err != nil {
		return fmt.Errorf("symlink %s: %w", remotePath, err)
	}
	return nil
}

// putSFTPFile writes one local file to the remote path, creating
// parent directories, preserving mode and mtime.
func putSFTPFile(sc *sftp.Client, localPath, remotePath string, info fs.FileInfo) error {
	if err := sc.MkdirAll(filepath.ToSlash(filepath.Dir(remotePath))); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(remotePath), err)
	}
	rf, err := sc.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("open remote %s: %w", remotePath, err)
	}
	defer rf.Close()
	lf, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local %s: %w", localPath, err)
	}
	defer lf.Close()
	if _, err := io.Copy(rf, lf); err != nil {
		return fmt.Errorf("copy %s: %w", remotePath, err)
	}
	if err := rf.Chmod(info.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod %s: %w", remotePath, err)
	}
	if err := sc.Chtimes(remotePath, info.ModTime(), info.ModTime()); err != nil {
		return fmt.Errorf("chtimes %s: %w", remotePath, err)
	}
	return nil
}
