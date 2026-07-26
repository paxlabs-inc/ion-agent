import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  type ChangeEvent,
  type Dispatch,
  type FormEvent,
  type SetStateAction,
  useEffect,
  useMemo,
  useState,
} from 'react'
import { Icon } from '../components/Icon'

type MediaKind =
  | 'text-to-image'
  | 'image-to-image'
  | 'text-to-video'
  | 'image-to-video'
  | 'inpainting'
  | 'cleanup'
  | 'remove-background'
  | 'replace-background'
  | 'remove-text'
  | 'merge-face'
  | 'upscale'

type MediaStatus = 'queued' | 'submitting' | 'running' | 'succeeded' | 'failed'

interface MediaProviderStatus {
  configured: boolean
  provider: string
  message: string
}

interface MediaRequest {
  kind: MediaKind
  prompt?: string
  negative_prompt?: string
  model?: string
  sampler?: string
  width?: number
  height?: number
  steps?: number
  guidance?: number
  seed: number
  image_count?: number
  strength?: number
  frames?: number
  fps?: number
  scale?: number
  resize_mode?: string
  image_name?: string
  mask_name?: string
  face_name?: string
  has_image: boolean
  has_mask: boolean
  has_face: boolean
}

interface MediaAsset {
  id: string
  job_id: string
  media_type: 'image' | 'video'
  mime_type: string
  name: string
  size: number
  url: string
}

interface MediaJob {
  id: string
  kind: MediaKind
  status: MediaStatus
  provider: string
  provider_task_id?: string
  progress: number
  error?: string
  request: MediaRequest
  assets: MediaAsset[]
  created_at: string
  updated_at: string
  completed_at?: string
}

interface StudioForm {
  kind: MediaKind
  prompt: string
  negative_prompt: string
  model: string
  sampler: string
  width: number
  height: number
  steps: number
  guidance: number
  seed: number
  image_count: number
  strength: number
  frames: number
  fps: number
  scale: number
  resize_mode: string
  image_base64: string
  mask_base64: string
  face_base64: string
  image_name: string
  mask_name: string
  face_name: string
}

type NumericFormField =
  | 'width'
  | 'height'
  | 'steps'
  | 'guidance'
  | 'seed'
  | 'image_count'
  | 'strength'
  | 'frames'
  | 'fps'
  | 'scale'

interface Workflow {
  kind: MediaKind
  label: string
  detail: string
}

const workflows: ReadonlyArray<{
  title: string
  items: readonly Workflow[]
}> = [
  {
    title: 'Create images',
    items: [
      { kind: 'text-to-image', label: 'Text to image', detail: 'Create from a written direction' },
      { kind: 'image-to-image', label: 'Image to image', detail: 'Restyle or transform a source' },
      { kind: 'inpainting', label: 'Inpaint', detail: 'Replace a masked region' },
    ],
  },
  {
    title: 'Create video',
    items: [
      { kind: 'text-to-video', label: 'Text to video', detail: 'Generate a short motion sequence' },
      { kind: 'image-to-video', label: 'Image to video', detail: 'Animate a still image' },
    ],
  },
  {
    title: 'Edit images',
    items: [
      { kind: 'cleanup', label: 'Clean up', detail: 'Erase a masked object' },
      { kind: 'remove-background', label: 'Remove background', detail: 'Create a clean cutout' },
      { kind: 'replace-background', label: 'Replace background', detail: 'Generate a new setting' },
      { kind: 'remove-text', label: 'Remove text', detail: 'Clear detected lettering' },
      { kind: 'merge-face', label: 'Merge face', detail: 'Apply a face reference' },
      { kind: 'upscale', label: 'Upscale', detail: 'Increase image resolution' },
    ],
  },
]

const workflowByKind = new Map(
  workflows.flatMap((group) => group.items).map((workflow) => [workflow.kind, workflow]),
)

