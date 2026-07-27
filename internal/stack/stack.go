package stack

import (
	"fmt"
	"os"
	"sync"

	"github.com/pcnerd/pier/internal/config"
)

type File struct {
	Path     string
	Contents []byte
	Mode     os.FileMode
}

type Files []File

type MergeWarning struct {
	Service    string
	Key        string
	SourceFile string
}

type Stack interface {
	Name() string
	Detect(projectPath string) bool
	DefaultConfig() config.StackConfig
	GenerateDevCompose(cfg config.Config) (Files, error)
	GenerateProdFiles(cfg config.Config) (Files, error)
	RequiredDirs() []string
}

var (
	regMu sync.RWMutex
	reg   = map[string]Stack{}
)

func Register(name string, s Stack) {
	if name == "" || s == nil {
		panic("stack: Register: empty name or nil stack")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := reg[name]; dup {
		panic("stack: Register: duplicate registration for " + name)
	}
	reg[name] = s
}

func Registry() map[string]Stack {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make(map[string]Stack, len(reg))
	for k, v := range reg {
		out[k] = v
	}
	return out
}

func ForName(name string) (Stack, error) {
	regMu.RLock()
	s, ok := reg[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("stack: %q not registered (known: laravel)", name)
	}
	return s, nil
}
