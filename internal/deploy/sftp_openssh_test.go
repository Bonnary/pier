package deploy

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/crypto/ssh"
)

// SFTP v3 packet types (draft-ietf-secsh-filexfer-02) spoken by the
// OpenSSH-like emulation below.
const (
	sftpInit     = 1
	sftpVersion  = 2
	sftpOpen     = 3
	sftpClose    = 4
	sftpWrite    = 6
	sftpLstat    = 7
	sftpRemove   = 13
	sftpMkdir    = 14
	sftpStat     = 17
	sftpRename   = 18
	sftpStatus   = 101
	sftpHandle   = 102
	sftpAttrs    = 105
	sftpExtended = 200
)

// SFTP v3 status codes.
const (
	statusOK            = 0
	statusNoSuchFile    = 2
	statusFailure       = 4
	statusOpUnsupported = 8
)

// openSSHRenameSFTP serves a minimal SFTP v3 subsystem on a channel,
// mirroring the OpenSSH sftp-server rename semantics that broke
// WriteFile: the standard rename request fails with SSH_FX_FAILURE
// when the target already exists (OpenSSH implements the draft
// literally), and no protocol extensions are advertised, so extended
// requests answer SSH_FX_OP_UNSUPPORTED. All other operations
// (open/write/close/mkdir/remove/stat) run against the real
// filesystem at the client-supplied paths.
type openSSHRenameSFTP struct {
	ch ssh.Channel
}

// serveOpenSSHRenameSFTP runs the emulation until the channel closes.
func serveOpenSSHRenameSFTP(ch ssh.Channel) {
	s := &openSSHRenameSFTP{ch: ch}
	s.run()
}

func (s *openSSHRenameSFTP) run() {
	var hdr [4]byte
	if _, err := io.ReadFull(s.ch, hdr[:]); err != nil {
		return
	}
	pkt := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(s.ch, pkt); err != nil || len(pkt) < 5 || pkt[0] != sftpInit {
		return
	}
	if err := s.sendVersion(); err != nil {
		return
	}

	handles := map[string]*os.File{}
	nextHandle := 0
	for {
		if _, err := io.ReadFull(s.ch, hdr[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n == 0 || n > 1<<20 {
			return
		}
		pkt := make([]byte, n)
		if _, err := io.ReadFull(s.ch, pkt); err != nil {
			return
		}
		typ, id, body := pkt[0], binary.BigEndian.Uint32(pkt[1:5]), pkt[5:]
		switch typ {
		case sftpOpen:
			path, _, ok := readSFTPString(body)
			if !ok {
				s.status(id, statusFailure, "bad open")
				continue
			}
			f, err := os.OpenFile(s.local(path), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				s.status(id, statusNoSuchFile, err.Error())
				continue
			}
			nextHandle++
			h := strconv.Itoa(nextHandle)
			handles[h] = f
			if err := s.sendPacket(sftpHandle, id, appendSFTPString(nil, h)); err != nil {
				return
			}
		case sftpWrite:
			h, rest, ok := readSFTPString(body)
			if !ok || len(rest) < 8 {
				s.status(id, statusFailure, "bad write")
				continue
			}
			off := binary.BigEndian.Uint64(rest)
			data, _, ok := readSFTPString(rest[8:])
			if !ok {
				s.status(id, statusFailure, "bad write")
				continue
			}
			f, ok := handles[h]
			if !ok {
				s.status(id, statusFailure, "no such handle")
				continue
			}
			if _, err := f.WriteAt([]byte(data), int64(off)); err != nil {
				s.status(id, statusFailure, err.Error())
				continue
			}
			s.status(id, statusOK, "")
		case sftpClose:
			h, _, ok := readSFTPString(body)
			if !ok {
				s.status(id, statusFailure, "bad close")
				continue
			}
			if f, ok := handles[h]; ok {
				_ = f.Close()
				delete(handles, h)
			}
			s.status(id, statusOK, "")
		case sftpMkdir:
			path, _, ok := readSFTPString(body)
			if !ok {
				s.status(id, statusFailure, "bad mkdir")
				continue
			}
			if err := os.Mkdir(s.local(path), 0o755); err != nil {
				s.status(id, statusFailure, err.Error())
				continue
			}
			s.status(id, statusOK, "")
		case sftpRemove:
			path, _, ok := readSFTPString(body)
			if !ok {
				s.status(id, statusFailure, "bad remove")
				continue
			}
			if err := os.Remove(s.local(path)); err != nil {
				s.status(id, statusNoSuchFile, err.Error())
				continue
			}
			s.status(id, statusOK, "")
		case sftpStat:
			path, _, ok := readSFTPString(body)
			if !ok {
				s.status(id, statusFailure, "bad stat")
				continue
			}
			info, err := os.Stat(s.local(path))
			if err != nil {
				s.status(id, statusNoSuchFile, err.Error())
				continue
			}
			if err := s.attrs(id, info); err != nil {
				return
			}
		case sftpLstat:
			// OpenSSH's sftp-server supports LSTAT; report the link
			// itself so the client can refuse to write through a
			// symlinked parent (F4).
			path, _, ok := readSFTPString(body)
			if !ok {
				s.status(id, statusFailure, "bad lstat")
				continue
			}
			info, err := os.Lstat(s.local(path))
			if err != nil {
				s.status(id, statusNoSuchFile, err.Error())
				continue
			}
			if err := s.attrs(id, info); err != nil {
				return
			}
		case sftpRename:
			oldp, rest, ok := readSFTPString(body)
			if !ok {
				s.status(id, statusFailure, "bad rename")
				continue
			}
			newp, _, ok := readSFTPString(rest)
			if !ok {
				s.status(id, statusFailure, "bad rename")
				continue
			}
			// OpenSSH semantics: the target must not exist.
			if _, err := os.Lstat(s.local(newp)); err == nil {
				s.status(id, statusFailure, "target exists")
				continue
			}
			if err := os.Rename(s.local(oldp), s.local(newp)); err != nil {
				s.status(id, statusFailure, err.Error())
				continue
			}
			s.status(id, statusOK, "")
		case sftpExtended:
			// The emulation advertises no extensions, so the
			// client's posix-rename@openssh.com request is
			// answered SSH_FX_OP_UNSUPPORTED — exactly what a
			// server without that extension replies.
			s.status(id, statusOpUnsupported, "unsupported extension")
		default:
			s.status(id, statusOpUnsupported, "unsupported packet")
		}
	}
}

// local converts a client-supplied SFTP path (always absolute, since
// pkg/sftp sends the joined absolute paths) into a local path. The
// emulation serves the real filesystem, like OpenSSH sftp-server.
func (s *openSSHRenameSFTP) local(p string) string {
	return filepath.FromSlash(p)
}

// sendVersion replies to INIT with SSH_FXP_VERSION. Unlike request
// replies the version packet carries no request ID: type + version.
func (s *openSSHRenameSFTP) sendVersion() error {
	var pkt [5]byte
	pkt[0] = sftpVersion
	binary.BigEndian.PutUint32(pkt[1:], 3)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(pkt)))
	if _, err := s.ch.Write(hdr[:]); err != nil {
		return err
	}
	_, err := s.ch.Write(pkt[:])
	return err
}

