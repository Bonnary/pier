// Package portcheck probes host TCP ports to detect collisions before
// `docker compose up`. Used by `pier dev` to fail fast with an
// actionable error pointing the user at [dev.ports] in pier.toml.
package portcheck

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Probe tries a 1-second TCP connect to each port on 127.0.0.1 and
// returns a map of taken ports to a "pid (process name)" string.
// Ports that connect successfully (something is already listening) are
// reported as taken. The process name is best-effort: populated on
// Linux (from /proc), empty on macOS and Windows.
func Probe(ctx context.Context, ports []int) (map[int]string, error) {
	taken := map[int]string{}
	for _, port := range ports {
		dctx, cancel := context.WithTimeout(ctx, 1*time.Second)
		var d net.Dialer
		conn, err := d.DialContext(dctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
		cancel()
		if err != nil {
			continue
		}
		conn.Close()
		who := attributeListener(port)
		taken[port] = who
	}
	return taken, nil
}

// attributeListener returns a "pid (process name)" string for whatever
// process is listening on the given port, or "" if it can't be
// determined. Linux uses /proc/net/tcp to find the listening inode,
// then walks /proc/<pid>/fd to find the owner, then reads /proc/<pid>/comm.
// Other platforms return "".
func attributeListener(port int) string {
	if runtime.GOOS == "linux" {
		return linuxAttributeListener(port)
	}
	return ""
}

func linuxAttributeListener(port int) string {
	hexPort := fmt.Sprintf("%04X", port)
	inode, ok := findListeningInode(hexPort)
	if !ok {
		return ""
	}
	pid, ok := findPIDByInode(inode)
	if !ok {
		return ""
	}
	name := readComm(pid)
	if name == "" {
		return strconv.Itoa(pid)
	}
	return fmt.Sprintf("%d (%s)", pid, name)
}

func findListeningInode(hexPort string) (string, bool) {
	data, err := readFile("/proc/net/tcp")
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(data, "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		if fields[3] != "0A" {
			continue
		}
		addr := fields[1]
		parts := strings.Split(addr, ":")
		if len(parts) != 2 {
			continue
		}
		if parts[1] == hexPort {
			return fields[9], true
		}
	}
	return "", false
}

func findPIDByInode(targetInode string) (int, bool) {
	entries, err := readDir("/proc")
	if err != nil {
		return 0, false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fds, err := readDir("/proc/" + e.Name() + "/fd")
		if err != nil {
			continue
		}
		for _, f := range fds {
			link, err := readlink("/proc/" + e.Name() + "/fd/" + f.Name())
			if err != nil {
				continue
			}
			if link == "socket:["+targetInode+"]" {
				return pid, true
			}
		}
	}
	return 0, false
}

func readComm(pid int) string {
	data, err := readFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(data)
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func readDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

func readlink(path string) (string, error) {
	return os.Readlink(path)
}
