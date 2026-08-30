package laravel

import (
	"strings"
	"testing"
)

func TestServicesAllRegistered(t *testing.T) {
	for _, name := range []string{"mysql", "postgres", "redis", "meilisearch", "mailpit", "queue", "scheduler", "s3"} {
		if _, ok := services()[name]; !ok {
			t.Errorf("service %q not registered", name)
		}
	}
}

func TestServicesExcludesFabricated(t *testing.T) {
	for _, name := range []string{"log-viewer", "dumps", "reverb"} {
		if _, ok := services()[name]; ok {
			t.Errorf("service %q is registered, but must be opt-in via [dev.services] (the hardcoded image does not exist on Docker Hub)", name)
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

func TestLookupRejectsFabricated(t *testing.T) {
	for _, name := range []string{"log-viewer", "dumps", "reverb"} {
		if _, ok := lookup(name); ok {
			t.Errorf("lookup(%q) = true, want false (no built-in sidecar; opt-in via [dev.services])", name)
		}
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

func TestS3RunsMiniToCreateBucket(t *testing.T) {
	s3 := services()["s3"]
	cmd := strings.Join(s3.Command, " ")
	if !strings.Contains(cmd, "weed mini") {
		t.Errorf("s3 command = %q, want it to run `weed mini` (the all-in-one mode that starts the S3 gateway on 8333 — `weed server` skips S3 unless -s3 is passed — and pre-creates the bucket)", cmd)
	}
	if !strings.Contains(cmd, "-dir=/data") {
		t.Errorf("s3 command = %q, want -dir=/data so data lands in the mounted s3_data:/data volume", cmd)
	}
	if got := s3.Env["S3_BUCKET"]; got != "${AWS_BUCKET}" {
		t.Errorf("s3 env S3_BUCKET = %q, want ${AWS_BUCKET} so the pre-created bucket stays in sync with the app's AWS_BUCKET", got)
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

func TestSupportedServicesExcludesFabricated(t *testing.T) {
	got := SupportedServices()
	for _, name := range []string{"log-viewer", "dumps", "reverb"} {
		for _, s := range got {
			if s == name {
				t.Errorf("SupportedServices contains %q, want it absent (no built-in sidecar)", name)
			}
		}
	}
}

func TestMeilisearchHealthcheckUsesIPv4Loopback(t *testing.T) {
	m := services()["meilisearch"]
	if m.Healthcheck == nil {
		t.Fatal("meilisearch has no healthcheck")
	}
	joined := strings.Join(m.Healthcheck.Test, " ")
	if strings.Contains(joined, "localhost") {
		t.Errorf("meilisearch healthcheck uses %q; localhost resolves to ::1 in alpine images and meilisearch only binds 0.0.0.0:7700 (IPv4), so the check fails with 'Connection refused'", joined)
	}
	if !strings.Contains(joined, "127.0.0.1") {
		t.Errorf("meilisearch healthcheck = %q, want it to use 127.0.0.1", joined)
	}
}

func TestS3HealthcheckIsValid(t *testing.T) {
	s3 := services()["s3"]
	if s3.Healthcheck == nil {
		t.Fatal("s3 has no healthcheck")
	}
	if s3.Healthcheck.Test[0] != "CMD-SHELL" {
		t.Fatalf("s3 healthcheck test[0] = %q, want CMD-SHELL", s3.Healthcheck.Test[0])
	}
	body := strings.Join(s3.Healthcheck.Test[1:], " ")
	if strings.Contains(body, "grep -q s3") {
		t.Errorf("s3 healthcheck greps server output for the literal 's3', but SeaweedFS replies 'HTTP/1.1 400 Bad Request' to non-HTTP payloads which never contains 's3', so the check always fails:\n  %s", body)
	}
	if !strings.Contains(body, "nc -z") {
		t.Errorf("s3 healthcheck = %q, want a port-open test (nc -z) that exits 0 when port 8333 is reachable", body)
	}
}

func TestServicesPortKeysMatchPorts(t *testing.T) {
	for name, svc := range services() {
		if len(svc.PortKeys) != len(svc.Ports) {
			t.Errorf("%s: len(PortKeys)=%d != len(Ports)=%d (every container port needs a matching key)", name, len(svc.PortKeys), len(svc.Ports))
		}
	}
}

func TestEnvWithWorkers(t *testing.T) {
	base := map[string]string{"CONTAINER_ROLE": "queue"}
	got := envWithWorkers(base, 3)
	if v := got["SUPERVISOR_NUMPROCS"]; v != "3" {
		t.Errorf("SUPERVISOR_NUMPROCS = %q, want 3", v)
	}
	if v := got["CONTAINER_ROLE"]; v != "queue" {
		t.Errorf("CONTAINER_ROLE = %q, want queue (copy must keep base keys)", v)
	}
	if _, ok := base["SUPERVISOR_NUMPROCS"]; ok {
		t.Error("envWithWorkers mutated base (the registry map is shared across renders; mutating it would leak a stale count)")
	}
}

func TestQueueSchedulerSetSupervisorCommand(t *testing.T) {
	for _, name := range []string{"queue", "scheduler"} {
		s := services()[name]
		got, ok := s.Env["SUPERVISOR_PHP_COMMAND"]
		if !ok {
			t.Errorf("%s env missing SUPERVISOR_PHP_COMMAND; supervisord will run the Dockerfile default ('artisan serve') instead of the role-specific command, so the healthcheck never sees the expected process", name)
			continue
		}
		switch name {
		case "queue":
			if !strings.Contains(got, "queue:work") {
				t.Errorf("queue SUPERVISOR_PHP_COMMAND = %q, want it to invoke 'artisan queue:work'", got)
			}
		case "scheduler":
			if !strings.Contains(got, "schedule:work") {
				t.Errorf("scheduler SUPERVISOR_PHP_COMMAND = %q, want it to invoke 'artisan schedule:work'", got)
			}
		}
	}
}
