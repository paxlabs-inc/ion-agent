import {
  type KeyboardEvent,
  type PointerEvent,
  type RefObject,
  useRef,
} from 'react'

interface PanelResizeHandleProps {
  containerRef: RefObject<HTMLElement | null>
  defaultValue: number
  direction: 'from-left' | 'from-right'
  label: string
  max: number
  min: number
  onChange(value: number): void
  onResizeStateChange?(resizing: boolean): void
  oppositeMinimum: number
  value: number
}

const handleWidth = 7
const keyboardStep = 24

export function PanelResizeHandle({
  containerRef,
  defaultValue,
  direction,
  label,
  max,
  min,
  onChange,
  onResizeStateChange,
  oppositeMinimum,
  value,
}: PanelResizeHandleProps) {
  const activePointer = useRef<number | undefined>(undefined)

  const effectiveMaximum = () => {
    const width = containerRef.current?.getBoundingClientRect().width
    if (width === undefined) return max
    return Math.max(min, Math.min(max, width - oppositeMinimum - handleWidth))
  }
  const update = (next: number) => {
    onChange(Math.round(Math.max(min, Math.min(effectiveMaximum(), next))))
  }
  const valueFromPointer = (clientX: number) => {
    const bounds = containerRef.current?.getBoundingClientRect()
    if (bounds === undefined) return value
    return direction === 'from-left'
      ? clientX - bounds.left
      : bounds.right - clientX
  }
  const finishResize = (event: PointerEvent<HTMLDivElement>) => {
    if (activePointer.current !== event.pointerId) return
    activePointer.current = undefined
    onResizeStateChange?.(false)
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId)
    }
  }
  const pointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (event.pointerType === 'mouse' && event.button !== 0) return
    event.preventDefault()
    activePointer.current = event.pointerId
    event.currentTarget.setPointerCapture(event.pointerId)
    onResizeStateChange?.(true)
    update(valueFromPointer(event.clientX))
  }
  const pointerMove = (event: PointerEvent<HTMLDivElement>) => {
    if (activePointer.current !== event.pointerId) return
    update(valueFromPointer(event.clientX))
  }
  const keyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (
      event.key !== 'ArrowLeft' &&
      event.key !== 'ArrowRight' &&
      event.key !== 'Home' &&
      event.key !== 'End'
    ) return
    event.preventDefault()
    if (event.key === 'Home') {
      update(min)
      return
    }
    if (event.key === 'End') {
      update(effectiveMaximum())
      return
    }
    const leftward = event.key === 'ArrowLeft'
    const delta = direction === 'from-left'
      ? (leftward ? -keyboardStep : keyboardStep)
      : (leftward ? keyboardStep : -keyboardStep)
    update(value + delta)
  }

  return (
    <div
      aria-label={label}
      aria-orientation="vertical"
      aria-valuemax={effectiveMaximum()}
      aria-valuemin={min}
      aria-valuenow={value}
      aria-valuetext={`${String(value)} pixels`}
      className="panel-resize-handle"
      onDoubleClick={() => update(defaultValue)}
      onKeyDown={keyDown}
      onLostPointerCapture={() => {
        activePointer.current = undefined
        onResizeStateChange?.(false)
      }}
      onPointerCancel={finishResize}
      onPointerDown={pointerDown}
      onPointerMove={pointerMove}
      onPointerUp={finishResize}
      role="separator"
      tabIndex={0}
      title={`${label}. Drag, use arrow keys, or double-click to reset.`}
    >
      <span aria-hidden="true" />
    </div>
  )
}
