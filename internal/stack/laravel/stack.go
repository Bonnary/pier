package laravel

import (
	"github.com/pcnerd/pier/internal/config"
	"github.com/pcnerd/pier/internal/stack"
)

type Stack struct{}

func New() *Stack { return &Stack{} }

func init() {
	stack.Register("laravel", New())
}

func (s *Stack) Name() string { return "laravel" }
func (s *Stack) Detect(path string) bool { return false }
func (s *Stack) DefaultConfig() config.StackConfig {
	return config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"}
}
func (s *Stack) GenerateDevCompose(cfg config.Config) (stack.Files, error) { return nil, nil }
func (s *Stack) GenerateProdFiles(cfg config.Config) (stack.Files, error) { return nil, nil }
func (s *Stack) RequiredDirs() []string { return nil }