export function MediaStudio() {
  const queryClient = useQueryClient()
  const [form, setForm] = useState<StudioForm>(() => defaultForm('text-to-image'))
  const [selectedID, setSelectedID] = useState<string>()
  const [deleteArmedID, setDeleteArmedID] = useState<string>()
  const [notice, setNotice] = useState('')
  const status = useQuery({
    queryKey: ['media.status'],
    queryFn: () => mediaFetch<MediaProviderStatus>('/v1/media/status'),
    retry: false,
    staleTime: 30_000,
  })
  const jobs = useQuery({
    queryKey: ['media.jobs'],
    queryFn: () => mediaFetch<MediaJob[]>('/v1/media/jobs'),
    retry: false,
    refetchInterval: (query) => {
      const items = query.state.data
      return items?.some((job) => activeStatus(job.status)) === true ? 2_000 : false
    },
  })
  const selected = useMemo(
    () => jobs.data?.find((job) => job.id === selectedID) ?? jobs.data?.[0],
    [jobs.data, selectedID],
  )
  const create = useMutation({
    mutationFn: (input: StudioForm) => mediaMutation<MediaJob>('/v1/media/jobs', 'POST', input),
    onSuccess: async (job) => {
      setSelectedID(job.id)
      setNotice('Generation started. You can leave this page while Ion keeps the job running.')
      await queryClient.invalidateQueries({ queryKey: ['media.jobs'] })
    },
    onError: (error) => setNotice(error instanceof Error ? error.message : String(error)),
  })
  const remove = useMutation({
    mutationFn: (jobID: string) => mediaMutation<void>(`/v1/media/jobs/${jobID}`, 'DELETE'),
    onSuccess: async () => {
      setSelectedID(undefined)
      setDeleteArmedID(undefined)
      setNotice('The generation and its stored outputs were removed.')
      await queryClient.invalidateQueries({ queryKey: ['media.jobs'] })
    },
    onError: (error) => setNotice(error instanceof Error ? error.message : String(error)),
  })

  useEffect(() => {
    if (selectedID === undefined && jobs.data?.[0] !== undefined) {
      setSelectedID(jobs.data[0].id)
    }
  }, [jobs.data, selectedID])

  const chooseWorkflow = (kind: MediaKind) => {
    setForm(defaultForm(kind))
    setNotice('')
  }
  const useSettings = (job: MediaJob) => {
    const next = defaultForm(job.kind)
    setForm({
      ...next,
      prompt: job.request.prompt ?? '',
      negative_prompt: job.request.negative_prompt ?? '',
      model: job.request.model ?? next.model,
      sampler: job.request.sampler ?? next.sampler,
      width: job.request.width ?? next.width,
      height: job.request.height ?? next.height,
      steps: job.request.steps ?? next.steps,
      guidance: job.request.guidance ?? next.guidance,
      seed: job.request.seed,
      image_count: job.request.image_count ?? next.image_count,
      strength: job.request.strength ?? next.strength,
      frames: job.request.frames ?? next.frames,
      fps: job.request.fps ?? next.fps,
      scale: job.request.scale ?? next.scale,
      resize_mode: job.request.resize_mode ?? next.resize_mode,
    })
    setNotice(job.request.has_image
      ? 'Settings restored. Re-add source images before generating.'
      : 'Settings restored and ready to generate again.')
  }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    setNotice('')
    create.mutate(form)
  }

  return (
    <section className="media-studio" aria-labelledby="media-studio-title">
      <header className="media-studio-header">
        <div>
          <p className="eyebrow">Creative workspace</p>
          <h1 id="media-studio-title">Image &amp; Video Studio</h1>
          <p>Create, transform, and keep media in one durable workspace.</p>
        </div>
        <div className="media-provider-state" data-ready={status.data?.configured === true}>
          <span aria-hidden="true" />
          <div>
            <strong>{status.data?.provider ?? 'Novita'}</strong>
            <small>{status.isLoading ? 'Checking connection' : status.data?.message ?? 'Connection unavailable'}</small>
          </div>
        </div>
      </header>

      <div className="media-studio-layout">
        <aside className="media-workflow-rail" aria-label="Media workflows">
          {workflows.map((group) => (
            <section key={group.title}>
              <h2>{group.title}</h2>
              {group.items.map((workflow) => (
                <button
                  aria-pressed={form.kind === workflow.kind}
                  key={workflow.kind}
                  onClick={() => chooseWorkflow(workflow.kind)}
                  type="button"
                >
                  <span>{workflow.label}</span>
                  <small>{workflow.detail}</small>
                </button>
              ))}
            </section>
          ))}
        </aside>

        <main className="media-stage">
          <div className="media-stage-toolbar">
            <div>
              <span>Output</span>
              <strong>{selected === undefined
                ? workflowByKind.get(form.kind)?.label
                : workflowByKind.get(selected.kind)?.label}</strong>
            </div>
            {selected !== undefined ? (
              <div className={`media-status status-${selected.status}`}>
                <i aria-hidden="true" />
                {statusLabel(selected)}
              </div>
            ) : null}
          </div>
          <MediaCanvas job={selected} />
          {selected !== undefined && activeStatus(selected.status) ? (
            <div className="media-progress" aria-label={`${selected.progress}% complete`}>
              <span style={{ width: `${String(Math.max(4, selected.progress))}%` }} />
            </div>
          ) : null}
          <section className="media-library" aria-labelledby="media-library-title">
            <div className="media-library-heading">
              <div>
                <h2 id="media-library-title">Recent generations</h2>
                <span>{jobs.data?.length ?? 0} saved jobs</span>
              </div>
              {jobs.isFetching ? <small>Refreshing</small> : null}
            </div>
            {jobs.isLoading ? (
              <div className="media-library-empty">Loading the media library…</div>
            ) : jobs.data === undefined || jobs.data.length === 0 ? (
              <div className="media-library-empty">
                Your generated images and videos will stay available here.
              </div>
            ) : (
              <div className="media-history-strip">
                {jobs.data.map((job) => (
                  <button
                    aria-current={selected?.id === job.id ? 'true' : undefined}
                    key={job.id}
                    onClick={() => setSelectedID(job.id)}
                    type="button"
                  >
                    <MediaThumbnail job={job} />
                    <span>{workflowByKind.get(job.kind)?.label}</span>
                    <small>{relativeTime(job.updated_at)}</small>
                  </button>
                ))}
              </div>
            )}
          </section>
        </main>

        <aside className="media-control-panel">
          <form onSubmit={submit}>
            <div className="media-control-heading">
              <div>
                <p className="eyebrow">Direction</p>
                <h2>{workflowByKind.get(form.kind)?.label}</h2>
              </div>
              <span>Novita</span>
            </div>
            <MediaControls form={form} setForm={setForm} />
            {notice !== '' ? (
              <p className={create.isError || remove.isError ? 'media-form-notice danger' : 'media-form-notice'} role="status">
                {notice}
              </p>
            ) : null}
            <button
              className="media-generate-button"
              disabled={create.isPending || status.data?.configured !== true}
              type="submit"
            >
              <Icon name="spark" />
              {create.isPending ? 'Starting…' : actionLabel(form.kind)}
            </button>
            {status.data?.configured === false ? (
              <p className="media-configuration-note">
                Set <code>NOVITA_API_KEY</code> in Ion’s protected server environment. The key is never sent to this page.
              </p>
            ) : null}
          </form>
          {selected !== undefined ? (
            <section className="media-job-details">
              <div>
                <h2>Selected job</h2>
                <span>{formatDate(selected.created_at)}</span>
              </div>
              <dl>
                <div><dt>Provider</dt><dd>{selected.provider}</dd></div>
                <div><dt>Seed</dt><dd>{selected.request.seed}</dd></div>
                {selected.request.model !== undefined ? (
                  <div><dt>Model</dt><dd title={selected.request.model}>{selected.request.model}</dd></div>
                ) : null}
              </dl>
              <div className="media-job-actions">
                <button onClick={() => useSettings(selected)} type="button">Use settings</button>
                <button
                  className="danger-button"
                  disabled={activeStatus(selected.status) || remove.isPending}
                  onClick={() => {
                    if (deleteArmedID === selected.id) {
                      remove.mutate(selected.id)
                    } else {
                      setDeleteArmedID(selected.id)
                      setNotice('Select Confirm delete to permanently remove this job and its stored outputs.')
                    }
                  }}
                  type="button"
                >
                  {deleteArmedID === selected.id ? 'Confirm delete' : 'Delete'}
                </button>
              </div>
            </section>
          ) : null}
        </aside>
      </div>
    </section>
  )
}

