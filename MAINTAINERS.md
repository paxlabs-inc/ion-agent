# Maintainers

This file lists the maintainers and area reviewers of the Ion project. See
[GOVERNANCE.md](GOVERNANCE.md) for what these roles mean and
[.github/CODEOWNERS](.github/CODEOWNERS) for the authoritative review routing.

## Maintainers

Maintainers own technical direction, the release process, and the specification.

| Maintainer | Affiliation | Areas |
|---|---|---|
| MatrixMCL Core Team | MatrixMCL | Runtime, security, specification, releases |

## Area reviewers

| Area | Path | Reviewers |
|---|---|---|
| Runtime & agent loop | `internal/agent/`, `internal/tools/` | MatrixMCL Core Team |
| Security | `internal/security/` | MatrixMCL Core Team |
| Session & memory | `internal/session/`, `internal/memory/` | MatrixMCL Core Team |
| Control plane & protocol | `internal/controlplane/`, `ui/shared/` | MatrixMCL Core Team |
| Web & terminal operators | `ui/web/`, `ui/tui/` | MatrixMCL Core Team |
| HNSW sidecar | `hnsw-service/` | MatrixMCL Core Team |
| Deployment & packaging | `deploy/`, `docker/`, `packaging/` | MatrixMCL Core Team |

## Contact

- Security reports: see [SECURITY.md](SECURITY.md)
- Conduct reports: conduct@matrixmcl.com
- General questions: [GitHub Discussions](https://github.com/paxlabs-inc/ion-agent/discussions)
