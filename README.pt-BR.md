<div align="center">

<img src="assets/ion_brand.png"/>

# Ion

**Um agente persistente. Memória durável. Execução delimitada. Evidência visível.**

Ion é um agente geral avançado da [MatrixMCL](https://matrixmcl.com) — uma única
identidade persistente com memória criptografada, execução de modelos neutra em
relação ao provedor e acesso controlado pelo operador a ferramentas, projetos,
navegadores e agentes especialistas.

[![CI](https://github.com/paxlabs-inc/ion-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/paxlabs-inc/ion-agent/actions/workflows/ci.yml)
[![CodeQL](https://github.com/paxlabs-inc/ion-agent/actions/workflows/codeql.yml/badge.svg)](https://github.com/paxlabs-inc/ion-agent/actions/workflows/codeql.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/paxlabs-inc/ion-agent.svg)](https://pkg.go.dev/github.com/paxlabs-inc/ion-agent)
[![Go Report Card](https://goreportcard.com/badge/github.com/paxlabs-inc/ion-agent)](https://goreportcard.com/report/github.com/paxlabs-inc/ion-agent)
[![License: MIT](https://img.shields.io/badge/License-MIT-informational.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](https://go.dev/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)
[![OpenSSF Best Practices](https://img.shields.io/badge/OpenSSF-best%20practices-informational.svg)](https://www.bestpractices.dev/)

[Produto](https://ion.matrixmcl.com) ·
[Documentação](docs/) ·
[Arquitetura](ARCHITECTURE.md) ·
[Segurança](SECURITY.md) ·
[Contribuindo](CONTRIBUTING.md) ·
[Roadmap](ROADMAP.md) ·
[Discussões](https://github.com/paxlabs-inc/ion-agent/discussions)

[English](README.md) ·
[简体中文](README.zh-CN.md) ·
[Español](README.es.md) ·
[हिन्दी](README.hi.md) ·
[العربية](README.ar.md) ·
[Français](README.fr.md) ·
**Português**

</div>

---

> **Software em pré-lançamento.** O operador web e o runtime central são o
> principal caminho de desenvolvimento. O cliente de terminal, o navegador
> supervisionado, o Computer e o Software Studio permanecem sujeitos aos limites
> de aceitação registrados em
> [`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). O Ion não afirma que uma
> operação teve sucesso a menos que o caminho de produção tenha produzido
> evidência de resultado autoritativa. Subsistemas indisponíveis são projetados
> como indisponíveis, e não representados por dados inventados.

## Índice

- [Por que Ion](#por-que-ion)
- [Recursos](#recursos)
- [Arquitetura](#arquitetura)
- [Início rápido](#início-rápido)
  - [Executar com Docker](#executar-com-docker)
  - [Compilar a partir do código-fonte](#compilar-a-partir-do-código-fonte)
  - [Dev container](#dev-container)
- [Inicializar](#inicializar)
- [Executar](#executar)
- [Configuração](#configuração)
- [Implantação](#implantação)
- [Estrutura do projeto](#estrutura-do-projeto)
- [Desenvolvimento](#desenvolvimento)
- [Testes e validação](#testes-e-validação)
- [Segurança](#segurança)
- [Roadmap](#roadmap)
- [Comunidade e suporte](#comunidade-e-suporte)
- [Contribuindo](#contribuindo)
- [Licença](#licença)

## Por que Ion

A maioria dos frameworks de agentes são bibliotecas que você monta em um processo
que esquece tudo quando encerra. O Ion é o oposto: um **único runtime durável**
que detém identidade, memória, política e evidência, e expõe essa autoridade a
clientes de operador leves.

- **Uma identidade, não muitas sessões.** O Ion é um único ator persistente com
  um automodelo contínuo, memória durável e uma trilha de auditoria estável — não
  um contexto novo a cada requisição.
- **A autoridade reside no runtime.** O runtime em Go detém política, aprovações,
  idempotência e auditoria. Os clientes web e de terminal renderizam um protocolo
  de control plane gerado e nunca inventam o estado de subsistemas.
- **Evidência acima de otimismo.** Uma operação consequente só é reportada como
  bem-sucedida depois que o subsistema real produziu evidência de resultado
  autoritativa e o estado durável foi gravado. Capacidades indisponíveis são
  exibidas como indisponíveis.
- **Neutralidade de provedor por design.** A execução de modelos é abstraída por
  trás de uma camada de provedor com fallback explícito e ordenado e rotação de
  credenciais.
- **Raio de impacto delimitado.** Sub-agentes especialistas recebem autoridade
  restrita e nunca herdam chaves do vault. Os runtimes de navegador e de projeto
  são processos locais supervisionados com caminhos, portas, ambientes e leases
  de controle delimitados.

## Recursos

| Capacidade | Descrição |
|---|---|
| **Memória e sessões criptografadas** | Criptografia de envelope AES-256-GCM com escopo por ator, com hierarquia KEK → User Key → DEK por objeto, rotação atômica e zeragem de chaves no desligamento. |
| **Armazenamento de sessões durável** | SQLite puro em Go com WAL, fila de escritor único, pool de leitores, migrações embarcadas versionadas e sessões-filhas acionadas por compressão. |
| **Execução neutra em relação ao provedor** | Um modelo de requisição/geração/chamada de ferramenta/stream agnóstico ao provedor, com adaptadores de protocolo validados e fallback ordenado em limites de taxa e falhas. |
| **Política, aprovação e auditoria** | Ferramentas consequentes passam por limites de política, aprovação humana, idempotência e auditoria. Sem respostas genéricas de mutação apenas-aceitas. |
| **Trabalho durável e agendamento** | Rastreamento de trabalho, agendamento e recuperação sobrevivem a reinicializações; o ciclo de vida das tarefas é desacoplado da conectividade do operador. |
| **Agentes especialistas delimitados** | Um registro de sub-agentes com escopo restrito e ciclo de vida delimitado que nunca herdam chaves do vault. |
| **Navegador supervisionado** | Sessões do Chromium controladas pelo Chrome DevTools Protocol sob controles de SSRF e de rede privada. |
| **Recuperação vetorial** | Um sidecar HNSW opcional em Rust para busca por similaridade de alta revocação. |
| **Operador web** | Um operador em React com projeções de chat, aprovações, sessões, provedores, memória, segurança, projetos, Computer e Software Studio. |
| **Operador de terminal** | Um cliente de terminal embarcado em React Ink para conexão local e operação supervisionada. |
| **Protocolo gerado** | Um único catálogo de control plane em Go gera o cliente TypeScript compartilhado usado por todos os operadores; desvios são rejeitados na CI. |

## Arquitetura

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

A implantação padrão é um único processo `ion` local. O HTTP simples é limitado
ao loopback; o acesso remoto requer um proxy reverso TLS gerenciado pelo operador.

Autoridade e execução seguem um único caminho:

1. Um ator autenticado envia uma requisição de control plane.
2. A aplicação do operador resolve ator, sessão, canal, perfil e contexto de
   aprovação.
3. Operações consequentes passam por política, aprovação, idempotência e auditoria.
4. O runtime executa a implementação real do subsistema.
5. Estado durável e evidência são gravados **antes** que o sucesso seja projetado.
6. Os clientes renderizam o estado resultante, incluindo resultados explícitos de
   indisponibilidade e parciais.

Para o design completo, consulte [ARCHITECTURE.md](ARCHITECTURE.md).

## Início rápido

### Executar com Docker

A maneira mais rápida de experimentar o Ion localmente. O operador é somente
loopback por padrão.

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

# Build the image and start the web operator on http://127.0.0.1:4174
docker compose -f docker/docker-compose.yml up --build
```

Abra <http://127.0.0.1:4174>. Consulte [docker/README.md](docker/README.md) para
variantes de imagem, volumes e variáveis de ambiente.

### Compilar a partir do código-fonte

**Requisitos**

| Ferramenta | Versão |
|---|---|
| Go | 1.26.5 |
| Node.js | 22.22+ (linha Node 22) |
| npm | 11 |
| Rust | 1.78.0 (serviço HNSW opcional) |
| Chromium | para testes de aceitação nativos do navegador |

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

make build
```

O build de release embarca artefatos determinísticos de web e terminal em `bin/ion`.

### Dev container

Um ambiente pronto para programar, com Go, Node, Rust e Chromium pré-instalados,
é fornecido em [`.devcontainer/`](.devcontainer/). No VS Code, execute
**Dev Containers: Reopen in Container**, ou use o botão do GitHub Codespaces.
Tudo, de `make build` a `make ci`, funciona imediatamente.

## Inicializar

A inicialização de produção usa a fonte de chaves protegida do host:

```bash
./bin/ion init
```

Ambientes de desenvolvimento headless sem uma fonte de chaves protegida com
suporte podem optar explicitamente pelo KEK em arquivo, exclusivo para
desenvolvimento:

```bash
./bin/ion init --dev-file-kek
```

> O fallback de desenvolvimento **não** deve ser usado como mecanismo de
> implantação de produção.

## Executar

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

O HTTP simples é somente loopback. O acesso remoto requer um proxy reverso TLS
gerenciado pelo operador.

## Configuração

O Ion lê seu diretório de dados, endereço de escuta e fonte de chaves a partir de
flags e variáveis de ambiente. Os controles mais comuns:

| Flag / Env | Padrão | Descrição |
|---|---|---|
| `--data-dir` / `ION_DATA_DIR` | `~/.ion` | Diretório de dados durável (SQLite, vault, estado de trabalho). |
| `--listen` / `ION_WEB_LISTEN` | `127.0.0.1:4174` | Endereço de escuta do operador web. Vincule somente ao loopback. |
| `--dev-file-kek` | off | KEK em arquivo, exclusivo para desenvolvimento. Nunca use em produção. |

Consulte [docs/configuration.md](docs/configuration.md) para a referência completa.

## Implantação

O Ion inclui recursos de implantação orientados à produção em [`deploy/`](deploy/):

- **Docker Compose** — [`deploy/compose/`](deploy/compose/) para um operador de
  host único atrás de um proxy reverso TLS.
- **Kubernetes** — manifestos [`deploy/kubernetes/`](deploy/kubernetes/)
  (namespace, deployment, service, config, ingress) com uma base Kustomize.
- **Helm** — chart [`deploy/helm/ion/`](deploy/helm/ion/) para instalações
  parametrizadas.
- **systemd** — unit [`deploy/systemd/`](deploy/systemd/) para hosts bare-metal.

Leia [docs/deployment.md](docs/deployment.md) e [deploy/README.md](deploy/README.md)
antes de expor o Ion além do loopback. Terminação TLS, fonte de chaves e controles
de egresso de rede são responsabilidades do operador.

## Estrutura do projeto

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

## Desenvolvimento

```bash
make build          # Build the operator release (web + TUI + Go binary)
make build-all      # Also build the Rust HNSW sidecar
make run            # Build and run the web operator
make dev            # Auto-rebuild on change (air)
make fmt tidy       # Format and tidy
make help           # List every target
```

Regras da casa que vale conhecer antes de abrir um PR:

- Entregue artefatos completos e executáveis — não diffs.
- Sem stubs, mocks ou fakes, exceto em verdadeiros limites externos.
- A UI do cliente separa camadas por contraste de cor de fundo, nunca por traços
  de borda.
- Sem emojis, gradientes roxos ou efeitos de brilho na UI ou na saída.

Consulte [CONTRIBUTING.md](CONTRIBUTING.md) e
[`spec/ion_spec/ENGINEERING_STANDARDS.md`](spec/ion_spec/ENGINEERING_STANDARDS.md).

## Testes e validação

```bash
make test-unit        # Unit tests with the race detector
make vet              # go vet
make verify-deps      # Verify checksummed Go and Rust dependencies
make test-operator    # Shared, web, TUI, browser, accessibility, and budget gates
make spec-validate    # Validate the authoritative spec.kvx
make ci               # Full CI pipeline
```

A CI executa os mesmos gates em cada push e pull request em Go, Rust e nos clientes
de operador, e rejeita desvios de contrato gerado e de documentação. Consulte
[`.github/workflows/`](.github/workflows/).

## Segurança

O Ion é um agente de presença contínua com capacidade de ação autônoma, e seu
modelo de segurança é tratado como uma superfície de produto de primeira classe:
oito classes de adversários, ativos crown-jewel definidos e decisões vinculantes
de arquitetura de segurança (SADRs). Sub-agentes nunca herdam chaves do vault,
principais em tempo ocioso não podem executar operações de alto risco ou externas,
e todas as substituições de segurança são registradas e visíveis ao usuário.

**Não reporte vulnerabilidades em issues públicas.** Use o
[relato privado de vulnerabilidades do GitHub](https://github.com/paxlabs-inc/ion-agent/security/advisories/new)
e siga o processo em [SECURITY.md](SECURITY.md).

## Roadmap

O plano autoritativo e o status das tarefas residem em
[`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). Um resumo legível por humanos é
mantido em [ROADMAP.md](ROADMAP.md). Resumos gerados são projeções informativas,
não o registro autoritativo de tarefas.

## Comunidade e suporte

- **Perguntas e ideias** — [GitHub Discussions](https://github.com/paxlabs-inc/ion-agent/discussions)
- **Bugs e funcionalidades** — [GitHub Issues](https://github.com/paxlabs-inc/ion-agent/issues)
- **Como obter ajuda** — [SUPPORT.md](SUPPORT.md)
- **Padrões da comunidade** — [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- **Governança do projeto** — [GOVERNANCE.md](GOVERNANCE.md)

## Contribuindo

Contribuições são bem-vindas. Leia [CONTRIBUTING.md](CONTRIBUTING.md), assuma uma
issue e abra um pull request focado. Espera-se que todos os contribuidores sigam o
[Código de Conduta](CODE_OF_CONDUCT.md).

## Licença

O Ion é um software livre e de código aberto licenciado sob a
[Licença MIT](LICENSE).

<div align="center">

Copyright © 2026 MatrixMCL — <a href="https://ion.matrixmcl.com">ion.matrixmcl.com</a>

</div>
