# CONTRIBUTING.md — How to Contribute to Ion

## Prerequisites

- Go 1.26.5
- Rust 1.78.0
- Node.js 22.22+
- golangci-lint 2.12.2
- Git

## Getting Started

```bash
# Clone
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

# Build everything
make build

# Run tests
make test

# Run linter
make lint
```

## Development Workflow

1. Fork the repository
2. Create a feature branch: `git checkout -b feat/your-feature`
3. Write code following the [Engineering Standards](spec/ion_spec/ENGINEERING_STANDARDS.md)
4. Write tests (≥80% coverage on new code)
5. Run the full CI pipeline: `make ci`
6. Open a pull request

## Code Standards

### Go

- Follow [Effective Go](https://go.dev/doc/effective_go)
- Follow [Uber Go Style Guide](https://github.com/uber-go/guide)
- All exported symbols have godoc comments
- No panic in library code
- Context as first parameter for I/O functions
- Errors handled or explicitly discarded with comment

### Rust

- Follow [Rust API Guidelines](https://rust-lang.github.io/api-guidelines/)
- Clippy clean with zero warnings
- Documentation on all public items

### TypeScript

- Strict mode enabled
- Type checking clean with zero errors
- Types preferred over interfaces for object shapes

## Testing

- Tests are proof, not ritual
- No stubs, mocks, or fakes except at true external boundaries
- Every acceptance criterion needs at least one test
- Performance tests set budgets, not just measure
- Adversarial tests exercise real attack paths

## Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add premise ledger extraction
fix: correct emotional state decay calculation
docs: update architecture diagram
test: add adversarial test for premise injection
refactor: extract policy pipeline into separate package
chore: update dependencies
```

## Pull Requests

- One feature per PR
- All CI checks must pass
- At least one human reviewer required
- No lint warnings
- Coverage thresholds met
- spec.kvx updated if requirements changed

## Questions?

Open a
[GitHub discussion](https://github.com/paxlabs-inc/ion-agent/discussions).
