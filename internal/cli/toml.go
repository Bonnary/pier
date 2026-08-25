package cli

import (
	"bytes"
	"fmt"

	"github.com/Bonnary/pier/internal/config"
)

func tomlEncode(c config.Config) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "[project]\nname = %q\n\n", c.Project.Name)
	fmt.Fprintf(&b, "[stack]\ntype = %q\nphp = %q\nnode = %q\nqueue_workers = %d\nservices = [", c.Stack.Type, c.Stack.PHP, c.Stack.Node, c.QueueWorkers())
	for i, s := range c.Stack.Services {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", s)
	}
	b.WriteString("]\n")
	fmt.Fprintf(&b, "\n[dev]\n# bind = %q   # uncomment to expose dev ports to your LAN (default: %s)\n",
		"0.0.0.0", config.DefaultDevBind)
	for env, dc := range c.Deploy {
		fmt.Fprintf(&b, "\n[deploy.%s]\n", env)
		fmt.Fprintf(&b, "host = %q\n", dc.Host)
		fmt.Fprintf(&b, "user = %q\n", dc.User)
		fmt.Fprintf(&b, "path = %q\n", dc.Path)
		fmt.Fprintf(&b, "branch = %q\n", dc.Branch)
		if dc.Domain != "" {
			fmt.Fprintf(&b, "domain = %q\n", dc.Domain)
		}
		if len(dc.RedirectDomains) > 0 {
			fmt.Fprintf(&b, "redirect_domains = %s\n", tomlStringArray(dc.RedirectDomains))
		}
		fmt.Fprintf(&b, "builder = %q\n", dc.BuilderMode())
		if dc.QueueWorkers > 0 {
			fmt.Fprintf(&b, "queue_workers = %d\n", dc.QueueWorkers)
		}
		if dc.BuildHost != "" {
			fmt.Fprintf(&b, "build_host = %q\n", dc.BuildHost)
		}
		if dc.BuildUser != "" {
			fmt.Fprintf(&b, "build_user = %q\n", dc.BuildUser)
		}
		if dc.BuildPath != "" {
			fmt.Fprintf(&b, "build_path = %q\n", dc.BuildPath)
		}
		if dc.Services != nil {
			fmt.Fprintf(&b, "services = %s\n", tomlStringArray(dc.Services))
		}
		if len(dc.BeforeDeploy) == 0 {
			fmt.Fprintf(&b, "# before_deploy = [%q]  # uncomment: runs in the app container before the new release starts\n", "php artisan down")
		} else {
			fmt.Fprintf(&b, "before_deploy = %s\n", tomlStringArray(dc.BeforeDeploy))
		}
		if len(dc.AfterDeploy) == 0 {
			fmt.Fprintf(&b, "# after_deploy = [%q]  # uncomment: runs in the app container after the new release is up\n", "php artisan migrate --force")
		} else {
			fmt.Fprintf(&b, "after_deploy = %s\n", tomlStringArray(dc.AfterDeploy))
		}
	}
	return b.Bytes(), nil
}

// tomlStringArray renders items as a TOML array of quoted strings.
func tomlStringArray(items []string) string {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, s := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", s)
	}
	b.WriteByte(']')
	return b.String()
}
