package portcheck

import (
	"context"
	"net"
	"testing"
)

func TestProbeFreePort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	taken, err := Probe(context.Background(), []int{port})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if _, ok := taken[port]; ok {
		t.Errorf("Probe reports port %d as taken, want free (listener was closed)", port)
	}
}

func TestProbeTakenPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	taken, err := Probe(context.Background(), []int{port})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if _, ok := taken[port]; !ok {
		t.Errorf("Probe does not report port %d as taken, want taken (listener is open)", port)
	}
}

func TestProbeMultiple(t *testing.T) {
	l1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen 1: %v", err)
	}
	defer l1.Close()
	p1 := l1.Addr().(*net.TCPAddr).Port

	l2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen 2: %v", err)
	}
	l2.Close()
	p2 := l2.Addr().(*net.TCPAddr).Port

	taken, err := Probe(context.Background(), []int{p1, p2})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if _, ok := taken[p1]; !ok {
		t.Errorf("Probe should report p1=%d as taken", p1)
	}
	if _, ok := taken[p2]; ok {
		t.Errorf("Probe should NOT report p2=%d as taken (listener was closed)", p2)
	}
}