function MediaCanvas({ job }: { job: MediaJob | undefined }) {
  if (job === undefined) {
    return (
      <div className="media-canvas-empty">
        <Icon name="spark" />
        <strong>Start with a direction</strong>
        <p>Choose a workflow, shape the details, and Ion will keep the result in your library.</p>
      </div>
    )
  }
  if (job.status === 'failed') {
    return (
      <div className="media-canvas-empty media-canvas-failed">
        <Icon name="activity" />
        <strong>Generation stopped</strong>
        <p>{job.error ?? 'The provider could not complete this media job.'}</p>
      </div>
    )
  }
  if (job.assets.length === 0) {
    return (
      <div className="media-canvas-empty media-canvas-working">
        <span className="media-orbit" aria-hidden="true" />
        <strong>{statusLabel(job)}</strong>
        <p>Your job is durable. You can switch tasks or close this page while it continues.</p>
      </div>
    )
  }
  return (
    <div className="media-output-grid" data-count={job.assets.length}>
      {job.assets.map((asset) => (
        <figure key={asset.id}>
          {asset.media_type === 'video' ? (
            <video controls playsInline preload="metadata" src={asset.url}>
              <track kind="captions" />
            </video>
          ) : (
            <img alt={job.request.prompt === undefined ? 'Generated media output' : `Generated output for ${job.request.prompt}`} src={asset.url} />
          )}
          <figcaption>
            <span>{formatBytes(asset.size)}</span>
            <a href={`${asset.url}?download=1`}>
              Download
            </a>
          </figcaption>
        </figure>
      ))}
    </div>
  )
}

