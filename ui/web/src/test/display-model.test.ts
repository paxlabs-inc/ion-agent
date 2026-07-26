import {
  DISPLAY_MODEL_VERSION,
  displayModelCompatibility,
  isDisplayModel,
  migrateDisplayModel,
  type DisplayModel,
} from '@matrixmcl/ion-shared'
import { describe, expect, it } from 'vitest'

const current: DisplayModel = {
  protocol_version: DISPLAY_MODEL_VERSION,
  kind: 'code',
  title: {
    value: 'src/main.ts',
    truth: 'observed',
    format: 'path',
    sources: [2],
  },
  fields: [{
    label: 'Truncated',
    value: {
      value: 'false',
      truth: 'observed',
      format: 'boolean',
      sources: [1],
    },
  }],
  blocks: [{
    kind: 'code',
    language: 'ts',
    content: {
      value: 'export const ready = true',
      truth: 'observed',
      format: 'code',
      sources: [1],
    },
  }],
}

describe('display model contract', () => {
  it('validates current source-linked models deterministically', () => {
    expect(isDisplayModel(current, 3)).toBe(true)
    expect(displayModelCompatibility(current, 3)).toBe('current')
    expect(migrateDisplayModel(current, 3)).toEqual(current)
  })

  it('rejects unsafe markup, terminal control data, and broken evidence links', () => {
    expect(isDisplayModel({
      ...current,
      blocks: [{
        kind: 'terminal',
        content: {
          value: '\u001b[31munsafe',
          truth: 'observed',
          format: 'terminal',
          sources: [1],
        },
      }],
    }, 3)).toBe(false)
    expect(isDisplayModel({
      ...current,
      title: {
        value: '<script>unsafe</script>',
        truth: 'observed',
        format: 'text',
        sources: [1],
      },
    }, 3)).toBe(false)
    expect(isDisplayModel({
      ...current,
      title: {
        ...current.title,
        sources: [3],
      },
    }, 3)).toBe(false)
    expect(isDisplayModel({
      ...current,
      fields: [{
        label: 'URL',
        value: {
          value: 'https://example.com/read?token=topsecret',
          truth: 'observed',
          format: 'url',
          sources: [1],
        },
      }],
    }, 3)).toBe(false)
  })

  it('migrates the known historical schema and isolates unknown versions', () => {
    const legacy = {
      protocol_version: 'ion.display-model.v0',
      kind: 'document',
      title: 'Historical brief',
      summary: 'Retained evidence',
      source: 0,
    }
    expect(displayModelCompatibility(legacy, 2)).toBe('migrated')
    expect(migrateDisplayModel(legacy, 2)).toMatchObject({
      protocol_version: DISPLAY_MODEL_VERSION,
      kind: 'document',
      title: {
        value: 'Historical brief',
        truth: 'observed',
        sources: [0],
      },
      blocks: [{
        kind: 'text',
        content: {
          value: 'Retained evidence',
          truth: 'summarized',
          sources: [0],
        },
      }],
    })
    const future = {
      protocol_version: 'ion.display-model.v99',
      kind: 'future-native-view',
      opaque: { must_not_be_reinterpreted: true },
    }
    expect(displayModelCompatibility(future, 2)).toBe('unsupported')
    expect(migrateDisplayModel(future, 2)).toBeUndefined()
  })

  it('enforces the encoded model bound', () => {
    const oversized = {
      ...current,
      title: {
        ...current.title,
        value: 'x'.repeat(65_537),
      },
    }
    expect(isDisplayModel(oversized, 3)).toBe(false)
    expect(displayModelCompatibility(oversized, 3)).toBe('invalid')
  })
})
