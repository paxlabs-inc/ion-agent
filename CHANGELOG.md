# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial project scaffolding
- Engineering standards specification (`spec/ENGINEERING_STANDARDS.md`)
- Machine-readable specification (`spec/spec.kvx`)
- Architecture design document (`spec/design.body.md`)
- Foundation directory structure
- Pure-Go SQLite session store with required WAL pragmas, versioned embedded
  migrations, a single-writer queue, four-reader pool, and metadata-only FTS5
  search
- Compression-triggered child sessions linked to their parent at 75% context
  utilization
- AES-256-GCM envelope encryption with a KEK to User Key to per-object DEK
  hierarchy, generic decryption failures, atomic rotation, and key zeroization
- Linux libsecret KEK integration and an explicit 0600 development file
  fallback
- Wave 1 acceptance tests covering real SQLite concurrency, encrypted storage,
  FTS5 isolation, session splitting, key rotation, tamper detection, and
  shutdown zeroization
- Provider-neutral O1 request, generation, tool-call, stream, capability, and
  token-usage types with validated OpenAI and Anthropic wire adapters
- Ordered provider fallback with automatic credential rotation on HTTP 429 and
  secret-free per-key usage accounting
- Self-registering Go tool manager with source discovery, deterministic tool
  surfaces, 30-second readiness caching, and 60-second last-good failure grace
- Context-bounded tool execution and a basic perception-to-action agent loop
  that returns tool results and errors to the provider
- Wave 2 acceptance tests using real HTTP servers, provider translations,
  readiness failure and recovery, dispatch timeouts, and a full tool-calling
  agent turn
- Go module with locked pure-Go dependencies
- Makefile with build, test, lint, and CI targets
- golangci-lint configuration
- GitHub Actions CI/CD pipeline
- Docker support
- Security policy
- Contributing guidelines
- EditorConfig for consistent formatting