function MediaThumbnail({ job }: { job: MediaJob }) {
  const asset = job.assets[0]
  if (asset === undefined) {
    return (
      <span className={`media-history-placeholder status-${job.status}`}>
        {activeStatus(job.status) ? `${String(job.progress)}%` : <Icon name="activity" />}
      </span>
    )
  }
  if (asset.media_type === 'video') {
    return (
      <span className="media-history-placeholder media-history-video">
        <Icon name="activity" />
        <small>Video</small>
      </span>
    )
  }
  return <img alt="" src={asset.url} />
}

function MediaControls({
  form,
  setForm,
}: {
  form: StudioForm
  setForm: Dispatch<SetStateAction<StudioForm>>
}) {
  const needsImage = form.kind !== 'text-to-image' && form.kind !== 'text-to-video'
  const needsMask = form.kind === 'cleanup' || form.kind === 'inpainting'
  const needsFace = form.kind === 'merge-face'
  const needsPrompt = [
    'text-to-image', 'image-to-image', 'text-to-video', 'replace-background', 'inpainting',
  ].includes(form.kind)
  const advancedGeneration = [
    'text-to-image', 'image-to-image', 'text-to-video', 'image-to-video', 'inpainting',
  ].includes(form.kind)
  const imageGeneration = [
    'text-to-image', 'image-to-image', 'inpainting',
  ].includes(form.kind)

  return (
    <div className="media-controls">
      {needsImage ? (
        <MediaUpload
          key={`${form.kind}-source`}
          label="Source image"
          name={form.image_name}
          onChange={(encoded, name) => setForm((current) => ({
            ...current, image_base64: encoded, image_name: name,
          }))}
        />
      ) : null}
      {needsMask ? (
        <MediaUpload
          key={`${form.kind}-mask`}
          label="Mask"
          name={form.mask_name}
          onChange={(encoded, name) => setForm((current) => ({
            ...current, mask_base64: encoded, mask_name: name,
          }))}
        />
      ) : null}
      {needsFace ? (
        <MediaUpload
          key={`${form.kind}-face`}
          label="Face reference"
          name={form.face_name}
          onChange={(encoded, name) => setForm((current) => ({
            ...current, face_base64: encoded, face_name: name,
          }))}
        />
      ) : null}
      {needsPrompt ? (
        <label className="media-prompt-field">
          <span>{form.kind === 'replace-background' ? 'New background' : 'Prompt'}</span>
          <textarea
            maxLength={2048}
            onChange={(event) => setForm((current) => ({ ...current, prompt: event.target.value }))}
            placeholder={promptPlaceholder(form.kind)}
            required
            rows={5}
            value={form.prompt}
          />
        </label>
      ) : null}
      {imageGeneration || form.kind === 'text-to-video' ? (
        <label>
          <span>Negative prompt <small>Optional</small></span>
          <textarea
            maxLength={2048}
            onChange={(event) => setForm((current) => ({ ...current, negative_prompt: event.target.value }))}
            placeholder="Elements, artifacts, or styles to avoid"
            rows={2}
            value={form.negative_prompt}
          />
        </label>
      ) : null}
      {advancedGeneration || form.kind === 'upscale' ? (
        <label>
          <span>Model</span>
          {form.kind === 'image-to-video' ? (
            <select
              onChange={(event) => setForm((current) => ({
                ...current,
                model: event.target.value,
                frames: event.target.value === 'SVD' ? 14 : 25,
              }))}
              value={form.model}
            >
              <option value="SVD-XT">SVD-XT · 25 frames</option>
              <option value="SVD">SVD · 14 frames</option>
            </select>
          ) : (
            <input
              maxLength={256}
              onChange={(event) => setForm((current) => ({ ...current, model: event.target.value }))}
              required
              value={form.model}
            />
          )}
        </label>
      ) : null}
      {advancedGeneration ? (
        <>
          <div className="media-control-row">
            <NumberField label="Width" max={2048} min={128} setForm={setForm} value={form.width} field="width" />
            <NumberField label="Height" max={2048} min={128} setForm={setForm} value={form.height} field="height" />
          </div>
          <div className="media-control-row">
            <NumberField label="Steps" max={100} min={1} setForm={setForm} value={form.steps} field="steps" />
            <NumberField label="Guidance" max={30} min={1} step={0.5} setForm={setForm} value={form.guidance} field="guidance" />
          </div>
          <NumberField label="Seed" max={2147483647} min={-1} setForm={setForm} value={form.seed} field="seed" />
        </>
      ) : null}
      {imageGeneration ? (
        <div className="media-control-row">
          <NumberField label="Outputs" max={4} min={1} setForm={setForm} value={form.image_count} field="image_count" />
          {form.kind !== 'text-to-image' ? (
            <NumberField label="Strength" max={1} min={0.05} step={0.05} setForm={setForm} value={form.strength} field="strength" />
          ) : (
            <label>
              <span>Sampler</span>
              <select onChange={(event) => setForm((current) => ({ ...current, sampler: event.target.value }))} value={form.sampler}>
                <option>DPM++ 2M Karras</option>
                <option>DPM++ 2S a Karras</option>
                <option>Euler a</option>
                <option>UniPC</option>
              </select>
            </label>
          )}
        </div>
      ) : null}
      {form.kind === 'text-to-video' || form.kind === 'image-to-video' ? (
        <div className="media-control-row">
          <NumberField
            label="Frames"
            max={form.kind === 'image-to-video' ? form.frames : 64}
            min={form.kind === 'image-to-video' ? form.frames : 8}
            setForm={setForm}
            value={form.frames}
            field="frames"
          />
          <NumberField
            label="FPS"
            max={form.kind === 'image-to-video' ? 6 : 24}
            min={form.kind === 'image-to-video' ? 6 : 1}
            setForm={setForm}
            value={form.fps}
            field="fps"
          />
        </div>
      ) : null}
      {form.kind === 'image-to-video' ? (
        <label>
          <span>Image fit</span>
          <select onChange={(event) => setForm((current) => ({ ...current, resize_mode: event.target.value }))} value={form.resize_mode}>
            <option value="ORIGINAL_RESOLUTION">Keep original framing</option>
            <option value="CROP_TO_ASPECT_RATIO">Crop to video frame</option>
          </select>
        </label>
      ) : null}
      {form.kind === 'upscale' ? (
        <NumberField label="Scale factor" max={4} min={1.1} step={0.1} setForm={setForm} value={form.scale} field="scale" />
      ) : null}
    </div>
  )
}

