package deploy

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Bonnary/pier/internal/config"
)

// scriptedSaver is an imageSaver that writes canned bytes to sink and
// optionally fails.
type scriptedSaver struct {
	data string
	err  error
}

func (s scriptedSaver) Save(ctx context.Context, image string, sink io.Writer, onLine func(string)) error {
	if s.err != nil {
		return s.err
	}
	_, err := io.WriteString(sink, s.data)
	return err
}

func TestTransferImageStreamsSaveIntoLoad(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0, captureStdin: true}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	saver := scriptedSaver{data: "TARBYTES"}
	var lines []string
	n, err := TransferImage(context.Background(), saver, client, "myapp:abc1234", func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatalf("TransferImage: %v", err)
	}
	if n != int64(len("TARBYTES")) {
		t.Errorf("TransferImage bytes = %d, want %d", n, len("TARBYTES"))
	}
	if got := string(fs.stdin()); got != "TARBYTES" {
		t.Errorf("docker load stdin = %q, want the save stream", got)
	}
	if len(fs.cmds) != 1 || fs.cmds[0] != "docker load" {
		t.Errorf("cmds = %q, want [docker load]", fs.cmds)
	}
}

func TestTransferImageSaveFailureAborts(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	_, err := TransferImage(context.Background(), scriptedSaver{err: errors.New("save boom")}, client, "myapp:abc1234", nil)
	if err == nil || !strings.Contains(err.Error(), "save boom") {
		t.Errorf("TransferImage() = %v, want the save error propagated", err)
	}
}

// TestTransferImageLoadExitEarlyDoesNotHang asserts that a remote
// `docker load` which exits without draining stdin (daemon died,
// permission error) fails the transfer instead of hanging forever.
// captureStdin is OFF so the fake server never reads the channel,
// mirroring the early exit; the payload exceeds the SSH transport
// window so the saver is still blocked writing into the pipe when the
// load session dies. The old code never closed the pipe's read side,
// so the blocked save write could never unblock and `<-errCh` hung
// the deploy.
func TestTransferImageLoadExitEarlyDoesNotHang(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("docker load: open /var/lib/docker/tmp: permission denied\n"), status: 1}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	saver := scriptedSaver{data: strings.Repeat("x", 4<<20)}
	done := make(chan error, 1)
	go func() {
		_, err := TransferImage(context.Background(), saver, client, "myapp:abc1234", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "Process exited with status 1") {
			t.Errorf("TransferImage() = %v, want the docker load exit error propagated", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("TransferImage hung: remote docker load exited without draining stdin and the saver never unblocked")
	}
}

func TestPipelineTransferRetagsCurrentOnHost(t *testing.T) {
	t.Chdir(t.TempDir())
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0, captureStdin: true}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	logger := &recordingLogger{}
	p := &Pipeline{
		Config:    &config.Config{Project: config.ProjectConfig{Name: "myapp"}},
		DeployEnv: config.DeployConfig{Builder: "local_machine"},
		Logger:    logger, SSH: SSHConfig{Host: host, User: "deploy", Port: port, KeyPath: keyPath},
		tag: "abc1234",
	}
	p.saveLocal = func(ctx context.Context, dir, image string, sink io.Writer, onLine func(string)) error {
		_, err := io.WriteString(sink, "TAR")
		return err
	}
	if err := p.transfer(context.Background(), client); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if got := string(fs.stdin()); got != "TAR" {
		t.Errorf("docker load stdin = %q, want the save stream", got)
	}
	if len(fs.cmds) != 2 || fs.cmds[1] != "docker tag myapp:abc1234 myapp:current" {
		t.Errorf("cmds = %q, want [docker load docker tag myapp:abc1234 myapp:current]", fs.cmds)
	}
}
