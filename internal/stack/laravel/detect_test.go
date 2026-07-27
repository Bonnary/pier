package laravel

import (
	"path/filepath"
	"testing"
)

func TestDetectLaravel(t *testing.T) {
	if !detect(filepath.Join("testdata", "laravel")) {
		t.Error("detect(laravel) = false, want true")
	}
}

func TestDetectNoFramework(t *testing.T) {
	if detect(filepath.Join("testdata", "composer-no-framework")) {
		t.Error("detect(composer-no-framework) = true, want false")
	}
}

func TestDetectNoArtisan(t *testing.T) {
	if detect(filepath.Join("testdata", "laravel-no-artisan")) {
		t.Error("detect(laravel-no-artisan) = true, want false (no artisan file)")
	}
}

func TestDetectEmpty(t *testing.T) {
	if detect(filepath.Join("testdata", "empty-project")) {
		t.Error("detect(empty-project) = true, want false")
	}
}

func TestDetectMissing(t *testing.T) {
	if detect(filepath.Join("testdata", "does-not-exist")) {
		t.Error("detect(missing) = true, want false")
	}
}
