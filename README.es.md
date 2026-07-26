<div align="center">

<img src="assets/ion_brand.png"/>

# Ion

**Un agente persistente. Memoria duradera. Ejecución acotada. Evidencia visible.**

Ion es un agente general avanzado de [MatrixMCL](https://matrixmcl.com): una única
identidad persistente con memoria cifrada, ejecución de modelos neutral respecto al
proveedor y acceso controlado por el operador a herramientas, proyectos, navegadores
y agentes especialistas.

[![CI](https://github.com/paxlabs-inc/ion-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/paxlabs-inc/ion-agent/actions/workflows/ci.yml)
[![CodeQL](https://github.com/paxlabs-inc/ion-agent/actions/workflows/codeql.yml/badge.svg)](https://github.com/paxlabs-inc/ion-agent/actions/workflows/codeql.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/paxlabs-inc/ion-agent.svg)](https://pkg.go.dev/github.com/paxlabs-inc/ion-agent)
[![Go Report Card](https://goreportcard.com/badge/github.com/paxlabs-inc/ion-agent)](https://goreportcard.com/report/github.com/paxlabs-inc/ion-agent)
[![License: MIT](https://img.shields.io/badge/License-MIT-informational.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](https://go.dev/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)
[![OpenSSF Best Practices](https://img.shields.io/badge/OpenSSF-best%20practices-informational.svg)](https://www.bestpractices.dev/)

[Producto](https://ion.matrixmcl.com) ·
[Documentación](docs/) ·
[Arquitectura](ARCHITECTURE.md) ·
[Seguridad](SECURITY.md) ·
[Contribuir](CONTRIBUTING.md) ·
[Hoja de ruta](ROADMAP.md) ·
[Debates](https://github.com/paxlabs-inc/ion-agent/discussions)

[English](README.md) ·
[简体中文](README.zh-CN.md) ·
**Español** ·
[हिन्दी](README.hi.md) ·
[العربية](README.ar.md) ·
[Français](README.fr.md) ·
[Português](README.pt-BR.md)

</div>

---

> **Software en versión preliminar.** El operador web y el runtime central son la
> vía de desarrollo principal. El cliente de terminal, el navegador supervisado,
> Computer y Software Studio siguen sujetos a los límites de aceptación registrados
> en [`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). Ion no afirma que una
> operación tuvo éxito a menos que la vía de producción haya producido evidencia
> autoritativa del resultado. Los subsistemas no disponibles se proyectan como no
> disponibles en lugar de representarse con datos inventados.

## Tabla de contenidos

- [Por qué Ion](#por-qué-ion)
- [Características](#características)
- [Arquitectura](#arquitectura)
- [Inicio rápido](#inicio-rápido)
  - [Ejecutar con Docker](#ejecutar-con-docker)
  - [Compilar desde el código fuente](#compilar-desde-el-código-fuente)
  - [Contenedor de desarrollo](#contenedor-de-desarrollo)
- [Inicializar](#inicializar)
- [Ejecutar](#ejecutar)
- [Configuración](#configuración)
- [Despliegue](#despliegue)
- [Estructura del proyecto](#estructura-del-proyecto)
- [Desarrollo](#desarrollo)
- [Pruebas y validación](#pruebas-y-validación)
- [Seguridad](#seguridad)
- [Hoja de ruta](#hoja-de-ruta)
- [Comunidad y soporte](#comunidad-y-soporte)
- [Contribuir](#contribuir)
- [Licencia](#licencia)

## Por qué Ion

La mayoría de los frameworks de agentes son bibliotecas que ensamblas en un proceso
que olvida todo cuando termina. Ion es lo opuesto: un **único runtime duradero** que
posee identidad, memoria, política y evidencia, y expone esa autoridad a clientes de
operador ligeros.

- **Una identidad, no muchas sesiones.** Ion es un único actor persistente con un
  automodelo continuo, memoria duradera y un rastro de auditoría estable, no un
  contexto nuevo por cada petición.
- **La autoridad reside en el runtime.** El runtime en Go posee la política, las
  aprobaciones, la idempotencia y la auditoría. Los clientes web y de terminal
  renderizan un protocolo de control plane generado y nunca inventan el estado de
  los subsistemas.
- **Evidencia por encima de optimismo.** Una operación de consecuencia solo se
  reporta como exitosa una vez que el subsistema real produjo evidencia autoritativa
  del resultado y se escribió estado duradero. Las capacidades no disponibles se
  muestran como no disponibles.
- **Neutral respecto al proveedor por diseño.** La ejecución de modelos se abstrae
  detrás de una capa de proveedor con fallback explícito y ordenado y rotación de
  credenciales.
- **Radio de impacto acotado.** Los subagentes especialistas reciben autoridad
  acotada y nunca heredan claves del vault. Los runtimes de navegador y de proyecto
  son procesos locales supervisados con rutas, puertos, entornos y arrendamientos de
  toma de control acotados.

## Características

| Capacidad | Descripción |
|---|---|
| **Memoria y sesiones cifradas** | Cifrado de sobre AES-256-GCM con alcance de actor, con una jerarquía KEK → clave de usuario → DEK por objeto, rotación atómica y borrado a cero de claves al apagar. |
| **Almacén de sesiones duradero** | SQLite en Go puro con WAL, cola de escritor único, pool de lectores, migraciones embebidas versionadas y sesiones hijas activadas por compresión. |
| **Ejecución neutral respecto al proveedor** | Un modelo de petición/generación/llamada de herramienta/stream agnóstico al proveedor con adaptadores de protocolo validados y fallback ordenado ante límites de tasa y fallos. |
| **Política, aprobación y auditoría** | Las herramientas de consecuencia pasan por los límites de política, aprobación humana, idempotencia y auditoría. Sin respuestas de mutación genéricas de solo aceptado. |
| **Trabajo duradero y planificación** | El seguimiento de trabajo, la planificación y la recuperación sobreviven a los reinicios; el ciclo de vida de las tareas está desacoplado de la conectividad del operador. |
| **Agentes especialistas acotados** | Un registro de subagentes con alcance definido y ciclo de vida acotado que nunca heredan claves del vault. |
| **Navegador supervisado** | Sesiones de Chromium controladas mediante el Chrome DevTools Protocol bajo controles de SSRF y de red privada. |
| **Recuperación vectorial** | Un sidecar opcional en Rust con HNSW para búsqueda de similitud de alta exhaustividad. |
| **Operador web** | Un operador en React con proyecciones de chat, aprobaciones, sesiones, proveedores, memoria, seguridad, proyectos, Computer y Software Studio. |
| **Operador de terminal** | Un cliente de terminal React Ink embebido para la conexión local y la operación supervisada. |
| **Protocolo generado** | Un único catálogo de control plane en Go genera el cliente TypeScript compartido que utiliza cada operador; la divergencia se rechaza en CI. |

## Arquitectura

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

El despliegue por defecto es un único proceso `ion` local. El HTTP en texto plano
está limitado a loopback; el acceso remoto requiere un proxy inverso TLS gestionado
por el operador.

La autoridad y la ejecución siguen una sola vía:

1. Un actor autenticado envía una petición al control plane.
2. La aplicación de operador resuelve el actor, la sesión, el canal, el perfil y el
   contexto de aprobación.
3. Las operaciones de consecuencia pasan por política, aprobación, idempotencia y
   auditoría.
4. El runtime ejecuta la implementación real del subsistema.
5. El estado duradero y la evidencia se escriben **antes** de que se proyecte el
   éxito.
6. Los clientes renderizan el estado resultante, incluidos los resultados
   explícitos de no disponible y parcial.

Para el diseño completo, consulta [ARCHITECTURE.md](ARCHITECTURE.md).

## Inicio rápido

### Ejecutar con Docker

La forma más rápida de probar Ion localmente. El operador es solo de loopback por
defecto.

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

# Build the image and start the web operator on http://127.0.0.1:4174
docker compose -f docker/docker-compose.yml up --build
```

Abre <http://127.0.0.1:4174>. Consulta [docker/README.md](docker/README.md) para
variantes de imagen, volúmenes y variables de entorno.

### Compilar desde el código fuente

**Requisitos**

| Herramienta | Versión |
|---|---|
| Go | 1.26.5 |
| Node.js | 22.22+ (línea de Node 22) |
| npm | 11 |
| Rust | 1.78.0 (servicio HNSW opcional) |
| Chromium | para las pruebas de aceptación de navegador nativo |

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

make build
```

La compilación de release embebe artefactos web y de terminal deterministas en
`bin/ion`.

### Contenedor de desarrollo

Se proporciona un entorno listo para programar con Go, Node, Rust y Chromium
preinstalados en [`.devcontainer/`](.devcontainer/). En VS Code, ejecuta
**Dev Containers: Reopen in Container**, o usa el botón de GitHub Codespaces.
Todo, desde `make build` hasta `make ci`, funciona de inmediato.

## Inicializar

La inicialización de producción utiliza la fuente de claves protegida del host:

```bash
./bin/ion init
```

Los entornos de desarrollo sin interfaz que carezcan de una fuente de claves
protegida compatible pueden optar explícitamente por el KEK de archivo solo para
desarrollo:

```bash
./bin/ion init --dev-file-kek
```

> El fallback de desarrollo **no** debe usarse como mecanismo de despliegue en
> producción.

## Ejecutar

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

El HTTP en texto plano es solo de loopback. El acceso remoto requiere un proxy
inverso TLS gestionado por el operador.

## Configuración

Ion lee su directorio de datos, dirección de escucha y fuente de claves a partir de
flags y variables de entorno. Los ajustes más comunes:

| Flag / Env | Por defecto | Descripción |
|---|---|---|
| `--data-dir` / `ION_DATA_DIR` | `~/.ion` | Directorio de datos duradero (SQLite, vault, estado de trabajo). |
| `--listen` / `ION_WEB_LISTEN` | `127.0.0.1:4174` | Dirección de escucha del operador web. Enlazar solo a loopback. |
| `--dev-file-kek` | off | KEK de archivo solo para desarrollo. No usar nunca en producción. |

Consulta [docs/configuration.md](docs/configuration.md) para la referencia completa.

## Despliegue

Ion incluye recursos de despliegue orientados a producción en [`deploy/`](deploy/):

- **Docker Compose** — [`deploy/compose/`](deploy/compose/) para un operador de un
  solo host detrás de un proxy inverso TLS.
- **Kubernetes** — manifiestos de [`deploy/kubernetes/`](deploy/kubernetes/)
  (namespace, deployment, service, config, ingress) con una base de Kustomize.
- **Helm** — chart de [`deploy/helm/ion/`](deploy/helm/ion/) para instalaciones
  parametrizadas.
- **systemd** — unidad de [`deploy/systemd/`](deploy/systemd/) para hosts en
  bare-metal.

Lee [docs/deployment.md](docs/deployment.md) y [deploy/README.md](deploy/README.md)
antes de exponer Ion más allá de loopback. La terminación TLS, la fuente de claves y
los controles de egreso de red son responsabilidades del operador.

## Estructura del proyecto

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

## Desarrollo

```bash
make build          # Build the operator release (web + TUI + Go binary)
make build-all      # Also build the Rust HNSW sidecar
make run            # Build and run the web operator
make dev            # Auto-rebuild on change (air)
make fmt tidy       # Format and tidy
make help           # List every target
```

Reglas de la casa que conviene conocer antes de abrir un PR:

- Entrega artefactos completos y ejecutables, no diffs.
- Sin stubs, mocks ni fakes salvo en los verdaderos límites externos.
- La UI de cliente separa las capas por contraste de color de fondo, nunca con
  trazos de borde.
- Sin emojis, degradados morados ni efectos de brillo en la UI o en la salida.

Consulta [CONTRIBUTING.md](CONTRIBUTING.md) y
[`spec/ion_spec/ENGINEERING_STANDARDS.md`](spec/ion_spec/ENGINEERING_STANDARDS.md).

## Pruebas y validación

```bash
make test-unit        # Unit tests with the race detector
make vet              # go vet
make verify-deps      # Verify checksummed Go and Rust dependencies
make test-operator    # Shared, web, TUI, browser, accessibility, and budget gates
make spec-validate    # Validate the authoritative spec.kvx
make ci               # Full CI pipeline
```

CI ejecuta las mismas comprobaciones en cada push y pull request en Go, Rust y los
clientes de operador, y rechaza la divergencia del contrato generado y de la
documentación. Consulta [`.github/workflows/`](.github/workflows/).

## Seguridad

Ion es un agente de presencia continua con capacidad de acción autónoma, y su modelo
de seguridad se trata como una superficie de producto de primera clase: ocho clases
de adversario, activos joya de la corona definidos y decisiones vinculantes de
arquitectura de seguridad (SADR). Los subagentes nunca heredan claves del vault, los
principales en tiempo de inactividad no pueden ejecutar operaciones de alto riesgo o
externas, y todas las anulaciones de seguridad quedan registradas y son visibles
para el usuario.

**No reportes vulnerabilidades en issues públicos.** Usa el
[reporte privado de vulnerabilidades de GitHub](https://github.com/paxlabs-inc/ion-agent/security/advisories/new)
y sigue el proceso descrito en [SECURITY.md](SECURITY.md).

## Hoja de ruta

El plan autoritativo y el estado de las tareas viven en
[`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). Se mantiene un resumen legible
para humanos en [ROADMAP.md](ROADMAP.md). Los resúmenes generados son proyecciones
informativas, no el registro autoritativo de tareas.

## Comunidad y soporte

- **Preguntas e ideas** — [GitHub Discussions](https://github.com/paxlabs-inc/ion-agent/discussions)
- **Errores y funcionalidades** — [GitHub Issues](https://github.com/paxlabs-inc/ion-agent/issues)
- **Cómo obtener ayuda** — [SUPPORT.md](SUPPORT.md)
- **Estándares de la comunidad** — [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- **Gobernanza del proyecto** — [GOVERNANCE.md](GOVERNANCE.md)

## Contribuir

Las contribuciones son bienvenidas. Lee [CONTRIBUTING.md](CONTRIBUTING.md), toma un
issue y abre un pull request enfocado. Se espera que todos los colaboradores sigan
el [Código de Conducta](CODE_OF_CONDUCT.md).

## Licencia

Ion es software libre y de código abierto licenciado bajo la [Licencia MIT](LICENSE).

<div align="center">

Copyright © 2026 MatrixMCL — <a href="https://ion.matrixmcl.com">ion.matrixmcl.com</a>

</div>
