# Frequently asked questions

## What is Ion?

Ion is an advanced general agent from [MatrixMCL](https://matrixmcl.com): one
persistent identity with encrypted, durable memory, provider-neutral model
execution, and operator-controlled access to tools, projects, browsers, and
specialist agents. Authority lives in a Go runtime; the web and terminal
operators render a generated control-plane protocol and never invent subsystem
state.

## How is Ion different from an agent framework or library?

Most frameworks are libraries you assemble into a process that forgets its state
when it exits. Ion is a single durable runtime that owns identity, memory,
policy, approvals, and audit evidence, and exposes that authority to thin
operator clients.

## Is Ion production-ready?

Ion is pre-release software. The web operator and core runtime are the primary
development path. Other subsystems remain behind the acceptance boundaries
recorded in [`spec/ion_spec/spec.kvx`](../spec/ion_spec/spec.kvx). Ion never
reports success without authoritative outcome evidence, and shows unavailable
subsystems as unavailable.

## Which model providers does Ion support?

Ion executes models through a provider-neutral layer with validated wire
adapters and ordered fallback with credential rotation. Model execution is
abstracted rather than tied to a single provider.

## Why does the web operator only listen on loopback?

Plain HTTP is restricted to loopback to avoid unencrypted remote exposure. For
remote access, terminate TLS in an operator-managed reverse proxy that forwards
to `127.0.0.1:4174`. See [deployment](deployment.md).

## What is the development file KEK, and can I use it in production?

It is an explicit, opt-in fallback for machines without a supported host
protected key source, intended for development only. It is not a production
deployment mechanism.

## Do specialist sub-agents share Ion's keys?

No. Sub-agents receive scoped authority and never inherit vault keys. This is a
binding security decision. See [SECURITY.md](../SECURITY.md).

## Can I run more than one replica?

No. Ion is a single persistent identity backed by a `ReadWriteOnce` volume. Run
exactly one instance.

## What language is Ion written in?

The runtime is Go. The web operator is React and TypeScript; the terminal
operator is React Ink. An optional vector-search sidecar is written in Rust.

## How do I report a security issue?

Do not open a public issue. Use
[private vulnerability reporting](https://github.com/paxlabs-inc/ion-agent/security/advisories/new).
See [SECURITY.md](../SECURITY.md).

## What license is Ion under?

The [MIT License](../LICENSE).
