package deploy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// imageSaver produces a docker save tar stream for image, either from
// the local docker daemon (localSaver) or over an SSH connection to a
// build server (remoteSaver).
type imageSaver interface {
	Save(ctx context.Context, image string, sink io.Writer, onLine func(string)) error
}

// localSaver saves images from the local docker daemon. dir is the
// working directory for the docker invocation; save defaults to the
// package saveLocal when nil.
type localSaver struct {
	dir  string
	save func(ctx context.Context, dir, image string, sink io.Writer, onLine func(string)) error
}

func (s localSaver) Save(ctx context.Context, image string, sink io.Writer, onLine func(string)) error {
	save := s.save
	if save == nil {
		save = saveLocal
	}
	return save(ctx, s.dir, image, sink, onLine)
}

// remoteSaver saves images over an SSH connection to a build server.
type remoteSaver struct {
	c *Client
}

func (s remoteSaver) Save(ctx context.Context, image string, sink io.Writer, onLine func(string)) error {
	return s.c.StreamOut(ctx, "docker save "+image, sink, onLine)
}

// saveLocal runs `docker save <image>` locally in dir, streaming the
// tar into sink. It is a var so tests can script the docker side.
var saveLocal = func(ctx context.Context, dir, image string, sink io.Writer, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, "docker", "save", image)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	var stderrErr error
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if onLine != nil {
				onLine(sc.Text())
			}
		}
		stderrErr = sc.Err()
	}()
	_, copyErr := io.Copy(sink, stdout)
	<-done
	if err := cmd.Wait(); err != nil {
		return err
	}
	if copyErr != nil {
		return copyErr
	}
	return stderrErr
}

// countingReader counts the bytes read through it. The count is
// mutex-guarded: the SSH session's stdin copy goroutine may still be
// draining its final chunk when TransferImage reads the total (the
// pipe write→read handshake guarantees all data bytes are counted by
// the time the saver's errCh send completes, but the trailing reads
// must not race the final count read).
type countingReader struct {
	mu sync.Mutex
	r  io.Reader
	n  int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.mu.Lock()
	c.n += int64(n)
	c.mu.Unlock()
	return n, err
}

func (c *countingReader) count() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// TransferImage streams image from saver into dst's docker daemon via
// `docker load` and returns the number of bytes streamed. Both sides
// run concurrently: the saver's output feeds an io.Pipe whose read
// side is the docker load session's stdin. A failure on either side
// aborts the transfer; the saver's error takes precedence so the
// reported cause matches where the image came from.
func TransferImage(ctx context.Context, saver imageSaver, dst *Client, image string, onLine func(string)) (int64, error) {
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := saver.Save(ctx, image, pw, onLine)
		_ = pw.CloseWithError(err)
		errCh <- err
	}()
	counter := &countingReader{r: pr}
	loadErr := dst.StreamIn(ctx, "docker load", counter, onLine)
	// If the remote docker load died without draining stdin, the SSH
	// session stops reading the pipe and the saver blocks forever in
	// pw.Write. Close the read side with the load error so the blocked
	// write fails and errCh fires instead of hanging the deploy.
	_ = pr.CloseWithError(loadErr)
	saveErr := <-errCh
	if saveErr != nil {
		return counter.count(), fmt.Errorf("docker save %s: %w", image, saveErr)
	}
	if loadErr != nil {
		return counter.count(), fmt.Errorf("docker load: %w", loadErr)
	}
	return counter.count(), nil
}
