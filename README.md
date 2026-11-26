# forge

Self-hosted CI/CD platform in Go for building, transferring, and deploying Docker images securely over VPN.

FORGE builds, tests, packages and ships images; the deployment on the server is done by
[`itc-deploy`](../itc-deploy) (`deltactl`), which FORGE triggers over SSH. See [`FORGE_PLAN.md`](FORGE_PLAN.md).

## Development

```bash
make check   # gofmt (check only), go vet, go test
make build   # bin/forge
```