function NumberField({
  label,
  value,
  field,
  min,
  max,
  step = 1,
  setForm,
}: {
  label: string
  value: number
  field: NumericFormField
  min: number
  max: number
  step?: number
  setForm: Dispatch<SetStateAction<StudioForm>>
}) {
  return (
    <label>
      <span>{label}</span>
      <input
        max={max}
        min={min}
        onChange={(event) => setForm((current) => ({
          ...current,
          [field]: Number(event.target.value),
        }))}
        step={step}
        type="number"
        value={value}
      />
    </label>
  )
}

function MediaUpload({
  label,
  name,
  onChange,
}: {
  label: string
  name: string
  onChange(encoded: string, name: string): void
}) {
  const [error, setError] = useState('')
  const [preview, setPreview] = useState('')
  const choose = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    if (file === undefined) return
    try {
      const image = await readImage(file)
      setError('')
      setPreview(image.preview)
      onChange(image.encoded, file.name)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason))
    }
  }
  return (
    <label className="media-upload">
      <input accept="image/jpeg,image/png,image/webp" onChange={(event) => { void choose(event) }} type="file" />
      {preview === '' ? <Icon name={name === '' ? 'plus' : 'check'} /> : <img alt="" src={preview} />}
      <span>{name === '' ? label : name}</span>
      <small>{error === '' ? (name === '' ? 'PNG, JPEG, or WebP · up to 30 MB' : 'Choose another file') : error}</small>
    </label>
  )
}

