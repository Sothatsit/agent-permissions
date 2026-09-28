package rules

import (
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
)

// podmanGlobalOptions holds the options podman 4.9.4 defines before its
// subcommand in cmd/podman/root.go, hidden ones included. The hidden ones hold
// docker-compatible spellings an agent used to docker may type (-H, -D,
// --context, --config) and internal knobs (--db-backend, --max-workers).
// Some point podman at other programs or files (--runtime, --conmon,
// --hooks-dir, --module, --network-cmd-path, --default-mounts-file), but the
// shipped podman:* already allows running any container, so stripping them
// hides nothing the shipped presets would deny. --help and --version stay in
// place to keep matching their own patterns. An option added after 4.9.4 stays
// in place and leaves the command matching only podman:*.
var podmanGlobalOptions = &model.GlobalOptions{
	Arguments: map[string]int{
		"--remote": 0, "-r": 0,
		"--debug": 0, "-D": 0,
		"--noout":               0,
		"--syslog":              0,
		"--trace":               0,
		"--transient-store":     0,
		"--connection":          1,
		"-c":                    1,
		"--host":                1,
		"-H":                    1,
		"--cgroup-manager":      1,
		"--config":              1,
		"--conmon":              1,
		"--context":             1,
		"--cpu-profile":         1,
		"--db-backend":          1,
		"--default-mounts-file": 1,
		"--events-backend":      1,
		"--hooks-dir":           1,
		"--identity":            1,
		"--imagestore":          1,
		"--log-level":           1,
		"--max-workers":         1,
		"--memory-profile":      1,
		"--module":              1,
		"--namespace":           1,
		"--network-backend":     1,
		"--network-cmd-path":    1,
		"--network-config-dir":  1,
		"--out":                 1,
		"--registries-conf":     1,
		"--root":                1,
		"--runroot":             1,
		"--runtime":             1,
		"--runtime-flag":        1,
		"--ssh":                 1,
		"--storage-driver":      1,
		"--storage-opt":         1,
		"--tmpdir":              1,
		"--url":                 1,
		"--volumepath":          1,
	},
	TakesAttachedValue: pflagTakesAttachedValue,
}

// dockerGlobalOptions holds the options docker --help lists. They choose the
// daemon, its credentials, and the client's logging. --help and --version stay
// in place to keep matching their own patterns.
var dockerGlobalOptions = &model.GlobalOptions{
	Arguments: map[string]int{
		"--debug": 0, "-D": 0,
		"--tls":       0,
		"--tlsverify": 0,
		"--config":    1,
		"--context":   1,
		"-c":          1,
		"--host":      1,
		"-H":          1,
		"--log-level": 1,
		"-l":          1,
		"--tlscacert": 1,
		"--tlscert":   1,
		"--tlskey":    1,
	},
	TakesAttachedValue: pflagTakesAttachedValue,
}

// pflagTakesAttachedValue matches the spf13/pflag parser that podman and docker
// use, which reads --option=value for every long option, booleans included.
func pflagTakesAttachedValue(name string, _ int) bool {
	return strings.HasPrefix(name, "--")
}
