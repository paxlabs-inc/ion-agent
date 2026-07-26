import type { SVGProps } from 'react'

export type IconName =
  | 'activity'
  | 'archive'
  | 'arrow-up'
  | 'brain'
  | 'check'
  | 'chevron-down'
  | 'close'
  | 'folder'
  | 'fork'
  | 'history'
  | 'home'
  | 'menu'
  | 'more'
  | 'paperclip'
  | 'panel-left-close'
  | 'panel-left-open'
  | 'plus'
  | 'search'
  | 'share'
  | 'settings'
  | 'shield'
  | 'spark'
  | 'stop'
  | 'trash'
  | 'volume'
  | 'workflow'
  | 'edit'

const paths: Record<IconName, React.ReactNode> = {
  activity: <path d="M4 12h3l2-5 4 10 2-5h5" />,
  archive: (
    <>
      <path d="M4 7h16M5 7v12h14V7M3 4h18v3H3z" />
      <path d="M9 11h6" />
    </>
  ),
  'arrow-up': <path d="m6 11 6-6 6 6M12 5v14" />,
  brain: (
    <>
      <path d="M9.5 4.5A3 3 0 0 0 5 7a3 3 0 0 0 .6 5.8A3.2 3.2 0 0 0 9 18a3 3 0 0 0 3-3V7.5a3 3 0 0 0-2.5-3Z" />
      <path d="M14.5 4.5A3 3 0 0 1 19 7a3 3 0 0 1-.6 5.8A3.2 3.2 0 0 1 15 18a3 3 0 0 1-3-3M8 9h4M16 9h-4" />
    </>
  ),
  check: <path d="m5 12 4 4L19 6" />,
  'chevron-down': <path d="m6 9 6 6 6-6" />,
  close: <path d="m6 6 12 12M18 6 6 18" />,
  folder: <path d="M3 6.5h7l2 2h9v10H3z" />,
  fork: (
    <>
      <circle cx="6" cy="5" r="2" />
      <circle cx="18" cy="5" r="2" />
      <circle cx="12" cy="19" r="2" />
      <path d="M6 7v2a4 4 0 0 0 4 4h2M18 7v2a4 4 0 0 1-4 4h-2v4" />
    </>
  ),
  history: (
    <>
      <path d="M3 12a9 9 0 1 0 3-6.7L3 8" />
      <path d="M3 3v5h5M12 7v5l3 2" />
    </>
  ),
  home: <path d="m3 11 9-7 9 7v9h-6v-6H9v6H3z" />,
  menu: <path d="M4 7h16M4 12h16M4 17h16" />,
  more: <path d="M5 12h.01M12 12h.01M19 12h.01" />,
  paperclip: <path d="m8 12.5 6.2-6.2a3 3 0 0 1 4.3 4.2l-8 8a4.5 4.5 0 0 1-6.4-6.4l8-8" />,
  'panel-left-close': (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M9 4v16m7-12-4 4 4 4" />
    </>
  ),
  'panel-left-open': (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M9 4v16m3-12 4 4-4 4" />
    </>
  ),
  plus: <path d="M12 5v14M5 12h14" />,
  search: (
    <>
      <circle cx="11" cy="11" r="7" />
      <path d="m16 16 5 5" />
    </>
  ),
  share: (
    <>
      <circle cx="18" cy="5" r="2" />
      <circle cx="6" cy="12" r="2" />
      <circle cx="18" cy="19" r="2" />
      <path d="m8 11 8-5M8 13l8 5" />
    </>
  ),
  settings: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M19 12a7 7 0 0 0-.1-1l2-1.5-2-3.4-2.4 1a8 8 0 0 0-1.7-1L14.5 3h-5L9 6.1a8 8 0 0 0-1.7 1l-2.4-1-2 3.4L5 11a7 7 0 0 0 0 2l-2.1 1.5 2 3.4 2.4-1a8 8 0 0 0 1.7 1l.5 3.1h5l.5-3.1a8 8 0 0 0 1.7-1l2.4 1 2-3.4L19 13a7 7 0 0 0 .1-1Z" />
    </>
  ),
  shield: <path d="M12 3 20 6v5c0 5-3.5 8.5-8 10-4.5-1.5-8-5-8-10V6zM9 12l2 2 4-5" />,
  spark: <path d="M12 2c.5 5.7 2.3 7.5 8 8-5.7.5-7.5 2.3-8 8-.5-5.7-2.3-7.5-8-8 5.7-.5 7.5-2.3 8-8Z" />,
  stop: <rect x="7" y="7" width="10" height="10" rx="2" />,
  trash: (
    <>
      <path d="M4 7h16M9 7V4h6v3M7 7l1 13h8l1-13" />
      <path d="M10 11v5M14 11v5" />
    </>
  ),
  volume: (
    <>
      <path d="M5 10v4h3l4 4V6L8 10H5Z" />
      <path d="M16 9a4 4 0 0 1 0 6M18.5 6.5a8 8 0 0 1 0 11" />
    </>
  ),
  edit: (
    <>
      <path d="m4 20 4.5-1 10-10a2.1 2.1 0 0 0-3-3l-10 10L4 20Z" />
      <path d="m14 7 3 3" />
    </>
  ),
  workflow: (
    <>
      <rect x="3" y="4" width="7" height="5" rx="1" />
      <rect x="14" y="15" width="7" height="5" rx="1" />
      <path d="M10 6.5h3a4 4 0 0 1 4 4V15M14 17.5h-3a4 4 0 0 1-4-4V9" />
    </>
  ),
}

export function Icon({
  name,
  ...props
}: { name: IconName } & SVGProps<SVGSVGElement>) {
  return (
    <svg
      aria-hidden="true"
      fill="none"
      height="20"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth="1.8"
      viewBox="0 0 24 24"
      width="20"
      {...props}
    >
      {paths[name]}
    </svg>
  )
}