function defaultForm(kind: MediaKind): StudioForm {
  const video = kind === 'text-to-video' || kind === 'image-to-video'
  const model = kind === 'text-to-video'
    ? 'darkSushiMixMix_225D_64380.safetensors'
    : kind === 'image-to-video'
      ? 'SVD-XT'
      : kind === 'upscale'
        ? 'RealESRNet_x4plus'
        : kind === 'inpainting'
          ? 'realisticVisionV51_v51VAE-inpainting_94324.safetensors'
          : 'sd_xl_base_1.0.safetensors'
  return {
    kind,
    prompt: '',
    negative_prompt: '',
    model,
    sampler: 'DPM++ 2M Karras',
    width: video ? 640 : 1024,
    height: video ? 480 : 1024,
    steps: 20,
    guidance: 7.5,
    seed: -1,
    image_count: 1,
    strength: 0.7,
    frames: kind === 'image-to-video' ? 25 : 16,
    fps: 6,
    scale: 2,
    resize_mode: 'ORIGINAL_RESOLUTION',
    image_base64: '',
    mask_base64: '',
    face_base64: '',
    image_name: '',
    mask_name: '',
    face_name: '',
  }
}

async function mediaFetch<T>(path: string): Promise<T> {
  const response = await fetch(path, { credentials: 'same-origin', headers: { Accept: 'application/json' } })
  const body = await response.json() as T & { error?: string }
  if (!response.ok) throw new Error(body.error ?? 'The media service is unavailable.')
  return body
}

async function mediaMutation<T>(
  path: string,
  method: 'POST' | 'DELETE',
  body?: unknown,
): Promise<T> {
  const response = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
      'X-Ion-CSRF': readCookie('__Host-ion_csrf'),
      ...(method === 'POST' ? { 'X-Ion-Idempotency-Key': crypto.randomUUID() } : {}),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  })
  if (response.status === 204) return undefined as T
  const result = await response.json() as T & { error?: string }
  if (!response.ok) throw new Error(result.error ?? 'The media request could not be completed.')
  return result
}

function readCookie(name: string): string {
  const prefix = `${name}=`
  for (const value of document.cookie.split(';')) {
    const trimmed = value.trim()
    if (trimmed.startsWith(prefix)) return decodeURIComponent(trimmed.slice(prefix.length))
  }
  return ''
}

async function readImage(file: File): Promise<{ encoded: string; preview: string }> {
  if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type)) {
    throw new Error('Choose a PNG, JPEG, or WebP image.')
  }
  if (file.size > 30 * 1024 * 1024) throw new Error('The image must be 30 MB or smaller.')
  const result = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(new Error('The image could not be read.'))
    reader.onload = () => resolve(String(reader.result))
    reader.readAsDataURL(file)
  })
  return { encoded: result.slice(result.indexOf(',') + 1), preview: result }
}

function activeStatus(status: MediaStatus): boolean {
  return status === 'queued' || status === 'submitting' || status === 'running'
}

function statusLabel(job: MediaJob): string {
  switch (job.status) {
  case 'queued': return 'Queued'
  case 'submitting': return 'Sending to Novita'
  case 'running': return job.progress > 5 ? `Generating · ${String(job.progress)}%` : 'Generating'
  case 'succeeded': return 'Ready'
  case 'failed': return 'Failed'
  }
}

function actionLabel(kind: MediaKind): string {
  if (kind === 'text-to-video' || kind === 'image-to-video') return 'Generate video'
  if (['cleanup', 'remove-background', 'replace-background', 'remove-text', 'merge-face', 'upscale', 'inpainting'].includes(kind)) return 'Apply edit'
  return 'Generate images'
}

function promptPlaceholder(kind: MediaKind): string {
  switch (kind) {
  case 'replace-background': return 'A quiet sunlit gallery with pale stone walls…'
  case 'inpainting': return 'Describe what should appear inside the masked area…'
  case 'image-to-image': return 'Transform the source into a cinematic editorial photograph…'
  case 'text-to-video': return 'A slow tracking shot through a rain-lit city at night…'
  default: return 'Describe the subject, composition, light, mood, and finish…'
  }
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium', timeStyle: 'short',
  }).format(new Date(value))
}

function relativeTime(value: string): string {
  const seconds = Math.round((new Date(value).getTime() - Date.now()) / 1000)
  const absolute = Math.abs(seconds)
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })
  if (absolute < 60) return formatter.format(seconds, 'second')
  if (absolute < 3600) return formatter.format(Math.round(seconds / 60), 'minute')
  if (absolute < 86400) return formatter.format(Math.round(seconds / 3600), 'hour')
  return formatter.format(Math.round(seconds / 86400), 'day')
}

function formatBytes(value: number): string {
  if (value < 1024) return `${String(value)} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / (1024 * 1024)).toFixed(1)} MB`
}
