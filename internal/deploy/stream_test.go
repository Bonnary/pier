package deploy

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestStreamInFeedsStdinAndStreamsStderr(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("Loaded image: myapp:abc1234\n"), status: 0, captureStdin: true}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	var lines []string
	tar := "TARBYTES\x00BINARY"
	if err := client.StreamIn(context.Background(), "docker load", strings.NewReader(tar), func(l string) {
		lines = append(lines, l)
	}); err != nil {
		t.Fatalf("StreamIn: %v", err)
	}
	if len(fs.cmds) != 1 || fs.cmds[0] != "docker load" {
		t.Errorf("cmds = %q, want [docker load]", fs.cmds)
	}
	if got := string(fs.stdin()); got != tar {
		t.Errorf("stdin received = %q, want %q", got, tar)
	}
	if len(lines) == 0 {
		t.Error("onLine never called; stderr should have streamed docker load output")
	}
}

func TestStreamInPropagatesExitError(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("open /var/run/docker.sock: permission denied\n"), status: 1}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	err := client.StreamIn(context.Background(), "docker load", strings.NewReader("data"), nil)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("StreamIn() = %v, want failure carrying the stderr tail", err)
	}
}

func TestStreamOutPipesBinaryStdout(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("TAR\x00BINARY\x01data"), status: 0}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	var out bytes.Buffer
	if err := client.StreamOut(context.Background(), "docker save myapp:abc1234", &out, nil); err != nil {
		t.Fatalf("StreamOut: %v", err)
	}
	if !bytes.Equal(out.Bytes(), []byte("TAR\x00BINARY\x01data")) {
		t.Errorf("StreamOut stdout = %q, want binary tar preserved byte-for-byte", out.Bytes())
	}
}
