package laravel

import "gopkg.in/yaml.v3"

func yamlMarshal(v interface{}) ([]byte, error) { return yaml.Marshal(v) }
