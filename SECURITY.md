# SECURITY.md — Ion Security Policy

## Threat Model

Ion is a continuous-presence AI agent with autonomous action capabilities. This makes it a high-value target with a unique attack surface. Our security model addresses 8 adversary classes, 11 attack surfaces, and enforces 5 binding security architecture decisions.

## Adversary Classes

| ID | Adversary | Access Level |
|----|-----------|--------------|
| A1 | Malicious User | External (chat interface) |
| A2 | Prompt Injector | Indirect (via tool outputs) |
| A3 | Supply-Chain Attacker | Pre-install |
| A4 | Rogue Agent (self-compromise) | Internal (autonomous action) |
| A5 | Lateral Movement (compromised sub-agent) | Internal (sub-agent boundary) |
| A6 | Insider (operator with vault access) | Privileged (infrastructure) |
| A7 | Network Attacker (MITM) | Network layer |
| A8 | Cassandra Abuser | Internal (via crafted evidence) |

## Crown Jewels

| Asset | Classification |
|-------|---------------|
| KEK / User Key | CRITICAL |
| Self-Model | CRITICAL |
| SOUL.md | CRITICAL |
| Premise Ledger | HIGH |
| Signed Receipts | HIGH |
| Emotional State | HIGH |
| Vault (all encrypted data) | CRITICAL |
| Relationship Model | HIGH |

## Binding Security Decisions (SADR)

1. **SADR-001**: Emotional state is read-only for safety pipeline
2. **SADR-002**: Dreamweaver never touches Identity, Preference, or Constraint memories
3. **SADR-003**: Sub-agents never inherit vault keys
4. **SADR-004**: Idle-time principals cannot execute RED or external operations
5. **SADR-005**: All safety overrides are logged and user-visible

## Three Non-Negotiables

1. No monetary damage without explicit approval
2. No reputational damage without explicit approval
3. No psychological damage without explicit approval

## Reporting a Vulnerability

**DO NOT** open a public GitHub issue for security vulnerabilities.

Use
[GitHub private vulnerability reporting](https://github.com/paxlabs-inc/ion-agent/security/advisories/new).

Please include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

Maintainers will acknowledge a complete report as soon as practical and will
coordinate disclosure and remediation through the private advisory.

## Security Testing

Every release undergoes:
- Automated adversarial testing (premise injection, emotional manipulation, memory poisoning, sub-agent lateral movement, SSRF bypass, Cassandra abuse)
- Manual security review of all SADR enforcement points
- Dependency vulnerability scanning
- Supply-chain verification

## Bug Bounty

We do not currently operate a bug bounty program. We may in the future.
