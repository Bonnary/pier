package deploy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakeSession records session-channel requests (exec, shell, pty-req,
// env, window-change) and answers them with canned output and a
// configurable exit status, mirroring how the deploy host responds to
// `pier shell <env>` / `pier exec <env>`.
type fakeSession struct {
	mu      sync.Mutex
	cmds    []string
	shell   bool
	ptyTerm string
	ptyRows uint32
	ptyCols uint32
	reject  bool
	output  []byte
	status  int
	// statusFn, when non-nil, returns the exit status for a session
	// command instead of the shared status field (per-command control
	// for pipeline tests).
	statusFn func(cmd string) int
}

func (f *fakeSession) addCmd(cmd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
}

func (f *fakeSession) recordPty(term string, cols, rows uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ptyTerm, f.ptyCols, f.ptyRows = term, cols, rows
}

// startFakeSession starts an in-process SSH server that answers
// session channels with f's canned behavior. Returns the listen
// address ("127.0.0.1:PORT").
func startFakeSession(t *testing.T, scfg *ssh.ServerConfig, f *fakeSession) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("host key signer: %v", err)
	}
	scfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeSessionConn(nc, scfg, f)
		}
	}()
	return ln.Addr().String()
}

func serveFakeSessionConn(nc net.Conn, scfg *ssh.ServerConfig, f *fakeSession) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, scfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		go serveFakeSessionChannel(ch, f)
	}
}

func serveFakeSessionChannel(ch ssh.NewChannel, f *fakeSession) {
	if ch.ChannelType() != "session" {
		_ = ch.Reject(ssh.UnknownChannelType, "unsupported channel type")
		return
	}
	channel, reqs, err := ch.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			term, cols, rows := parsePtyReq(req.Payload)
			f.recordPty(term, cols, rows)
			_ = req.Reply(true, nil)
		case "env", "window-change":
			_ = req.Reply(true, nil)
		case "exec":
			if f.reject {
				_ = req.Reply(false, nil)
				return
			}
			cmd := string(req.Payload[4:])
			f.addCmd(cmd)
			_ = req.Reply(true, nil)
			_, _ = channel.Write(f.output)
			st := f.status
			if f.statusFn != nil {
				st = f.statusFn(cmd)
			}
			finishFakeSession(channel, st)
			return
		case "shell":
			f.mu.Lock()
			f.shell = true
			f.mu.Unlock()
			_ = req.Reply(true, nil)
			_, _ = channel.Write(f.output)
			finishFakeSession(channel, f.status)
			return
		}
	}
}

// parsePtyReq decodes a pty-req payload: string term, uint32 cols,
// uint32 rows, uint32 pixel width, uint32 pixel height, string modes.
func parsePtyReq(payload []byte) (term string, cols, rows uint32) {
	termLen := int(binary.BigEndian.Uint32(payload[0:4]))
	term = string(payload[4 : 4+termLen])
	rest := payload[4+termLen:]
	return term, binary.BigEndian.Uint32(rest[0:4]), binary.BigEndian.Uint32(rest[4:8])
}

func finishFakeSession(ch ssh.Channel, status int) {
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(status)}))
	// Give the client's request-handler goroutine time to process the
	// exit-status before the channel EOF, or sess.Wait() races the
	// request delivery against the close and returns io.EOF.
	time.Sleep(10 * time.Millisecond)
	_ = ch.Close()
}

func TestRemoteExecCommand(t *testing.T) {
	// The want-strings below are the transport-encoded form: every arg
	// is wrapped by quoteShell, and the remote shell strips the quotes
	// at runtime (functionally identical to the unquoted form).
	got := remoteExecCommand("/srv/x", []string{"php", "artisan", "migrate"})
	want := "cd '/srv/x' && docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app 'php' 'artisan' 'migrate'"
	if got != want {
		t.Errorf("remoteExecCommand = %q, want %q", got, want)
	}
	got = remoteExecCommand("", []string{"php", "-v"})
	want = "docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app 'php' '-v'"
	if got != want {
		t.Errorf("remoteExecCommand (empty dir) = %q, want %q", got, want)
	}
	got = remoteExecCommand("/srv/x", []string{"php", "artisan", "migrate --force"})
	want = "cd '/srv/x' && docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app 'php' 'artisan' 'migrate --force'"
	if got != want {
		t.Errorf("remoteExecCommand (quoting) = %q, want %q", got, want)
	}
}

// dialTestClient dials cfg against the fake session server and
// fails the test on error.
func dialTestClient(t *testing.T, cfg SSHConfig) *Client {
	t.Helper()
	client, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return client
}

