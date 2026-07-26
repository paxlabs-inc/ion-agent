<div align="center">

<img src="assets/ion_brand.png"/>

# Ion

**Un agent persistant. Une mémoire durable. Une exécution bornée. Des preuves visibles.**

Ion est un agent généraliste avancé de [MatrixMCL](https://matrixmcl.com) — une identité
unique et persistante dotée d'une mémoire chiffrée, d'une exécution de modèles neutre vis-à-vis
du fournisseur, et d'un accès contrôlé par l'opérateur aux outils, aux projets, aux navigateurs
et aux agents spécialisés.

[![CI](https://github.com/paxlabs-inc/ion-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/paxlabs-inc/ion-agent/actions/workflows/ci.yml)
[![CodeQL](https://github.com/paxlabs-inc/ion-agent/actions/workflows/codeql.yml/badge.svg)](https://github.com/paxlabs-inc/ion-agent/actions/workflows/codeql.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/paxlabs-inc/ion-agent.svg)](https://pkg.go.dev/github.com/paxlabs-inc/ion-agent)
[![Go Report Card](https://goreportcard.com/badge/github.com/paxlabs-inc/ion-agent)](https://goreportcard.com/report/github.com/paxlabs-inc/ion-agent)
[![License: MIT](https://img.shields.io/badge/License-MIT-informational.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](https://go.dev/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)
[![OpenSSF Best Practices](https://img.shields.io/badge/OpenSSF-best%20practices-informational.svg)](https://www.bestpractices.dev/)

[Produit](https://ion.matrixmcl.com) ·
[Documentation](docs/) ·
[Architecture](ARCHITECTURE.md) ·
[Sécurité](SECURITY.md) ·
[Contribuer](CONTRIBUTING.md) ·
[Feuille de route](ROADMAP.md) ·
[Discussions](https://github.com/paxlabs-inc/ion-agent/discussions)

[English](README.md) ·
[简体中文](README.zh-CN.md) ·
[Español](README.es.md) ·
[हिन्दी](README.hi.md) ·
[العربية](README.ar.md) ·
**Français** ·
[Português](README.pt-BR.md)

</div>

---

> **Logiciel en pré-version.** L'opérateur web et le runtime central constituent le
> principal axe de développement. Le client terminal, le navigateur supervisé, Computer
> et Software Studio restent soumis aux limites d'acceptation consignées dans
> [`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). Ion ne prétend pas qu'une
> opération a réussi tant que le chemin de production n'a pas produit de preuve de résultat
> faisant autorité. Les sous-systèmes indisponibles sont projetés comme indisponibles plutôt
> que représentés par des données inventées.

## Table des matières

- [Pourquoi Ion](#pourquoi-ion)
- [Fonctionnalités](#fonctionnalités)
- [Architecture](#architecture)
- [Démarrage rapide](#démarrage-rapide)
  - [Exécuter avec Docker](#exécuter-avec-docker)
  - [Compiler depuis les sources](#compiler-depuis-les-sources)
  - [Conteneur de développement](#conteneur-de-développement)
- [Initialiser](#initialiser)
- [Exécuter](#exécuter)
- [Configuration](#configuration)
- [Déploiement](#déploiement)
- [Structure du projet](#structure-du-projet)
- [Développement](#développement)
- [Tests et validation](#tests-et-validation)
- [Sécurité](#sécurité)
- [Feuille de route](#feuille-de-route)
- [Communauté et support](#communauté-et-support)
- [Contribuer](#contribuer)
- [Licence](#licence)

## Pourquoi Ion

La plupart des frameworks d'agents sont des bibliothèques que l'on assemble dans un
processus qui oublie tout dès qu'il se termine. Ion fait l'inverse : c'est un **runtime
unique et durable** qui détient l'identité, la mémoire, la politique et les preuves, et
qui expose cette autorité à des clients opérateurs légers.

- **Une identité, pas de multiples sessions.** Ion est un acteur unique et persistant,
  doté d'un modèle de soi continu, d'une mémoire durable et d'une piste d'audit stable —
  et non d'un contexte réinitialisé à chaque requête.
- **L'autorité réside dans le runtime.** Le runtime Go détient la politique, les
  approbations, l'idempotence et l'audit. Les clients web et terminal affichent un
  protocole de plan de contrôle généré et n'inventent jamais l'état des sous-systèmes.
- **La preuve plutôt que l'optimisme.** Une opération conséquente n'est signalée comme
  réussie que lorsque le sous-système réel a produit une preuve de résultat faisant autorité
  et qu'un état durable a été écrit. Les capacités indisponibles sont affichées comme
  indisponibles.
- **Neutre vis-à-vis du fournisseur par conception.** L'exécution des modèles est
  abstraite derrière une couche fournisseur assortie d'un mécanisme de repli explicite et
  ordonné, ainsi que d'une rotation des identifiants.
- **Rayon d'impact borné.** Les sous-agents spécialisés reçoivent une autorité restreinte
  et n'héritent jamais des clés du vault. Les runtimes de navigateur et de projet sont des
  processus locaux supervisés aux chemins, ports, environnements et baux de prise en main
  bornés.

## Fonctionnalités

| Capacité | Description |
|---|---|
| **Mémoire et sessions chiffrées** | Chiffrement d'enveloppe AES-256-GCM à portée d'acteur, avec une hiérarchie KEK → Clé utilisateur → DEK par objet, rotation atomique et mise à zéro des clés à l'arrêt. |
| **Stockage de sessions durable** | SQLite en Go pur avec WAL, file d'attente à écrivain unique, pool de lecteurs, migrations embarquées versionnées et sessions enfants déclenchées par compression. |
| **Exécution neutre vis-à-vis du fournisseur** | Un modèle de requête/génération/appel d'outil/flux indépendant du fournisseur, avec des adaptateurs de transport validés et un repli ordonné en cas de limitation de débit et d'échec. |
| **Politique, approbation et audit** | Les outils conséquents passent par les frontières de politique, d'approbation humaine, d'idempotence et d'audit. Aucune réponse générique de mutation simplement acceptée. |
| **Travaux et planification durables** | Le suivi du travail, la planification et la reprise survivent aux redémarrages ; le cycle de vie des tâches est découplé de la connectivité de l'opérateur. |
| **Agents spécialisés bornés** | Un registre de sous-agents à portée restreinte, au cycle de vie borné, qui n'héritent jamais des clés du vault. |
| **Navigateur supervisé** | Des sessions Chromium pilotées via le Chrome DevTools Protocol sous contrôles SSRF et de réseau privé. |
| **Recherche vectorielle** | Un sidecar Rust HNSW optionnel pour une recherche de similarité à fort rappel. |
| **Opérateur web** | Un opérateur React avec chat, approbations, sessions, fournisseurs, mémoire, sécurité, projets, Computer et projections Software Studio. |
| **Opérateur terminal** | Un client terminal React Ink embarqué pour l'attachement local et l'exploitation supervisée. |
| **Protocole généré** | Un unique catalogue de plan de contrôle Go génère le client TypeScript partagé qu'utilise chaque opérateur ; toute dérive est rejetée en CI. |

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

Le déploiement par défaut est un unique processus local `ion`. Le HTTP en clair est limité
au bouclage local ; l'accès distant requiert un proxy inverse TLS géré par l'opérateur.

L'autorité et l'exécution suivent un seul chemin :

1. Un acteur authentifié soumet une requête au plan de contrôle.
2. L'application opérateur résout l'acteur, la session, le canal, le profil et le
   contexte d'approbation.
3. Les opérations conséquentes passent par la politique, l'approbation, l'idempotence et l'audit.
4. Le runtime exécute l'implémentation réelle du sous-système.
5. L'état durable et les preuves sont écrits **avant** que le succès ne soit projeté.
6. Les clients affichent l'état résultant, y compris les résultats explicitement
   indisponibles et partiels.

Pour la conception complète, voir [ARCHITECTURE.md](ARCHITECTURE.md).

## Démarrage rapide

### Exécuter avec Docker

Le moyen le plus rapide d'essayer Ion en local. L'opérateur est limité au bouclage local par défaut.

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

# Build the image and start the web operator on http://127.0.0.1:4174
docker compose -f docker/docker-compose.yml up --build
```

Ouvrez <http://127.0.0.1:4174>. Voir [docker/README.md](docker/README.md) pour les
variantes d'image, les volumes et les variables d'environnement.

### Compiler depuis les sources

**Prérequis**

| Outil | Version |
|---|---|
| Go | 1.26.5 |
| Node.js | 22.22+ (ligne Node 22) |
| npm | 11 |
| Rust | 1.78.0 (service HNSW optionnel) |
| Chromium | pour les tests d'acceptation du navigateur natif |

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

make build
```

La compilation de release intègre des artefacts web et terminal déterministes dans `bin/ion`.

### Conteneur de développement

Un environnement prêt à coder, avec Go, Node, Rust et Chromium préinstallés, est
fourni sous [`.devcontainer/`](.devcontainer/). Dans VS Code, exécutez
**Dev Containers: Reopen in Container**, ou utilisez le bouton GitHub Codespaces.
Tout, de `make build` à `make ci`, fonctionne sans configuration supplémentaire.

## Initialiser

L'initialisation en production utilise la source de clés protégée de l'hôte :

```bash
./bin/ion init
```

Les environnements de développement sans interface graphique et sans source de clés
protégée prise en charge peuvent explicitement opter pour la KEK sur fichier réservée au
développement :

```bash
./bin/ion init --dev-file-kek
```

> Le repli de développement ne doit **pas** être utilisé comme mécanisme de déploiement
> en production.

## Exécuter

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

Le HTTP en clair est limité au bouclage local. L'accès distant requiert un proxy inverse
TLS géré par l'opérateur.

## Configuration

Ion lit son répertoire de données, son adresse d'écoute et sa source de clés depuis des
drapeaux et des variables d'environnement. Les réglages les plus courants :

| Drapeau / Env | Défaut | Description |
|---|---|---|
| `--data-dir` / `ION_DATA_DIR` | `~/.ion` | Répertoire de données durable (SQLite, vault, état des travaux). |
| `--listen` / `ION_WEB_LISTEN` | `127.0.0.1:4174` | Adresse d'écoute de l'opérateur web. À lier uniquement au bouclage local. |
| `--dev-file-kek` | off | KEK sur fichier réservée au développement. Ne jamais utiliser en production. |

Voir [docs/configuration.md](docs/configuration.md) pour la référence complète.

## Déploiement

Ion livre des ressources de déploiement orientées production sous [`deploy/`](deploy/) :

- **Docker Compose** — [`deploy/compose/`](deploy/compose/) pour un opérateur
  mono-hôte derrière un proxy inverse TLS.
- **Kubernetes** — manifestes [`deploy/kubernetes/`](deploy/kubernetes/)
  (namespace, deployment, service, config, ingress) avec une base Kustomize.
- **Helm** — chart [`deploy/helm/ion/`](deploy/helm/ion/) pour des installations
  paramétrées.
- **systemd** — unité [`deploy/systemd/`](deploy/systemd/) pour les hôtes bare-metal.

Lisez [docs/deployment.md](docs/deployment.md) et [deploy/README.md](deploy/README.md)
avant d'exposer Ion au-delà du bouclage local. La terminaison TLS, la source de clés et les
contrôles de sortie réseau relèvent de la responsabilité de l'opérateur.

## Structure du projet

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

## Développement

```bash
make build          # Build the operator release (web + TUI + Go binary)
make build-all      # Also build the Rust HNSW sidecar
make run            # Build and run the web operator
make dev            # Auto-rebuild on change (air)
make fmt tidy       # Format and tidy
make help           # List every target
```

Règles internes à connaître avant d'ouvrir une PR :

- Livrez des artefacts complets et exécutables — pas des diffs.
- Aucun stub, mock ou fake, sauf aux véritables frontières externes.
- L'interface client sépare les couches par contraste de couleur de fond, jamais par
  traits de bordure.
- Aucun emoji, dégradé violet ni effet de halo dans l'interface ou les sorties.

Voir [CONTRIBUTING.md](CONTRIBUTING.md) et
[`spec/ion_spec/ENGINEERING_STANDARDS.md`](spec/ion_spec/ENGINEERING_STANDARDS.md).

## Tests et validation

```bash
make test-unit        # Unit tests with the race detector
make vet              # go vet
make verify-deps      # Verify checksummed Go and Rust dependencies
make test-operator    # Shared, web, TUI, browser, accessibility, and budget gates
make spec-validate    # Validate the authoritative spec.kvx
make ci               # Full CI pipeline
```

La CI exécute les mêmes contrôles à chaque push et pull request sur Go, Rust et les
clients opérateurs, et rejette toute dérive du contrat généré et de la documentation. Voir
[`.github/workflows/`](.github/workflows/).

## Sécurité

Ion est un agent à présence continue doté d'une capacité d'action autonome, et son modèle
de sécurité est traité comme une surface produit de premier plan : huit classes
d'adversaires, des actifs joyaux de la couronne définis, et des décisions d'architecture de
sécurité contraignantes (SADR). Les sous-agents n'héritent jamais des clés du vault, les
principaux inactifs ne peuvent pas exécuter d'opérations à haut risque ou externes, et
toutes les dérogations de sécurité sont journalisées et visibles par l'utilisateur.

**Ne signalez pas de vulnérabilités dans des tickets publics.** Utilisez le
[signalement privé de vulnérabilités de GitHub](https://github.com/paxlabs-inc/ion-agent/security/advisories/new)
et suivez le processus décrit dans [SECURITY.md](SECURITY.md).

## Feuille de route

Le plan faisant autorité et l'état des tâches résident dans
[`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). Un résumé lisible par l'humain est
maintenu dans [ROADMAP.md](ROADMAP.md). Les résumés générés sont des projections
informatives, et non le registre de tâches faisant autorité.

## Communauté et support

- **Questions et idées** — [GitHub Discussions](https://github.com/paxlabs-inc/ion-agent/discussions)
- **Bugs et fonctionnalités** — [GitHub Issues](https://github.com/paxlabs-inc/ion-agent/issues)
- **Comment obtenir de l'aide** — [SUPPORT.md](SUPPORT.md)
- **Standards de la communauté** — [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- **Gouvernance du projet** — [GOVERNANCE.md](GOVERNANCE.md)

## Contribuer

Les contributions sont les bienvenues. Lisez [CONTRIBUTING.md](CONTRIBUTING.md),
choisissez un ticket et ouvrez une pull request ciblée. Tous les contributeurs sont tenus
de respecter le [Code de conduite](CODE_OF_CONDUCT.md).

## Licence

Ion est un logiciel libre et open-source sous [licence MIT](LICENSE).

<div align="center">

Copyright © 2026 MatrixMCL — <a href="https://ion.matrixmcl.com">ion.matrixmcl.com</a>

</div>
