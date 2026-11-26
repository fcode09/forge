# forge

Self-hosted CI/CD platform in Go for building, transferring, and deploying Docker images securely over VPN.

FORGE builds, tests, packages and ships images; the deployment on the server is done by
[`itc-deploy`](../itc-deploy) (`deltactl`), which FORGE triggers over SSH. See [`FORGE_PLAN.md`](FORGE_PLAN.md).

## Install (Windows)

```powershell
cd E:\ITC\forge
go install ./cmd/forge      # puts forge.exe in %GOPATH%\bin (add it to PATH)
forge version
```

Needs Go (see `go.mod`), Git and OpenSSH (`ssh` and `scp`, included with Windows). Docker can be Docker
Desktop, or Docker installed only inside WSL: FORGE finds it (`build.docker: auto`) and, when it lives in WSL,
runs `wsl.exe -e docker` and translates `E:\...` paths to `/mnt/e/...` by itself. `forge doctor` shows which one it uses.

## Ship images to your server (no deltactl)

For when you only need the images on the server and you move them into place yourself:

```yaml
# forge.yaml (delete the deployment section)
build:
  docker: auto                # auto | native | wsl
  images:
    - {name: backend, image: auth-backend}
upload:
  target: mpa                 # an alias from your ~/.ssh/config, or user@host
  remote_dir: images          # relative to your home on the server; it must already exist
```

```powershell
forge doctor                  # Docker, git, ssh, scp
forge ship                    # build + package + scp -> ~/images/<release-id>/ on the server
forge ship --dry-run          # show what would run
forge ship --release ID       # upload an already packaged release (no rebuild)
```

- FORGE refuses to build with uncommitted changes, because the release ID carries the commit. Your own
  `forge.yaml`, `dist/` and `.forge/` never count. To build anyway, set `build.allow_dirty: true` in
  `forge.yaml` (or pass `--allow-dirty` once): the release ID then ends in `-dirty`.
- One `scp -r` is one connection, so a password prompt appears **once**. Authentication is whatever `ssh mpa`
  does in your terminal: FORGE does not disable passwords and never stores them.
- Host keys are always verified (`StrictHostKeyChecking=yes`): connect once with `ssh mpa` so the host is known.
- Nothing runs on the server. The release folder holds `images/<name>.tar.gz`, `manifest.json`, `metadata.json`
  and `checksums.sha256`. To check the copy there: `cd images/<release-id> && sha256sum -c checksums.sha256`,
  then `gunzip -c images/<name>.tar.gz | docker load`.
- `retention.releases` trims the local `dist/` only. A release folder that already exists on the server is
  overwritten file by file: release IDs are unique (`<UTC timestamp>-<commit>`), so this only happens if you
  repeat `forge ship --release ID`.
- An interrupted upload leaves a partial folder on the server (no resume): delete it and repeat.

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
| `forge ship` | build + package + copy the release to `upload.target` with `scp` (nothing runs on the server) |
| `forge transfer` | Upload to `<remote_dir>/incoming/<release-id>` and verify with `sha256sum` on the host |
| `forge deploy` | build + package + transfer, then `deltactl` load, tag, check, deploy |
| `forge rollback --yes` | `deltactl rollback` for the instance (without `--yes` it only shows the current tags) |
| `forge status` | `deltactl status` of the instance plus the last local event |
| `forge releases` | Local history of deploys, rollbacks and uploads (`.forge/history.jsonl`) |

`transfer`, `deploy`, `rollback` and `status` need the `deployment` section; `ship` needs `upload`.

Useful flags: `--dry-run` (build, deploy, ship), `--tag`, `--allow-dirty`, `--skip-tests`, `--release ID` and
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
