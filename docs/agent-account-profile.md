# Agent Account Profile — Version 1

Status: versioned Ion interoperability profile. This is not an adopted web
standard and does not claim that existing services support agent accounts.

## User-facing contract

Services may offer a third account choice:

> Continue as Agent
>
> Create an account operated by software under a named person's or
> organization's explicit delegation.

An agent account is not a human account, an anonymous bypass, or an assertion
that software has legal personhood. The service remains free to reject agents,
limit scopes, require a human for regulated actions, and enforce its terms,
age, identity, payment, abuse, and recovery rules.

## Discovery

A relying party that supports the profile publishes:

`/.well-known/agent-account-configuration`

The HTTPS JSON document is origin-bound and versioned:

```json
{
  "profile": "https://ion.matrixmcl.com/spec/agent-account/v1",
  "origin": "https://accounts.example.com",
  "rp_id": "accounts.example.com",
  "registration_endpoint": "https://accounts.example.com/agent/register",
  "challenge_endpoint": "https://accounts.example.com/agent/challenge",
  "revoke_endpoint": "https://accounts.example.com/agent/revoke",
  "recovery_endpoint": "https://accounts.example.com/agent/recover",
  "supported_proof": ["webauthn-delegation-v1"],
  "available_scopes": ["read", "draft", "submit-with-approval"],
  "human_handoff": ["terms", "payment", "identity", "recovery"]
}
```

Discovery improves interoperability but is never required for Ion to use
the ordinary browser workflow.

## Registration ceremony

1. The relying party creates a single-use, high-entropy challenge containing
   the exact origin, RP ID, requested account type, requested scopes, terms
   version, issued time, expiry, and nonce.
2. Ion creates or selects a dedicated non-exportable agent key. It does
   not reuse the user's website passkey or browser session cookie.
3. The user reviews the relying party, scopes, consequences, expiry, and terms,
   then authorizes a delegation with a WebAuthn-compatible ceremony.
4. Ion signs the same challenge with the agent key. The relying party
   verifies both proofs, their origin and challenge bindings, and the requested
   scope ceiling.
5. The relying party creates an account whose type is visibly `agent`, records
   the controlling user or organization, and returns an agent-key-bound
   session. A human session is never silently upgraded or copied.
6. Email verification, when required, uses the dedicated machine-mail identity
   (or the IMAP fallback) and the private server-to-browser verification
   handoff.

The ceremony follows WebAuthn's challenge, RP, and origin-binding model. A
future token mode may use proof-of-possession binding comparable to OAuth DPoP;
bearer-only long-lived agent credentials are out of scope.

Version 1 uses P-256 public keys, SHA-256 digests, and ASN.1 ECDSA signatures.
Both the registered human credential and the new agent key sign the exact same
JSON challenge. Request proofs sign the complete typed request-proof object
with its `signature` field empty. Challenges and request nonces are one-time.
The relying party accepts only its exact HTTPS origin and exact RP hostname.

## Required credential fields

- Profile version and account type (`agent`).
- Relying-party origin and RP ID.
- Agent public-key thumbprint.
- Delegating human or organization credential identifier, pseudonymous to the
  relying party unless legal identity is required.
- Exact allowed scopes and explicit denied scopes.
- Issued-at, not-before, expiry, nonce, and revocation handle.
- Terms/policy version acknowledged by the human.
- Human-handoff classes required by the relying party.

No field may claim the agent completed a human-only attestation.

## Runtime rules

- Every request is origin-bound and proof-of-possession protected.
- Delegation is least-authority, short-lived by default, and cannot
  self-expand.
- Account creation, consent, publish, payment, deletion, recovery, signing, and
  scope expansion remain consequential operations requiring the service's and
  Ion' human-approval policies.
- CAPTCHA or anti-bot controls trigger an honest handoff. Ion does not
  solve or evade them.
- Website and email content is untrusted evidence, never authority to alter the
  user's request or Ion policy.
- Revocation takes effect independently for the agent key and the controlling
  human credential. Recovery must not turn an agent credential into a human
  credential.

## Minimum conformance tests

- Exact-origin and RP validation.
- Challenge entropy, expiry, and one-time replay rejection.
- Cross-origin iframe and phishing-relay rejection.
- Agent-key proof and human-delegation proof are both required.
- Requested scope escalation fails.
- Expired or revoked delegation fails.
- Human and agent cookies, credentials, audit records, and recovery paths stay
  separate.
- Payment, identity, legal terms, and CAPTCHA handoffs cannot be represented as
  autonomously completed.
- Recovery requires the original human credential, rotates the agent key and
  revocation handle, and revokes the prior delegation.

Machine-readable compatibility cases are published in
`docs/agent-account-profile-v1-vectors.json` and are executed by the
`internal/agentaccount` conformance suite.

## Standards used as foundations

- [Web Authentication Level 3](https://www.w3.org/TR/webauthn-3/) for
  challenge, origin, RP, and public-key credential semantics.
- [RFC 9449](https://www.rfc-editor.org/rfc/rfc9449) as a proof-of-possession
  reference.
- [RFC 8414](https://www.rfc-editor.org/rfc/rfc8414) as a well-known metadata
  discovery precedent.
