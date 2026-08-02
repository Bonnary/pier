package deploy

import (
	"context"
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

// putSFTPLink recreates a local symlink on the remote side, creating
// parent directories first. The target is preserved verbatim so
// relative links stay relative. Symlinked directories are not walked
// into (filepath.WalkDir never follows symlinks).
func putSFTPLink(sc *sftp.Client, localPath, remotePath string) error {
	target, err := os.Readlink(localPath)
	if err != nil {
		return fmt.Errorf("readlink %s: %w", localPath, err)
	}
	if err := sc.MkdirAll(filepath.ToSlash(filepath.Dir(remotePath))); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(remotePath), err)
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
