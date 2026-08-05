package cli

import (
	"bytes"
	"fmt"

	"github.com/Bonnary/pier/internal/config"
)

func tomlEncode(c config.Config) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "[project]\nname = %q\ndomain = %q\n\n", c.Project.Name, c.Project.Domain)
	fmt.Fprintf(&b, "[stack]\ntype = %q\nphp = %q\nnode = %q\nservices = [", c.Stack.Type, c.Stack.PHP, c.Stack.Node)
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
