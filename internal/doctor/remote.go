package doctor

import (
	"context"
	"strconv"
	"strings"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
)

// RemoteRunner runs a command on the production host (implemented by transport.Client).
type RemoteRunner interface {
	Run(ctx context.Context, argv ...string) (execx.Result, error)
}

// RunRemote checks the production host over SSH. It only reads: no file is created or changed.
// If the connection fails the remaining checks are skipped.
func RunRemote(ctx context.Context, r RemoteRunner, d config.Deployment) []Check {
	var out []Check
	if res, err := r.Run(ctx, "true"); err != nil {
		return []Check{{"ssh-connect", Fail, d.User + "@" + d.Host + ": " + firstLine(res.Stderr, err)}}
	}
	out = append(out, Check{"ssh-connect", OK, d.User + "@" + d.Host})

	if res, err := r.Run(ctx, "test", "-w", d.RemoteDir); err != nil {
		out = append(out, Check{"remote-dir", Fail, d.RemoteDir + " does not exist or is not writable by " + d.User + failSuffix(res)})
	} else {
		out = append(out, Check{"remote-dir", OK, d.RemoteDir})
		out = append(out, checkRemoteSpace(ctx, r, d.RemoteDir))
	}
	if res, err := r.Run(ctx, "env", "-C", "/", "sha256sum", "--version"); err != nil {
		out = append(out, Check{"remote-sha256", Fail, "env -C and sha256sum are required on the host" + failSuffix(res)})
	} else {
		out = append(out, Check{"remote-sha256", OK, strings.SplitN(strings.TrimSpace(res.Stdout), "\n", 2)[0]})
	}
	if res, err := r.Run(ctx, "test", "-x", d.DeltactlPath); err != nil {
		out = append(out, Check{"deltactl", Fail, d.DeltactlPath + " is not an executable file" + failSuffix(res)})
	} else {
		out = append(out, Check{"deltactl", OK, d.DeltactlPath})
		out = append(out, checkInterruptedRuns(ctx, r, d))
	}
	return out
}

func checkRemoteSpace(ctx context.Context, r RemoteRunner, dir string) Check {
	res, err := r.Run(ctx, "df", "-Pk", dir)
	if err != nil {
		return Check{"remote-space", Warn, "cannot read free space" + failSuffix(res)}
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return Check{"remote-space", Warn, "unexpected df output"}
	}
	kb, err := strconv.ParseInt(f[3], 10, 64)
	if err != nil {
		return Check{"remote-space", Warn, "unexpected df output"}
	}
	return Check{"remote-space", OK, strconv.FormatInt(kb/1024, 10) + " MiB free in " + dir}
}

func failSuffix(r execx.Result) string {
	if s := strings.TrimSpace(r.Stderr); s != "" {
		return ": " + strings.SplitN(s, "\n", 2)[0]
	}
	return ""
}

// checkInterruptedRuns warns when deltactl lists an interrupted operation: until someone reviews it with
// `deltactl runs resolve`, deltactl refuses every new operation. This is a text heuristic on the listing
// (it only looks for the word) and only ever warns; FORGE never resolves a run by itself.
func checkInterruptedRuns(ctx context.Context, r RemoteRunner, d config.Deployment) Check {
	res, err := r.Run(ctx, d.DeltactlPath, "runs", "list", "--instance", d.Instance, "-n", "20", "--json")
	if err != nil {
		return Check{"deltactl-runs", Warn, "cannot read the run history" + failSuffix(res)}
	}
	out := strings.ToLower(res.Stdout)
	if strings.Contains(out, "interrumpida") || strings.Contains(out, "interrupted") {
		return Check{"deltactl-runs", Warn, "an operation is marked interrupted: deltactl blocks new operations until it is reviewed (deltactl runs show last, then runs resolve <id>)"}
	}
	return Check{"deltactl-runs", OK, "no interrupted operations in the last 20 runs"}
}
