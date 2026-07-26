# Ion documentation

Welcome to the Ion documentation. Ion is a persistent general agent runtime from
[MatrixMCL](https://matrixmcl.com): one identity with durable memory, bounded
execution, and visible evidence.

## Start here

- [Getting started](getting-started.md) — install, initialize, and run Ion.
- [Installation](installation.md) — every install method in detail.
- [Configuration](configuration.md) — flags, environment variables, and paths.
- [Operator guide](operator.md) — the operator surface (generated).

## Operate

- [Deployment](deployment.md) — Docker, Kubernetes, Helm, and systemd.
- [Troubleshooting](troubleshooting.md) — common problems and fixes.
- [FAQ](faq.md) — frequently asked questions.

## Understand

- [Architecture](../ARCHITECTURE.md) — runtime shape and authority model.
- [Security policy](../SECURITY.md) — threat model and reporting.
- [Glossary](glossary.md) — terms used across Ion.
- [Agent account profile](agent-account-profile.md) — identity profile format.
- [Architecture decision records](adr/) — recorded design decisions.

## Contribute

- [Contributing](../CONTRIBUTING.md)
- [Engineering standards](../spec/ion_spec/ENGINEERING_STANDARDS.md)
- [Governance](../GOVERNANCE.md)
- [Roadmap](../ROADMAP.md)

The authoritative product specification is
[`spec/ion_spec/spec.kvx`](../spec/ion_spec/spec.kvx). Generated documents (such
as [operator.md](operator.md)) are projections of the implemented system and are
checked for drift in CI.
