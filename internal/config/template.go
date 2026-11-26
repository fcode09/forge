package config

import _ "embed"

// Template is the example forge.yaml written by `forge init`.
//
//go:embed template.yaml
var Template []byte
