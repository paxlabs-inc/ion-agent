<div align="center">

<img src="assets/ion_brand.png"/>

# Ion

**एक स्थायी agent. टिकाऊ स्मृति. सीमित निष्पादन. दृश्यमान प्रमाण.**

Ion, [MatrixMCL](https://matrixmcl.com) का एक उन्नत सामान्य-प्रयोजन agent है — एक एकल
स्थायी पहचान जिसमें encrypted स्मृति, provider-निरपेक्ष model निष्पादन, और
tools, projects, browsers, तथा विशेषज्ञ agents तक operator-नियंत्रित पहुँच है।

[![CI](https://github.com/paxlabs-inc/ion-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/paxlabs-inc/ion-agent/actions/workflows/ci.yml)
[![CodeQL](https://github.com/paxlabs-inc/ion-agent/actions/workflows/codeql.yml/badge.svg)](https://github.com/paxlabs-inc/ion-agent/actions/workflows/codeql.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/paxlabs-inc/ion-agent.svg)](https://pkg.go.dev/github.com/paxlabs-inc/ion-agent)
[![Go Report Card](https://goreportcard.com/badge/github.com/paxlabs-inc/ion-agent)](https://goreportcard.com/report/github.com/paxlabs-inc/ion-agent)
[![License: MIT](https://img.shields.io/badge/License-MIT-informational.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](https://go.dev/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)
[![OpenSSF Best Practices](https://img.shields.io/badge/OpenSSF-best%20practices-informational.svg)](https://www.bestpractices.dev/)

[Product](https://ion.matrixmcl.com) ·
[Documentation](docs/) ·
[Architecture](ARCHITECTURE.md) ·
[Security](SECURITY.md) ·
[Contributing](CONTRIBUTING.md) ·
[Roadmap](ROADMAP.md) ·
[Discussions](https://github.com/paxlabs-inc/ion-agent/discussions)

[English](README.md) ·
[简体中文](README.zh-CN.md) ·
[Español](README.es.md) ·
**हिन्दी** ·
[العربية](README.ar.md) ·
[Français](README.fr.md) ·
[Português](README.pt-BR.md)

</div>

---

> **Pre-release सॉफ़्टवेयर।** web operator और core runtime प्राथमिक
> विकास पथ हैं। terminal client, supervised browser, Computer, और
> Software Studio अभी भी [`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx) में
> दर्ज स्वीकृति सीमाओं के अधीन हैं। Ion यह दावा नहीं करता कि कोई
> operation सफल हुआ जब तक कि production पथ ने आधिकारिक परिणाम
> प्रमाण उत्पन्न न किया हो। अनुपलब्ध subsystems को गढ़े गए डेटा द्वारा
> प्रस्तुत करने के बजाय अनुपलब्ध के रूप में ही दर्शाया जाता है।

## विषय-सूची

- [Ion क्यों](#why-ion)
- [विशेषताएँ](#features)
- [Architecture](#architecture)
- [त्वरित शुरुआत](#quick-start)
  - [Docker के साथ चलाएँ](#run-with-docker)
  - [स्रोत से build करें](#build-from-source)
  - [Dev container](#dev-container)
- [Initialize करें](#initialize)
- [चलाएँ](#run)
- [Configuration](#configuration)
- [Deployment](#deployment)
- [प्रोजेक्ट संरचना](#project-layout)
- [विकास](#development)
- [परीक्षण और सत्यापन](#testing-and-validation)
- [Security](#security)
- [Roadmap](#roadmap)
- [समुदाय और समर्थन](#community-and-support)
- [योगदान](#contributing)
- [License](#license)

## Ion क्यों

अधिकांश agent frameworks केवल libraries हैं जिन्हें आप एक ऐसी process में
जोड़ते हैं जो बाहर निकलते ही सब कुछ भूल जाती है। Ion इसका उल्टा है: एक
**एकल टिकाऊ runtime** जो पहचान, स्मृति, policy, और प्रमाण का स्वामी है, और
यह अधिकार पतले operator clients को प्रदान करता है।

- **एक पहचान, कई sessions नहीं।** Ion एक स्थायी actor है जिसमें एक
  सतत स्व-मॉडल, टिकाऊ स्मृति, और एक स्थिर audit trail है — प्रत्येक अनुरोध पर
  एक नया context नहीं।
- **अधिकार runtime में रहता है।** Go runtime policy, approvals,
  idempotency, और audit का स्वामी है। web और terminal clients एक उत्पन्न
  control-plane protocol को render करते हैं और कभी भी subsystem स्थिति नहीं गढ़ते।
- **आशावाद पर प्रमाण।** किसी परिणामकारी operation को केवल तभी सफल
  बताया जाता है जब वास्तविक subsystem ने आधिकारिक परिणाम प्रमाण उत्पन्न किया हो और
  टिकाऊ स्थिति लिखी जा चुकी हो। अनुपलब्ध क्षमताओं को अनुपलब्ध के रूप में दर्शाया जाता है।
- **डिज़ाइन से provider-निरपेक्ष।** Model निष्पादन एक provider परत के पीछे
  अमूर्त किया गया है जिसमें स्पष्ट, क्रमबद्ध fallback और credential rotation है।
- **सीमित प्रभाव-क्षेत्र।** विशेषज्ञ sub-agents को सीमित अधिकार मिलता है और वे
  कभी vault keys विरासत में नहीं लेते। Browser और project runtimes सीमित पथों, ports,
  environments, और takeover leases वाली supervised local processes हैं।

## विशेषताएँ

| क्षमता | विवरण |
|---|---|
| **Encrypted स्मृति और sessions** | Actor-scoped, AES-256-GCM envelope encryption जिसमें KEK → User Key → per-object DEK पदानुक्रम, atomic rotation, और shutdown पर key zeroization है। |
| **टिकाऊ session store** | WAL, single-writer queue, reader pool, versioned embedded migrations, और compression-triggered child sessions के साथ Pure-Go SQLite। |
| **Provider-निरपेक्ष निष्पादन** | एक provider-निरपेक्ष request/generation/tool-call/stream model जिसमें validated wire adapters और rate limits तथा विफलताओं पर क्रमबद्ध fallback है। |
| **Policy, approval और audit** | परिणामकारी tools policy, मानवीय approval, idempotency, और audit सीमाओं से गुज़रते हैं। कोई सामान्य accepted-only mutation प्रतिक्रियाएँ नहीं। |
| **टिकाऊ कार्य और scheduling** | कार्य ट्रैकिंग, scheduling, और पुनर्प्राप्ति restarts से बच जाती है; task lifecycle operator connectivity से अलग है। |
| **सीमित विशेषज्ञ agents** | सीमित lifecycle वाले scoped sub-agents की एक registry जो कभी vault keys विरासत में नहीं लेती। |
| **Supervised browser** | Chrome DevTools Protocol के माध्यम से SSRF और private-network नियंत्रणों के अंतर्गत संचालित Chromium sessions। |
| **Vector retrieval** | उच्च-recall समानता खोज के लिए एक वैकल्पिक Rust HNSW sidecar। |
| **Web operator** | chat, approvals, sessions, providers, memory, security, projects, Computer, और Software Studio projections वाला एक React operator। |
| **Terminal operator** | local attachment और supervised operation के लिए एक embedded React Ink terminal client। |
| **Generated protocol** | एक एकल Go control-plane catalog साझा TypeScript client उत्पन्न करता है जिसका उपयोग हर operator करता है; drift को CI में अस्वीकार कर दिया जाता है। |

## Architecture

```text
Web operator ─┐
              ├─ authenticated control plane ─ agent runtime ─ providers
Terminal UI ──┘                │                    │
                               │                    ├─ policy-bound tools
                               │                    ├─ supervised projects/browser
                               │                    └─ bounded specialist agents
                               │
                               └─ encrypted session, memory, audit, and work state
```

डिफ़ॉल्ट deployment एक एकल local `ion` process है। सादा HTTP केवल
loopback तक सीमित है; remote पहुँच के लिए एक operator-प्रबंधित TLS reverse proxy आवश्यक है।

अधिकार और निष्पादन एक ही पथ का अनुसरण करते हैं:

1. एक authenticated actor एक control-plane request प्रस्तुत करता है।
2. operator application actor, session, channel, profile, और
   approval context को हल करता है।
3. परिणामकारी operations policy, approval, idempotency, और audit से गुज़रते हैं।
4. runtime वास्तविक subsystem कार्यान्वयन को निष्पादित करता है।
5. सफलता को प्रस्तुत करने से **पहले** टिकाऊ स्थिति और प्रमाण लिखे जाते हैं।
6. Clients परिणामी स्थिति को render करते हैं, जिसमें स्पष्ट अनुपलब्ध और आंशिक
   परिणाम शामिल हैं।

पूर्ण design के लिए, [ARCHITECTURE.md](ARCHITECTURE.md) देखें।

## त्वरित शुरुआत

### Docker के साथ चलाएँ

Ion को स्थानीय रूप से आज़माने का सबसे तेज़ तरीका। operator डिफ़ॉल्ट रूप से केवल loopback है।

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

# Build the image and start the web operator on http://127.0.0.1:4174
docker compose -f docker/docker-compose.yml up --build
```

<http://127.0.0.1:4174> खोलें। image
variants, volumes, और environment variables के लिए [docker/README.md](docker/README.md) देखें।

### स्रोत से build करें

**आवश्यकताएँ**

| Tool | संस्करण |
|---|---|
| Go | 1.26.5 |
| Node.js | 22.22+ (Node 22 line) |
| npm | 11 |
| Rust | 1.78.0 (वैकल्पिक HNSW service) |
| Chromium | native browser acceptance tests के लिए |

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

make build
```

release build नियतात्मक web और terminal artifacts को `bin/ion` में embed करता है।

### Dev container

Go, Node, Rust, और Chromium पहले से इंस्टॉल किए गए एक तैयार-कोडिंग environment को
[`.devcontainer/`](.devcontainer/) के अंतर्गत प्रदान किया गया है। VS Code में,
**Dev Containers: Reopen in Container** चलाएँ, या GitHub Codespaces बटन का उपयोग करें।
`make build` से लेकर `make ci` तक सब कुछ बिना किसी अतिरिक्त सेटअप के काम करता है।

## Initialize करें

Production initialization host के संरक्षित key source का उपयोग करता है:

```bash
./bin/ion init
```

समर्थित संरक्षित key source के बिना headless विकास environments स्पष्ट रूप से
केवल-विकास वाले file KEK का विकल्प चुन सकते हैं:

```bash
./bin/ion init --dev-file-kek
```

> विकास fallback का उपयोग production deployment
> तंत्र के रूप में **नहीं** किया जाना चाहिए।

## चलाएँ

```bash
# Web operator (http://127.0.0.1:4174 by default)
./bin/ion dashboard

# Terminal operator with a supervised local runtime
./bin/ion tui

# Attach the terminal operator to an existing dashboard runtime
./bin/ion tui --attach

# Print version, commit, and build metadata
./bin/ion version
```

सादा HTTP केवल loopback है। remote पहुँच के लिए एक operator-प्रबंधित TLS
reverse proxy आवश्यक है।

## Configuration

Ion अपनी data directory, listen address, और key source को flags और
environment variables से पढ़ता है। सबसे सामान्य विकल्प:

| Flag / Env | डिफ़ॉल्ट | विवरण |
|---|---|---|
| `--data-dir` / `ION_DATA_DIR` | `~/.ion` | टिकाऊ data directory (SQLite, vault, work state)। |
| `--listen` / `ION_WEB_LISTEN` | `127.0.0.1:4174` | Web operator listen address। केवल loopback से bind करें। |
| `--dev-file-kek` | off | केवल-विकास वाला file KEK। production में कभी उपयोग न करें। |

पूर्ण संदर्भ के लिए [docs/configuration.md](docs/configuration.md) देखें।

## Deployment

Ion, [`deploy/`](deploy/) के अंतर्गत production-उन्मुख deployment assets प्रदान करता है:

- **Docker Compose** — TLS reverse proxy के पीछे एक single-host operator के लिए
  [`deploy/compose/`](deploy/compose/)।
- **Kubernetes** — Kustomize base के साथ [`deploy/kubernetes/`](deploy/kubernetes/) manifests
  (namespace, deployment, service, config, ingress)।
- **Helm** — पैरामीटरयुक्त installs के लिए [`deploy/helm/ion/`](deploy/helm/ion/) chart।
- **systemd** — bare-metal hosts के लिए [`deploy/systemd/`](deploy/systemd/) unit।

Ion को loopback से आगे उजागर करने से पहले [docs/deployment.md](docs/deployment.md) और [deploy/README.md](deploy/README.md)
पढ़ें। TLS termination, key source, और network
egress नियंत्रण operator की ज़िम्मेदारियाँ हैं।

## प्रोजेक्ट संरचना

```text
cmd/ion/                 CLI and runtime entry point
cmd/ion-web-e2e/         build-tagged browser acceptance helper
internal/agent/          provider/tool turn loop
internal/controlplane/   generated client contract and transports
internal/operatorapp/    production capability wiring and projections
internal/security/       vault, policy, sandbox, SSRF, and safety controls
internal/session/        encrypted durable session state
internal/memory/         journal, integrity, retrieval, and Cortex services
internal/project/        workspace, terminal, Git, runtime, and preview control
internal/browser/        supervised Chromium sessions
internal/swarm/          bounded specialist-agent registry and lifecycle
internal/tools/          policy-bound tool manager and lifecycle
internal/work/           durable work tracking
internal/scheduler/      durable scheduled work
ui/shared/               generated protocol types and client
ui/web/                  React web operator
ui/tui/                  embedded terminal operator
hnsw-service/            Rust vector-search sidecar
migrations/              embedded SQLite migrations
spec/ion_spec/           authoritative product specification
deploy/                  Kubernetes, Helm, Compose, and systemd assets
docker/                  container image and local Compose stack
tests/                   integration, adversarial, and clean-install acceptance
```

## विकास

```bash
make build          # Build the operator release (web + TUI + Go binary)
make build-all      # Also build the Rust HNSW sidecar
make run            # Build and run the web operator
make dev            # Auto-rebuild on change (air)
make fmt tidy       # Format and tidy
make help           # List every target
```

PR खोलने से पहले जानने योग्य घरेलू नियम:

- पूर्ण, चलाने योग्य artifacts प्रदान करें — diffs नहीं।
- वास्तविक बाहरी सीमाओं को छोड़कर कोई stubs, mocks, या fakes नहीं।
- Client UI परतों को background-color contrast से अलग करता है, कभी border strokes से नहीं।
- UI या output में कोई emojis, purple gradients, या glow effects नहीं।

[CONTRIBUTING.md](CONTRIBUTING.md) और
[`spec/ion_spec/ENGINEERING_STANDARDS.md`](spec/ion_spec/ENGINEERING_STANDARDS.md) देखें।

## परीक्षण और सत्यापन

```bash
make test-unit        # Unit tests with the race detector
make vet              # go vet
make verify-deps      # Verify checksummed Go and Rust dependencies
make test-operator    # Shared, web, TUI, browser, accessibility, and budget gates
make spec-validate    # Validate the authoritative spec.kvx
make ci               # Full CI pipeline
```

CI, Go, Rust, और operator clients में प्रत्येक push और pull request पर समान gates
चलाता है, और generated-contract तथा documentation drift को अस्वीकार करता है।
[`.github/workflows/`](.github/workflows/) देखें।

## Security

Ion स्वायत्त क्रिया क्षमता वाला एक सतत-उपस्थिति agent है, और इसके
security model को एक प्रथम-श्रेणी उत्पाद surface के रूप में माना जाता है: आठ adversary
classes, परिभाषित crown-jewel assets, और बाध्यकारी security architecture निर्णय
(SADRs)। Sub-agents कभी vault keys विरासत में नहीं लेते, idle-time principals उच्च-जोखिम
या बाहरी operations निष्पादित नहीं कर सकते, और सभी safety overrides logged और
user-दृश्यमान होते हैं।

**सार्वजनिक issues में vulnerabilities की रिपोर्ट न करें।**
[GitHub private vulnerability reporting](https://github.com/paxlabs-inc/ion-agent/security/advisories/new)
का उपयोग करें और [SECURITY.md](SECURITY.md) में दी गई प्रक्रिया का पालन करें।

## Roadmap

आधिकारिक योजना और task स्थिति
[`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx) में रहती है। एक मानव-पठनीय सारांश
[ROADMAP.md](ROADMAP.md) में बनाए रखा जाता है। उत्पन्न सारांश सूचनात्मक
projections हैं, आधिकारिक task ledger नहीं।

## समुदाय और समर्थन

- **प्रश्न और विचार** — [GitHub Discussions](https://github.com/paxlabs-inc/ion-agent/discussions)
- **Bugs और features** — [GitHub Issues](https://github.com/paxlabs-inc/ion-agent/issues)
- **मदद कैसे प्राप्त करें** — [SUPPORT.md](SUPPORT.md)
- **समुदाय मानक** — [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- **प्रोजेक्ट governance** — [GOVERNANCE.md](GOVERNANCE.md)

## योगदान

योगदान का स्वागत है। [CONTRIBUTING.md](CONTRIBUTING.md) पढ़ें, कोई
issue उठाएँ, और एक केंद्रित pull request खोलें। सभी योगदानकर्ताओं से
[Code of Conduct](CODE_OF_CONDUCT.md) का पालन करने की अपेक्षा की जाती है।

## License

Ion एक मुफ़्त और open-source सॉफ़्टवेयर है जो [MIT License](LICENSE) के अंतर्गत लाइसेंस प्राप्त है।

<div align="center">

Copyright © 2026 MatrixMCL — <a href="https://ion.matrixmcl.com">ion.matrixmcl.com</a>

</div>
