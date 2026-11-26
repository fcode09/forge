# forge

Self-hosted CI/CD platform in Go for building, transferring, and deploying Docker images securely over VPN.

FORGE builds, tests, packages and ships images; the deployment on the server is done by
[`itc-deploy`](../itc-deploy) (`deltactl`), which FORGE triggers over SSH. See [`FORGE_PLAN.md`](FORGE_PLAN.md).

## Commands

Run them from any directory inside the backend project that has a `forge.yaml` (see `forge.yaml.example`).

| Command | What it does |
|---|---|
| `forge init` | Write an example `forge.yaml` |
| `forge config validate` | Validate `forge.yaml` |
| `forge doctor [--remote]` | Check Docker, Git, ssh and config; with `--remote` also the host (SSH, free space, `sha256sum`, `deltactl`, interrupted runs) |
| `forge build` | Build the images tagged `<image>:<release-id>`, then run `build.tests` |
| `forge package` | `docker save` + gzip, `manifest.json`, `metadata.json`, `checksums.sha256` in `dist/<release-id>/` |
| `forge package --verify DIR` | Verify a release directory |
| `forge transfer` | Upload to `<remote_dir>/incoming/<release-id>` and verify with `sha256sum` on the host |
| `forge deploy` | build + package + transfer, then `deltactl` load, tag, check, deploy |
| `forge rollback --yes` | `deltactl rollback` for the instance (without `--yes` it only shows the current tags) |
| `forge status` | `deltactl status` of the instance plus the last local event |
| `forge releases` | Local history of deploys and rollbacks (`.forge/history.jsonl`) |

Useful flags: `--dry-run` (build, deploy), `--tag`, `--allow-dirty`, `--skip-tests`, `--release ID` and
`--skip-transfer` (deploy an already packaged / staged release), `--service s0`, `--json`, `--verbose`.

### Exit codes

`0` ok · `1` error · `2` usage · `3` deploy failed and the previous version is running again ·
`4` deploy failed and the previous state could **not** be confirmed (needs a person).

### How a deploy is made safe

- The release ID is `<UTC timestamp>-<commit>`; `latest` is refused. A dirty work tree is refused unless `--allow-dirty`.
- Releases are immutable: an existing `dist/<release-id>` is never overwritten.
- The host verifies every checksum before anything is loaded; a failed or partial upload removes only the
  staging directory this run created.
- Only a closed list of remote commands is ever sent (`mkdir`, `df`, `dd`, `env -C ... sha256sum`, `ls`, `rm` inside
  the staging area, and `deltactl load | env get/set | check | deploy | rollback | status | runs list`). Every
  argument is limited to `[A-Za-z0-9_@%+=:,./-]`; anything else is refused before ssh runs.
- SSH always uses `StrictHostKeyChecking=yes`, the `known_hosts` file from `forge.yaml`, `BatchMode` and no forwarding.
- Before touching tags FORGE records the current ones. If `check` fails they are restored; if `deploy` fails,
  `deltactl rollback --yes` runs and FORGE confirms the tags are the previous ones, otherwise exit code 4.
- A first deployment (instance without an image tag) is done by hand with deltactl; FORGE refuses to guess.
- Retention (`retention.releases`) trims `dist/` and the host staging area only. Loaded Docker images are never
  removed: `deltactl rollback` needs them.
- FORGE never modifies `itc-deploy` and never resolves a `deltactl` interrupted run by itself.

## Development

```bash
make check   # gofmt (check only), go vet, go test
make build   # bin/forge
```

Tests use doubles for Docker, ssh and `deltactl`; none of them touches a real server, Docker daemon or network.
The only dependency is `gopkg.in/yaml.v3`.