func TestRemoteExecStreamsAndPropagatesExit(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("migrated\n"), status: 3}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))

	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = wOut, wErr
	defer func() { os.Stdout, os.Stderr = oldStdout, oldStderr; _ = rOut.Close(); _ = rErr.Close() }()

	err := RemoteExec(context.Background(), SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath}, "/srv/x", []string{"php", "artisan", "migrate"})
	wOut.Close()
	wErr.Close()
	var out, errOut bytes.Buffer
	_, _ = io.Copy(&out, rOut)
	_, _ = io.Copy(&errOut, rErr)
	if err == nil {
		t.Fatal("RemoteExec = nil error, want exit-status error")
	}
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("error type = %T, want *ExitError", err)
	}
	if ee.Code != 3 {
		t.Errorf("exit code = %d, want 3", ee.Code)
	}
	if ee.RemoteHost != host {
		t.Errorf("RemoteHost = %q, want %q", ee.RemoteHost, host)
	}
	if got := out.String(); got != "migrated\n" {
		t.Errorf("stdout = %q, want %q", got, "migrated\n")
	}
	if len(fs.cmds) != 1 {
		t.Fatalf("recorded commands = %v, want 1", fs.cmds)
	}
	want := "cd '/srv/x' && docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app 'php' 'artisan' 'migrate'"
	if fs.cmds[0] != want {
		t.Errorf("command = %q, want %q", fs.cmds[0], want)
	}
}

func TestRemoteExecSuccess(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))
	if err := RemoteExec(context.Background(), SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath}, "/srv/x", []string{"php", "-v"}); err != nil {
		t.Errorf("RemoteExec = %v, want nil", err)
	}
}

func TestRemoteExecSessionRejected(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{reject: true}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))
	err := RemoteExec(context.Background(), SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath}, "/srv/x", []string{"php", "-v"})
	if err == nil {
		t.Fatal("RemoteExec = nil error, want session error")
	}
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("error type = %T, want *ExitError", err)
	}
	if ee.Kind != KindSSH {
		t.Errorf("kind = %v, want KindSSH", ee.Kind)
	}
	if ee.Code != ExitGeneral {
		t.Errorf("code = %d, want ExitGeneral", ee.Code)
	}
}

func TestRemoteShellRequiresTTY(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))
	err := RemoteShell(context.Background(), SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath}, "/srv/x")
	if !errors.Is(err, ErrShellNoTTY) {
		t.Fatalf("err = %v, want ErrShellNoTTY", err)
	}
	if len(fs.cmds) != 0 {
		t.Errorf("cmds = %v, want none (no shell without a TTY)", fs.cmds)
	}
}

func TestInteractiveShellPTYAndStreams(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("hi there\n"), status: 0}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))

	origIsT, origSize, origRaw := shellIsTerminal, shellGetSize, shellMakeRaw
	shellIsTerminal = func(int) bool { return true }
	shellGetSize = func(int) (int, int, error) { return 80, 24, nil }
	restored := false
	shellMakeRaw = func(int) (func() error, error) {
		return func() error { restored = true; return nil }, nil
	}
	defer func() { shellIsTerminal, shellGetSize, shellMakeRaw = origIsT, origSize, origRaw }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString("exit\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = w.Close()
	rOut, wOut, _ := os.Pipe()
	oldStdin, oldStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = r, wOut
	defer func() { os.Stdin, os.Stdout = oldStdin, oldStdout; _ = r.Close(); _ = rOut.Close() }()

	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()
	if err := client.InteractiveShell(context.Background(), "/srv/x"); err != nil {
		t.Fatalf("InteractiveShell: %v", err)
	}
	wOut.Close()
	var out bytes.Buffer
	_, _ = io.Copy(&out, rOut)
	if !restored {
		t.Error("terminal restore func not called")
	}
	if fs.ptyTerm != "xterm-256color" {
		t.Errorf("pty term = %q, want xterm-256color", fs.ptyTerm)
	}
	if fs.ptyCols != 80 || fs.ptyRows != 24 {
		t.Errorf("pty size = %dx%d, want 80x24", fs.ptyCols, fs.ptyRows)
	}
	if got := out.String(); got != "hi there\n" {
		t.Errorf("stdout = %q, want %q", got, "hi there\n")
	}
	if len(fs.cmds) != 1 {
		t.Fatalf("recorded commands = %v, want 1", fs.cmds)
	}
	// The want-string is the transport-encoded form: the dir is wrapped
	// by quoteShell, and the remote shell strips the quotes at runtime
	// (functionally identical to the unquoted form, matching Task 1's
	// TestRemoteExecCommand).
	want := "cd '/srv/x' && docker compose --env-file .env.production -f docker-compose.prod.yml exec app bash"
	if fs.cmds[0] != want {
		t.Errorf("command = %q, want %q", fs.cmds[0], want)
	}
}

func TestInteractiveShellExitStatus(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{status: 42}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))

	origIsT, origSize, origRaw := shellIsTerminal, shellGetSize, shellMakeRaw
	shellIsTerminal = func(int) bool { return true }
	shellGetSize = func(int) (int, int, error) { return 80, 24, nil }
	shellMakeRaw = func(int) (func() error, error) {
		return func() error { return nil }, nil
	}
	defer func() { shellIsTerminal, shellGetSize, shellMakeRaw = origIsT, origSize, origRaw }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	_, _ = w.WriteString("exit\n")
	_ = w.Close()
	rOut, wOut, _ := os.Pipe()
	oldStdin, oldStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = r, wOut
	defer func() { os.Stdin, os.Stdout = oldStdin, oldStdout; _ = r.Close(); _ = rOut.Close() }()

	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()
	err = client.InteractiveShell(context.Background(), "/srv/x")
	wOut.Close()
	_, _ = io.Copy(io.Discard, rOut)
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want *ExitError", err)
	}
	if ee.Code != 42 {
		t.Errorf("exit code = %d, want 42", ee.Code)
	}
}
