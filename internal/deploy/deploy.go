// Package deploy applies an already staged release on the host through deltactl: load the images, point the
// instance at the new tags, validate, deploy and, if that fails, roll back to the tags that were running.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
)

// Image is one staged image archive and the deltactl service it belongs to.
type Image struct {
	Service string
	File    string // relative to the staging directory, e.g. images/backend.tar.gz
}

// Plan is what to apply.
type Plan struct {
	ReleaseID  string
	StagingDir string
	Images     []Image
}

// Ops are the deltactl operations Apply needs (implemented by remote.Deltactl).
type Ops interface {
	Load(ctx context.Context, file string) error
	GetTag(ctx context.Context, service string) (string, error)
	SetTag(ctx context.Context, service, tag string) error
	Check(ctx context.Context, service string) error
	Deploy(ctx context.Context, service string) error
	Rollback(ctx context.Context, service string) error
	Status(ctx context.Context) (string, error)
}

// Outcome of Apply.
type Outcome string

// Possible outcomes.
const (
	// Deployed: every service is running the new release.
	Deployed Outcome = "deployed"
	// Aborted: something failed before any service was deployed; the previous tags are back and nothing was restarted.
	Aborted Outcome = "aborted"
	// RolledBack: a deploy failed and the services are back on the tags that were running before.
	RolledBack Outcome = "rolled_back"
	// RollbackFailed: a deploy failed and the previous state could not be confirmed. Needs a person.
	RollbackFailed Outcome = "rollback_failed"
)

// Result is what happened.
type Result struct {
	Outcome   Outcome  `json:"outcome"`
	ReleaseID string   `json:"release_id"`
	Failed    string   `json:"failed,omitempty"`   // the step that failed
	Problems  []string `json:"problems,omitempty"` // secondary problems found while recovering
	Status    string   `json:"status,omitempty"`   // deltactl status after a successful deploy
}

// Apply runs the plan. It returns a non-nil error exactly when Outcome != Deployed; the Result is always set.
func Apply(ctx context.Context, ops Ops, log *slog.Logger, p Plan) (*Result, error) {
	res := &Result{ReleaseID: p.ReleaseID}
	services := make([]string, len(p.Images))
	for i, im := range p.Images {
		services[i] = im.Service
	}
	if len(services) == 0 {
		res.Outcome, res.Failed = Aborted, "plan"
		return res, errors.New("nothing to deploy: the plan has no images")
	}
	// Recovery must still run after Ctrl+C, so it never uses the caller's cancellable context.
	rec := context.WithoutCancel(ctx)

	old := map[string]string{}
	for _, s := range services {
		tag, err := ops.GetTag(ctx, s)
		if err != nil {
			res.Outcome, res.Failed = Aborted, "read current tag of "+s
			return res, err
		}
		if tag == "" {
			// Without a current tag there is nothing to roll back to, and `env set` could not restore an empty value.
			res.Outcome, res.Failed = Aborted, "read current tag of "+s
			return res, fmt.Errorf("%s has no image tag configured yet: set it once with deltactl (a first deployment is done by hand) before using forge", s)
		}
		old[s] = tag
		log.Info("current tag", "service", s, "tag", tag)
	}

	for _, im := range p.Images {
		log.Info("loading image", "service", im.Service, "file", im.File)
		if err := ops.Load(ctx, path.Join(p.StagingDir, im.File)); err != nil {
			res.Outcome, res.Failed = Aborted, "load "+im.Service
			return res, err
		}
	}

	var tagged []string
	for _, s := range services {
		if err := ops.SetTag(ctx, s, p.ReleaseID); err != nil {
			res.restoreTags(rec, ops, log, append(tagged, s), old)
			res.Outcome, res.Failed = Aborted, "set tag of "+s
			return res, err
		}
		tagged = append(tagged, s)
	}

	for _, s := range services {
		log.Info("checking", "service", s)
		if err := ops.Check(ctx, s); err != nil {
			res.restoreTags(rec, ops, log, services, old)
			res.Outcome, res.Failed = Aborted, "check "+s
			return res, err
		}
	}

	for i, s := range services {
		log.Info("deploying", "service", s, "release", p.ReleaseID)
		err := ops.Deploy(ctx, s)
		if err == nil {
			continue
		}
		res.Failed = "deploy " + s
		// Services after the failed one were never touched: only their tag needs to go back.
		res.restoreTags(rec, ops, log, services[i+1:], old)
		res.rollBack(rec, ops, log, services[:i+1], old)
		return res, err
	}

	if st, err := ops.Status(ctx); err != nil {
		res.Problems = append(res.Problems, "status unavailable: "+err.Error())
	} else {
		res.Status = st
	}
	res.Outcome = Deployed
	return res, nil
}

