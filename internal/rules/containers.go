package rules

// podmanGlobalOptions holds the options podman 4.9 lists in podman --help, the
// GLOBAL OPTIONS of podman(1). Some point podman at other programs (--runtime,
// --conmon, --hooks-dir, --module, --network-cmd-path), but the shipped
// podman:* already allows running any container, so stripping them hides
// nothing the shipped presets would deny. --help and --version stay in place to
// keep matching their own patterns. An option missing here, such as a hidden
// one like -H or -D, or one added after 4.9, also stays in place and leaves the
// command matching only podman:*.
var podmanGlobalOptions = globalOptions{
	arguments: map[string]int{
		"--remote": 0, "-r": 0,
		"--syslog":             0,
		"--transient-store":    0,
		"--connection":         1,
		"-c":                   1,
		"--cgroup-manager":     1,
		"--conmon":             1,
		"--events-backend":     1,
		"--hooks-dir":          1,
		"--identity":           1,
		"--imagestore":         1,
		"--log-level":          1,
		"--module":             1,
		"--network-cmd-path":   1,
		"--network-config-dir": 1,
		"--out":                1,
		"--root":               1,
		"--runroot":            1,
		"--runtime":            1,
		"--runtime-flag":       1,
		"--ssh":                1,
		"--storage-driver":     1,
		"--storage-opt":        1,
		"--tmpdir":             1,
		"--url":                1,
		"--volumepath":         1,
	},
	takesAttachedValue: pflagTakesAttachedValue,
	unverified:         podmanUnverified,
}

// dockerGlobalOptions holds the options docker --help lists. They choose the
// daemon, its credentials, and the client's logging. --help and --version stay
// in place to keep matching their own patterns.
var dockerGlobalOptions = globalOptions{
	arguments: map[string]int{
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
	takesAttachedValue: pflagTakesAttachedValue,
	unverified:         dockerUnverified,
}