// sendPacket writes a framed packet: uint32 length, type, id, payload.
func (s *openSSHRenameSFTP) sendPacket(typ byte, id uint32, payload []byte) error {
	b := make([]byte, 0, 1+4+len(payload))
	b = append(b, typ)
	b = binary.BigEndian.AppendUint32(b, id)
	b = append(b, payload...)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := s.ch.Write(hdr[:]); err != nil {
		return err
	}
	_, err := s.ch.Write(b)
	return err
}

// status sends a SSH_FXP_STATUS reply with a message and empty lang.
func (s *openSSHRenameSFTP) status(id uint32, code uint32, msg string) error {
	p := binary.BigEndian.AppendUint32(nil, code)
	p = appendSFTPString(p, msg)
	p = appendSFTPString(p, "")
	return s.sendPacket(sftpStatus, id, p)
}

// attrs sends a SSH_FXP_ATTRS reply carrying just the permissions
// attribute (mode + filetype bits), which is what pkg/sftp's client
// consults for IsDir and mode bits.
func (s *openSSHRenameSFTP) attrs(id uint32, info os.FileInfo) error {
	p := binary.BigEndian.AppendUint32(nil, 0x00000004) // SSH_FILEXFER_ATTR_PERMISSIONS
	mode := uint32(info.Mode().Perm())
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		mode |= 0xA000 // S_IFLNK
	case info.IsDir():
		mode |= 0x4000 // S_IFDIR
	default:
		mode |= 0x8000 // S_IFREG
	}
	p = binary.BigEndian.AppendUint32(p, mode)
	return s.sendPacket(sftpAttrs, id, p)
}

func appendSFTPString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

func readSFTPString(b []byte) (string, []byte, bool) {
	if len(b) < 4 {
		return "", nil, false
	}
	n := binary.BigEndian.Uint32(b)
	if uint32(len(b)-4) < n {
		return "", nil, false
	}
	return string(b[4 : 4+n]), b[4+n:], true
}

// TestWriteFileOverwritesExistingTarget drives WriteFile against an
// SFTP server with OpenSSH's rename semantics: a second write (e.g.
// the deploy commit phase overwriting .pier/state.json) must replace
// the existing file instead of failing with SSH_FX_FAILURE.
func TestWriteFileOverwritesExistingTarget(t *testing.T) {
	remote := t.TempDir()
	addr := startSSHServerWithSFTP(t, passwordOnlyServer(), func(ch ssh.Channel) {
		serveOpenSSHRenameSFTP(ch)
	})
	host, port := testAddr(t, addr)

	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  writeTestKeyPath(t),
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	path := filepath.ToSlash(filepath.Join(remote, ".pier", "state.json"))

	if err := c.WriteFile(ctx, path, []byte(`{"current":"one"}`)); err != nil {
		t.Fatalf("WriteFile (fresh): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(remote, ".pier", "state.json"))
	if err != nil || string(got) != `{"current":"one"}` {
		t.Fatalf("state.json after fresh write = %q (err %v)", got, err)
	}

	if err := c.WriteFile(ctx, path, []byte(`{"current":"two"}`)); err != nil {
		t.Fatalf("WriteFile (overwrite): %v", err)
	}
	got, err = os.ReadFile(filepath.Join(remote, ".pier", "state.json"))
	if err != nil || string(got) != `{"current":"two"}` {
		t.Fatalf("state.json after overwrite = %q (err %v)", got, err)
	}
	if _, err := os.Stat(filepath.Join(remote, ".pier", "state.json.tmp")); !os.IsNotExist(err) {
		t.Fatalf("state.json.tmp left behind: %v", err)
	}
}