// rollBack reverts the given services (most recent first) and confirms the tags are the previous ones.
func (r *Result) rollBack(ctx context.Context, ops Ops, log *slog.Logger, services []string, old map[string]string) {
	r.Outcome = RolledBack
	for i := len(services) - 1; i >= 0; i-- {
		s := services[i]
		log.Warn("rolling back", "service", s)
		if err := ops.Rollback(ctx, s); err != nil {
			r.Outcome = RollbackFailed
			r.Problems = append(r.Problems, fmt.Sprintf("rollback %s: %v", s, err))
			continue
		}
		got, err := ops.GetTag(ctx, s)
		switch {
		case err != nil:
			r.Outcome = RollbackFailed
			r.Problems = append(r.Problems, fmt.Sprintf("cannot confirm the tag of %s after rollback: %v", s, err))
		case got != old[s]:
			r.Outcome = RollbackFailed
			r.Problems = append(r.Problems, fmt.Sprintf("%s rolled back to tag %q but %q was running before", s, got, old[s]))
		}
	}
}

// restoreTags puts the previous tag back for services whose tag was changed but that were never deployed.
func (r *Result) restoreTags(ctx context.Context, ops Ops, log *slog.Logger, services []string, old map[string]string) {
	for _, s := range services {
		if err := ops.SetTag(ctx, s, old[s]); err != nil {
			log.Error("could not restore tag", "service", s, "tag", old[s], "err", err)
			r.Problems = append(r.Problems, fmt.Sprintf("could not restore %s tag to %q: %v", s, old[s], err))
		}
	}
}

// Describe returns the remote commands Apply would run for a plan, in order, for --dry-run.
func Describe(p Plan, deltactl, instance string) []string {
	var out []string
	d := func(args ...string) string { return deltactl + " " + strings.Join(args, " ") }
	for _, im := range p.Images {
		v := strings.ToUpper(im.Service) + "_TAG"
		out = append(out, d("env", instance, "get", v))
	}
	for _, im := range p.Images {
		out = append(out, d("load", path.Join(p.StagingDir, im.File)))
	}
	for _, im := range p.Images {
		out = append(out, d("env", instance, "set", strings.ToUpper(im.Service)+"_TAG", p.ReleaseID))
	}
	for _, im := range p.Images {
		out = append(out, d("check", instance, im.Service))
	}
	for _, im := range p.Images {
		out = append(out, d("deploy", instance, im.Service))
	}
	return append(out, d("status", instance))
}

// Revert asks deltactl to roll the given services back to their previous tags and reports the tags before
// and after. It is the manual counterpart of the automatic recovery in Apply. A tag that did not change is
// reported as a problem because it means there was nothing to roll back to.
func Revert(ctx context.Context, ops Ops, log *slog.Logger, services []string) (*Result, error) {
	res := &Result{Outcome: RolledBack}
	if len(services) == 0 {
		res.Outcome, res.Failed = Aborted, "plan"
		return res, errors.New("no services to roll back")
	}
	before := map[string]string{}
	for _, s := range services {
		tag, err := ops.GetTag(ctx, s)
		if err != nil {
			res.Outcome, res.Failed = Aborted, "read current tag of "+s
			return res, err
		}
		before[s] = tag
	}
	var firstErr error
	for i := len(services) - 1; i >= 0; i-- {
		s := services[i]
		log.Warn("rolling back", "service", s, "from", before[s])
		if err := ops.Rollback(ctx, s); err != nil {
			res.Outcome, res.Failed = RollbackFailed, "rollback "+s
			res.Problems = append(res.Problems, fmt.Sprintf("rollback %s: %v", s, err))
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		after, err := ops.GetTag(ctx, s)
		switch {
		case err != nil:
			res.Outcome = RollbackFailed
			res.Problems = append(res.Problems, fmt.Sprintf("cannot confirm the tag of %s after rollback: %v", s, err))
		case after == before[s]:
			res.Problems = append(res.Problems, fmt.Sprintf("%s is still on tag %q: there was no previous version to go back to", s, after))
		default:
			log.Info("rolled back", "service", s, "from", before[s], "to", after)
		}
	}
	if firstErr == nil && res.Outcome == RollbackFailed {
		firstErr = errors.New("could not confirm the rollback")
	}
	return res, firstErr
}
