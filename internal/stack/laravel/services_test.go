package laravel

import "testing"

func TestServicesAllRegistered(t *testing.T) {
	for _, name := range []string{"mysql", "postgres", "redis", "meilisearch", "mailpit", "reverb", "queue", "scheduler", "log-viewer", "dumps", "s3"} {
		if _, ok := services()[name]; !ok {
			t.Errorf("service %q not registered", name)
		}
	}
}

func TestMailpitDevOnly(t *testing.T) {
	m := services()["mailpit"]
	if m.DevOnly != "true" {
		t.Errorf("mailpit DevOnly = %q, want true", m.DevOnly)
	}
}

func TestLookupCaseInsensitive(t *testing.T) {
	for _, n := range []string{"Redis", "REDIS", "redis"} {
		if _, ok := lookup(n); !ok {
			t.Errorf("lookup(%q) = false, want true", n)
		}
	}
}

func TestLookupUnknown(t *testing.T) {
	if _, ok := lookup("oracle"); ok {
		t.Error(`lookup("oracle") = true, want false`)
	}
}

func TestS3HasPorts(t *testing.T) {
	s3 := services()["s3"]
	want := map[string]bool{"8333": false, "8888": false, "9333": false}
	for _, p := range s3.Ports {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for p, found := range want {
		if !found {
			t.Errorf("s3 port %s not in Ports=%v", p, s3.Ports)
		}
	}
}

func TestSupportedServices(t *testing.T) {
	got := SupportedServices()
	if len(got) != len(services()) {
		t.Errorf("len = %d, want %d", len(got), len(services()))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Errorf("not sorted: %v", got)
		}
	}
	for _, name := range got {
		if _, ok := services()[name]; !ok {
			t.Errorf("SupportedServices contains %q which is not in services()", name)
		}
	}
}
