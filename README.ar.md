<div align="center">

<img src="assets/ion_brand.png"/>

# Ion

**وكيل واحد دائم. ذاكرة راسخة. تنفيذ محدود. أدلة ظاهرة.**

Ion وكيل عام متقدّم من [MatrixMCL](https://matrixmcl.com) — هوية واحدة دائمة
بذاكرة مشفّرة، وتنفيذ نموذجي محايد تجاه المزوّد، ووصول يتحكّم فيه المشغّل
إلى الأدوات والمشاريع والمتصفّحات والوكلاء المتخصّصين.

[![CI](https://github.com/paxlabs-inc/ion-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/paxlabs-inc/ion-agent/actions/workflows/ci.yml)
[![CodeQL](https://github.com/paxlabs-inc/ion-agent/actions/workflows/codeql.yml/badge.svg)](https://github.com/paxlabs-inc/ion-agent/actions/workflows/codeql.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/paxlabs-inc/ion-agent.svg)](https://pkg.go.dev/github.com/paxlabs-inc/ion-agent)
[![Go Report Card](https://goreportcard.com/badge/github.com/paxlabs-inc/ion-agent)](https://goreportcard.com/report/github.com/paxlabs-inc/ion-agent)
[![License: MIT](https://img.shields.io/badge/License-MIT-informational.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](https://go.dev/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)
[![OpenSSF Best Practices](https://img.shields.io/badge/OpenSSF-best%20practices-informational.svg)](https://www.bestpractices.dev/)

[المنتج](https://ion.matrixmcl.com) ·
[التوثيق](docs/) ·
[البنية المعمارية](ARCHITECTURE.md) ·
[الأمان](SECURITY.md) ·
[المساهمة](CONTRIBUTING.md) ·
[خارطة الطريق](ROADMAP.md) ·
[النقاشات](https://github.com/paxlabs-inc/ion-agent/discussions)

[English](README.md) ·
[简体中文](README.zh-CN.md) ·
[Español](README.es.md) ·
[हिन्दी](README.hi.md) ·
**العربية** ·
[Français](README.fr.md) ·
[Português](README.pt-BR.md)

</div>

---

<div dir="rtl">

> **برمجيات قبل الإصدار.** مشغّل الويب ووقت التشغيل الأساسي هما مسار
> التطوير الرئيسي. لا يزال عميل الطرفية، والمتصفّح المُشرَف عليه، والحاسوب،
> واستوديو البرمجيات خاضعين لحدود القبول المسجّلة في
> [`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). لا يدّعي Ion نجاح أي
> عملية ما لم ينتج مسار الإنتاج دليلًا موثوقًا على النتيجة. تُعرَض الأنظمة
> الفرعية غير المتاحة على أنها غير متاحة بدلًا من تمثيلها ببيانات مُختلَقة.

</div>

## جدول المحتويات

- [لماذا Ion](#لماذا-ion)
- [الميزات](#الميزات)
- [البنية المعمارية](#البنية-المعمارية)
- [البدء السريع](#البدء-السريع)
  - [التشغيل باستخدام Docker](#التشغيل-باستخدام-docker)
  - [البناء من المصدر](#البناء-من-المصدر)
  - [حاوية التطوير](#حاوية-التطوير)
- [التهيئة](#التهيئة)
- [التشغيل](#التشغيل)
- [الإعداد](#الإعداد)
- [النشر](#النشر)
- [بنية المشروع](#بنية-المشروع)
- [التطوير](#التطوير)
- [الاختبار والتحقّق](#الاختبار-والتحقق)
- [الأمان](#الأمان)
- [خارطة الطريق](#خارطة-الطريق)
- [المجتمع والدعم](#المجتمع-والدعم)
- [المساهمة](#المساهمة)
- [الرخصة](#الرخصة)

## لماذا Ion

<div dir="rtl">

معظم أطر عمل الوكلاء عبارة عن مكتبات تجمّعها في عملية تنسى كل شيء عند خروجها.
Ion هو العكس تمامًا: **وقت تشغيل واحد راسخ** يملك الهوية والذاكرة والسياسة
والأدلة، ويعرض تلك السلطة لعملاء مشغّل رقيقين.

- **هوية واحدة، لا جلسات متعددة.** Ion هو فاعل واحد دائم بنموذج ذاتي متواصل،
  وذاكرة راسخة، وسِجلّ تدقيق ثابت — لا سياق جديد لكل طلب.
- **السلطة تقيم في وقت التشغيل.** يملك وقت تشغيل Go السياسة والموافقات
  والحيادية تجاه التكرار (idempotency) والتدقيق. يعرض عميلا الويب والطرفية
  بروتوكول مستوى تحكّم مُولَّدًا ولا يختلقان أبدًا حالة نظام فرعي.
- **الأدلة قبل التفاؤل.** لا يُبلَّغ عن نجاح أي عملية ذات تبعات إلا بعد أن
  ينتج النظام الفرعي الحقيقي دليلًا موثوقًا على النتيجة وتُكتب الحالة الراسخة.
  تُعرَض القدرات غير المتاحة على أنها غير متاحة.
- **محايد تجاه المزوّد بحكم التصميم.** يُجرَّد تنفيذ النموذج خلف طبقة مزوّد
  ذات تراجع صريح ومرتّب وتدوير لبيانات الاعتماد.
- **نطاق تأثير محدود.** يتلقى الوكلاء الفرعيون المتخصّصون سلطة محصورة
  ولا يرثون أبدًا مفاتيح الخزنة. أوقات تشغيل المتصفّح والمشاريع هي عمليات
  محلية مُشرَف عليها ذات مسارات ومنافذ وبيئات وعقود استحواذ محدودة.

</div>

## الميزات

| القدرة | الوصف |
|---|---|
| **الذاكرة والجلسات المشفّرة** | تشفير مظروفي بخوارزمية AES-256-GCM محصور بالفاعل، بتسلسل هرمي KEK ← مفتاح المستخدم ← مفتاح DEK لكل كائن، وتدوير ذرّي، وتصفير المفاتيح عند الإيقاف. |
| **مخزن جلسات راسخ** | SQLite بلغة Go خالصة مع WAL، وطابور كاتب واحد، ومجمّع قرّاء، وترحيلات مضمّنة ذات إصدارات، وجلسات فرعية تُطلَق بالضغط. |
| **تنفيذ محايد تجاه المزوّد** | نموذج طلب/توليد/استدعاء أداة/تدفّق محايد تجاه المزوّد بمحوّلات سلكية مُتحقَّق منها وتراجع مرتّب عند تجاوز حدود المعدّل والإخفاقات. |
| **السياسة والموافقة والتدقيق** | تمرّ الأدوات ذات التبعات عبر حدود السياسة والموافقة البشرية والحيادية تجاه التكرار والتدقيق. لا استجابات تغيير عامة من نوع "مقبول فقط". |
| **العمل الراسخ والجدولة** | يبقى تتبّع العمل والجدولة والاسترداد بعد إعادة التشغيل؛ ودورة حياة المهمة مفصولة عن اتصال المشغّل. |
| **وكلاء متخصّصون محدودون** | سِجلّ من الوكلاء الفرعيين المحصورين بدورة حياة محدودة لا يرثون أبدًا مفاتيح الخزنة. |
| **متصفّح مُشرَف عليه** | جلسات Chromium تُدار عبر بروتوكول Chrome DevTools تحت ضوابط SSRF والشبكات الخاصة. |
| **الاسترجاع الشعاعي** | ملحق Rust HNSW اختياري للبحث بالتشابه عالي الاستدعاء. |
| **مشغّل الويب** | مشغّل React مزوّد بإسقاطات للدردشة والموافقات والجلسات والمزوّدين والذاكرة والأمان والمشاريع والحاسوب واستوديو البرمجيات. |
| **مشغّل الطرفية** | عميل طرفية React Ink مضمّن للإرفاق المحلي والتشغيل المُشرَف عليه. |
| **بروتوكول مُولَّد** | كتالوج مستوى تحكّم واحد بلغة Go يولّد عميل TypeScript المشترك الذي يستخدمه كل مشغّل؛ ويُرفَض أي انحراف في CI. |

## البنية المعمارية

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

<div dir="rtl">

النشر الافتراضي هو عملية `ion` محلية واحدة. يقتصر HTTP العادي على الاسترجاع
الحلقي (loopback)؛ ويتطلّب الوصول عن بُعد وكيلًا عكسيًا بطبقة TLS يديره المشغّل.

تتبع السلطة والتنفيذ مسارًا واحدًا:

1. يقدّم فاعل موثّق طلب مستوى تحكّم.
2. يحلّ تطبيق المشغّل الفاعل والجلسة والقناة والملف الشخصي وسياق الموافقة.
3. تمرّ العمليات ذات التبعات عبر السياسة والموافقة والحيادية تجاه التكرار والتدقيق.
4. ينفّذ وقت التشغيل التطبيق الحقيقي للنظام الفرعي.
5. تُكتب الحالة الراسخة والأدلة **قبل** إسقاط النجاح.
6. يعرض العملاء الحالة الناتجة، بما في ذلك النتائج غير المتاحة والجزئية الصريحة.

للتصميم الكامل، انظر [ARCHITECTURE.md](ARCHITECTURE.md).

</div>

## البدء السريع

### التشغيل باستخدام Docker

<div dir="rtl">

أسرع طريقة لتجربة Ion محليًا. المشغّل مقصور على الاسترجاع الحلقي افتراضيًا.

</div>

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

# Build the image and start the web operator on http://127.0.0.1:4174
docker compose -f docker/docker-compose.yml up --build
```

<div dir="rtl">

افتح <http://127.0.0.1:4174>. انظر [docker/README.md](docker/README.md) لمعرفة
أنواع الصور والوحدات التخزينية ومتغيّرات البيئة.

</div>

### البناء من المصدر

**المتطلّبات**

| الأداة | الإصدار |
|---|---|
| Go | 1.26.5 |
| Node.js | 22.22+ (خط Node 22) |
| npm | 11 |
| Rust | 1.78.0 (خدمة HNSW اختيارية) |
| Chromium | لاختبارات قبول المتصفّح الأصلية |

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

make build
```

<div dir="rtl">

يُضمّن بناء الإصدار منتجات ويب وطرفية حتمية داخل `bin/ion`.

</div>

### حاوية التطوير

<div dir="rtl">

توفّر بيئة جاهزة للبرمجة مثبَّت فيها مسبقًا Go وNode وRust وChromium تحت
[`.devcontainer/`](.devcontainer/). في VS Code، شغّل
**Dev Containers: Reopen in Container**، أو استخدم زر GitHub Codespaces.
يعمل كل شيء من `make build` إلى `make ci` جاهزًا دون إعداد إضافي.

</div>

## التهيئة

<div dir="rtl">

تستخدم تهيئة الإنتاج مصدر المفاتيح المحمي للمضيف:

</div>

```bash
./bin/ion init
```

<div dir="rtl">

قد تختار بيئات التطوير بلا واجهة رأسية التي تفتقر إلى مصدر مفاتيح محمي مدعوم
الاشتراك صراحةً في KEK ملفّي مخصّص للتطوير فقط:

</div>

```bash
./bin/ion init --dev-file-kek
```

<div dir="rtl">

> يجب **ألّا** يُستخدم بديل التطوير كآلية نشر إنتاجية.

</div>

## التشغيل

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

<div dir="rtl">

يقتصر HTTP العادي على الاسترجاع الحلقي. ويتطلّب الوصول عن بُعد وكيلًا عكسيًا
بطبقة TLS يديره المشغّل.

</div>

## الإعداد

<div dir="rtl">

يقرأ Ion دليل بياناته وعنوان الاستماع ومصدر المفاتيح من الرايات ومتغيّرات
البيئة. أكثر المفاتيح شيوعًا:

</div>

| الراية / متغيّر البيئة | الافتراضي | الوصف |
|---|---|---|
| `--data-dir` / `ION_DATA_DIR` | `~/.ion` | دليل البيانات الراسخ (SQLite والخزنة وحالة العمل). |
| `--listen` / `ION_WEB_LISTEN` | `127.0.0.1:4174` | عنوان استماع مشغّل الويب. اربطه بالاسترجاع الحلقي فقط. |
| `--dev-file-kek` | معطّل | KEK ملفّي مخصّص للتطوير فقط. لا تستخدمه أبدًا في الإنتاج. |

<div dir="rtl">

انظر [docs/configuration.md](docs/configuration.md) للمرجع الكامل.

</div>

## النشر

<div dir="rtl">

يوفّر Ion أصول نشر موجّهة للإنتاج تحت [`deploy/`](deploy/):

- **Docker Compose** — [`deploy/compose/`](deploy/compose/) لمشغّل بمضيف واحد
  خلف وكيل عكسي بطبقة TLS.
- **Kubernetes** — ملفّات [`deploy/kubernetes/`](deploy/kubernetes/) البيانية
  (namespace وdeployment وservice وconfig وingress) بقاعدة Kustomize.
- **Helm** — مخطط [`deploy/helm/ion/`](deploy/helm/ion/) للتثبيتات ذات
  الوسائط.
- **systemd** — وحدة [`deploy/systemd/`](deploy/systemd/) للمضيفات على العتاد
  المباشر.

اقرأ [docs/deployment.md](docs/deployment.md) و[deploy/README.md](deploy/README.md)
قبل تعريض Ion خارج الاسترجاع الحلقي. إنهاء TLS ومصدر المفاتيح وضوابط الصادر
الشبكي هي مسؤوليات المشغّل.

</div>

## بنية المشروع

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

## التطوير

```bash
make build          # Build the operator release (web + TUI + Go binary)
make build-all      # Also build the Rust HNSW sidecar
make run            # Build and run the web operator
make dev            # Auto-rebuild on change (air)
make fmt tidy       # Format and tidy
make help           # List every target
```

<div dir="rtl">

قواعد الدار الجديرة بالمعرفة قبل فتح طلب سحب:

- سلّم منتجات كاملة قابلة للتشغيل — لا فروقات (diffs).
- لا نماذج بديلة (stubs) أو محاكاة (mocks) أو تزييف إلا عند الحدود الخارجية الحقيقية.
- تفصل واجهة العميل بين الطبقات بتباين لون الخلفية، لا بحدود الحدّ (border strokes) أبدًا.
- لا رموز تعبيرية، ولا تدرّجات بنفسجية، ولا تأثيرات توهّج في الواجهة أو المخرجات.

انظر [CONTRIBUTING.md](CONTRIBUTING.md) و
[`spec/ion_spec/ENGINEERING_STANDARDS.md`](spec/ion_spec/ENGINEERING_STANDARDS.md).

</div>

## الاختبار والتحقّق

```bash
make test-unit        # Unit tests with the race detector
make vet              # go vet
make verify-deps      # Verify checksummed Go and Rust dependencies
make test-operator    # Shared, web, TUI, browser, accessibility, and budget gates
make spec-validate    # Validate the authoritative spec.kvx
make ci               # Full CI pipeline
```

<div dir="rtl">

يشغّل CI البوّابات نفسها عند كل دفع وطلب سحب عبر Go وRust وعملاء المشغّل،
ويرفض انحراف العقد المُولَّد والتوثيق. انظر
[`.github/workflows/`](.github/workflows/).

</div>

## الأمان

<div dir="rtl">

Ion وكيل ذو حضور متواصل بقدرة على الفعل الذاتي، ويُعامَل نموذجه الأمني بوصفه
سطح منتج من الدرجة الأولى: ثماني فئات من الخصوم، وأصول جواهر تاج محدّدة،
وقرارات بنية أمنية مُلزِمة (SADRs). لا يرث الوكلاء الفرعيون أبدًا مفاتيح الخزنة،
ولا يمكن للفاعلين في وقت الخمول تنفيذ عمليات عالية المخاطر أو خارجية، وتُسجَّل
كل تجاوزات الأمان وتكون ظاهرة للمستخدم.

**لا تُبلِّغ عن الثغرات في القضايا العامة.** استخدم
[التبليغ الخاص عن الثغرات في GitHub](https://github.com/paxlabs-inc/ion-agent/security/advisories/new)
واتبع العملية الواردة في [SECURITY.md](SECURITY.md).

</div>

## خارطة الطريق

<div dir="rtl">

تقيم الخطة الموثوقة وحالة المهام في
[`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). ويُصان ملخّص مقروء بشريًا في
[ROADMAP.md](ROADMAP.md). الملخّصات المُولَّدة هي إسقاطات إعلامية، لا سِجلّ المهام
الموثوق.

</div>

## المجتمع والدعم

<div dir="rtl">

- **الأسئلة والأفكار** — [نقاشات GitHub](https://github.com/paxlabs-inc/ion-agent/discussions)
- **العلل والميزات** — [قضايا GitHub](https://github.com/paxlabs-inc/ion-agent/issues)
- **كيفية الحصول على المساعدة** — [SUPPORT.md](SUPPORT.md)
- **معايير المجتمع** — [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- **حوكمة المشروع** — [GOVERNANCE.md](GOVERNANCE.md)

</div>

## المساهمة

<div dir="rtl">

المساهمات مُرحَّب بها. اقرأ [CONTRIBUTING.md](CONTRIBUTING.md)، والتقط قضية،
وافتح طلب سحب مركّزًا. يُتوقَّع من جميع المساهمين اتّباع
[مدوّنة السلوك](CODE_OF_CONDUCT.md).

</div>

## الرخصة

<div dir="rtl">

Ion برمجيات حرّة ومفتوحة المصدر مُرخّصة بموجب [رخصة MIT](LICENSE).

</div>

<div align="center">

Copyright © 2026 MatrixMCL — <a href="https://ion.matrixmcl.com">ion.matrixmcl.com</a>

</div>
