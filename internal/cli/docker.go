package cli

import (
	"context"
	"log/slog"
	"runtime"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/dockerx"
)

// dockerEngine decides once per run where Docker executes (native or inside WSL) and remembers the answer.
func (a *App) dockerEngine(ctx context.Context, cfg *config.Config, log *slog.Logger) (dockerx.Engine, error) {
	if a.engine != nil {
		return *a.engine, nil
	}
	goos := a.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	e, err := dockerx.Detect(ctx, cfg.Build.Docker, cfg.Build.WSLDistro, goos, a.Runner)
	if err != nil {
		return dockerx.Engine{}, err
	}
	a.engine = &e
	log.Info("docker engine", "mode", e.String(), "server", e.Version)
	return e, nil
}
