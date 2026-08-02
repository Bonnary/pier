package deploy

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// startSSHServer starts an in-process SSH server on 127.0.0.1 that
// authenticates via scfg and serves the sftp subsystem on "session"
// channels. It returns the listen address ("127.0.0.1:PORT").
//
// A generated ed25519 host key is added to scfg: ssh.NewServerConn
// refuses to run without one (the brief's server configs did not set
// one, so every handshake failed with "ssh: server has no host keys").
// Tests that need a specific host key can add their own via
// scfg.AddHostKey before calling this helper.
func startSSHServer(t *testing.T, scfg *ssh.ServerConfig) string {
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
			go serveTestSSHConn(nc, scfg)
		}
	}()
	return ln.Addr().String()
}

func serveTestSSHConn(nc net.Conn, scfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, scfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		go serveTestSSHChannel(ch)
	}
}

func serveTestSSHChannel(ch ssh.NewChannel) {
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
		case "subsystem":
			if string(req.Payload[4:]) == "sftp" {
				_ = req.Reply(true, nil)
				srv, err := sftp.NewServer(channel)
				if err != nil {
					return
				}
				_ = srv.Serve()
				return
			}
			_ = req.Reply(false, nil)
		case "exec", "shell", "env", "pty-req":
			_ = req.Reply(false, nil)
		}
	}
}

// testAddr splits addr into host and port for SSHConfig.
func testAddr(t *testing.T, addr string) (host string, port int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	port, err = strconv.Atoi(p)
	if err != nil {
		t.Fatalf("port %q: %v", p, err)
	}
	return h, port
}

// writeTestKey generates an ed25519 key pair at runtime, writes the
// private key as a PEM PKCS8 file (readable by ssh.ParsePrivateKey),
// and returns the file path and the public key. Nothing is committed.
func writeTestKey(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("NewSignerFromKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("WriteFile key: %v", err)
	}
	return path, signer.PublicKey()
}

// writeTestKeyPath is writeTestKey's convenience form for callers
// that only need the private key path.
func writeTestKeyPath(t *testing.T) string {
	t.Helper()
	path, _ := writeTestKey(t)
	return path
}

// passwordOnlyServer returns a ServerConfig accepting password
// "secret" for user "deploy".
func passwordOnlyServer() *ssh.ServerConfig {
	sc := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == "deploy" && string(pw) == "secret" {
				return nil, nil
			}
			return nil, errAuthFailed
		},
	}
	sc.SetDefaults()
	return sc
}

// keyOnlyServer returns a ServerConfig accepting only the given
// public key for user "deploy".
func keyOnlyServer(pub ssh.PublicKey) *ssh.ServerConfig {
	sc := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == "deploy" && string(key.Marshal()) == string(pub.Marshal()) {
				return nil, nil
			}
			return nil, errAuthFailed
		},
	}
	sc.SetDefaults()
	return sc
}

var errAuthFailed = &authError{}

type authError struct{}

func (*authError) Error() string { return "authentication failed" }
