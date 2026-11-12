// Line icons drawn on a 24×24 grid. Decorative by default
// (aria-hidden); pass `label` when an icon is the only content of a control.
import type { SVGProps } from 'react'

const paths = {
  search: (
    <>
      <circle cx="10.5" cy="10.5" r="6.5" />
      <path d="m15.5 15.5 5 5" />
    </>
  ),
  home: <path d="M4 10.5 12 4l8 6.5V19a1 1 0 0 1-1 1h-4.5v-5.5h-5V20H5a1 1 0 0 1-1-1z" />,
  film: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2.5" />
      <path d="M7.5 4.5v15M16.5 4.5v15M3.5 9.5h4M3.5 14.5h4M16.5 9.5h4M16.5 14.5h4" />
    </>
  ),
  tv: (
    <>
      <rect x="3" y="5" width="18" height="12" rx="2.5" />
      <path d="M8.5 20.5h7" />
    </>
  ),
  broadcast: (
    <>
      <rect x="3" y="8.5" width="18" height="12" rx="2.5" />
      <path d="m8 3.5 4 5 4-5" />
    </>
  ),
  music: (
    <>
      <path d="M9 18V6.5l11-2.5V16" />
      <circle cx="6.5" cy="18" r="2.5" />
      <circle cx="17.5" cy="16" r="2.5" />
    </>
  ),
  book: (
    <>
      <path d="M12 6.5C10.3 5.2 8 4.5 4 4.5v13c4 0 6.3.7 8 2 1.7-1.3 4-2 8-2v-13c-4 0-6.3.7-8 2z" />
      <path d="M12 6.5v13" />
    </>
  ),
  play: <path d="M7 4.8v14.4a.8.8 0 0 0 1.2.7l11.6-7.2a.8.8 0 0 0 0-1.4L8.2 4.1A.8.8 0 0 0 7 4.8z" fill="currentColor" stroke="none" />,
  pause: (
    <>
      <rect x="6" y="4.5" width="4" height="15" rx="1.2" fill="currentColor" stroke="none" />
      <rect x="14" y="4.5" width="4" height="15" rx="1.2" fill="currentColor" stroke="none" />
    </>
  ),
  next: (
    <>
      <path d="M4 6v12l8-6zM12 6v12l8-6z" fill="currentColor" stroke="none" />
    </>
  ),
  previous: (
    <>
      <path d="M20 6v12l-8-6zM12 6v12l-8-6z" fill="currentColor" stroke="none" />
    </>
  ),
  info: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 11v5.5" />
      <circle cx="12" cy="7.8" r="0.4" fill="currentColor" />
    </>
  ),
  plus: <path d="M12 5v14M5 12h14" />,
  minus: <path d="M5 12h14" />,
  minusCircle: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M8.5 12h7" />
    </>
  ),
  plusCircle: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 8.5v7M8.5 12h7" />
    </>
  ),
  // a reorder handle (SF «line.3.horizontal»)
  grip: <path d="M5 8h14M5 12h14M5 16h14" />,
  check: <path d="m5 12.5 4.5 4.5L19 7.5" />,
  close: <path d="m6.5 6.5 11 11M17.5 6.5l-11 11" />,
  chevronLeft: <path d="M15 5.5 8.5 12l6.5 6.5" />,
  chevronRight: <path d="M9 5.5 15.5 12 9 18.5" />,
  chevronDown: <path d="M5.5 9 12 15.5 18.5 9" />,
  chevronUp: <path d="M5.5 15 12 8.5 18.5 15" />,
  // a pop-up button's ⌃⌄
  chevronUpDown: <path d="M7.5 9.5 12 5l4.5 4.5M7.5 14.5 12 19l4.5-4.5" />,
  download: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5v8M8.5 12.5 12 16l3.5-3.5" />
    </>
  ),
  lock: (
    <>
      <rect x="5.5" y="10.5" width="13" height="9.5" rx="2" />
      <path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5" />
    </>
  ),
  person: (
    <>
      <circle cx="12" cy="8.5" r="3.5" />
      <path d="M5 19.5c1-3.3 3.8-5 7-5s6 1.7 7 5" />
    </>
  ),
  switchUser: (
    <>
      <circle cx="9.5" cy="8.5" r="3.2" />
      <path d="M3.5 19c.9-3 3.2-4.6 6-4.6 1.4 0 2.6.4 3.6 1.1" />
      <path d="M15.5 13.5h5M18.5 11.5l2 2-2 2M20.5 18.5h-5M17.5 16.5l-2 2 2 2" />
    </>
  ),
  // a cog: eight teeth (tips at r 9.1, roots on a circle of r 7) round a hub
  gear: (
    <>
      <path d="M10.32 5.21L10.7 2.99L13.3 2.99L13.68 5.21A7 7 0 0 1 15.61 6.01L17.45 4.71L19.29 6.55L17.99 8.39A7 7 0 0 1 18.79 10.32L21.01 10.7L21.01 13.3L18.79 13.68A7 7 0 0 1 17.99 15.61L19.29 17.45L17.45 19.29L15.61 17.99A7 7 0 0 1 13.68 18.79L13.3 21.01L10.7 21.01L10.32 18.79A7 7 0 0 1 8.39 17.99L6.55 19.29L4.71 17.45L6.01 15.61A7 7 0 0 1 5.21 13.68L2.99 13.3L2.99 10.7L5.21 10.32A7 7 0 0 1 6.01 8.39L4.71 6.55L6.55 4.71L8.39 6.01A7 7 0 0 1 10.32 5.21z" />
      <circle cx="12" cy="12" r="2.8" />
    </>
  ),
  cloud: <path d="M7.5 18.5h9.5a4 4 0 0 0 .6-7.95A5.5 5.5 0 0 0 7 9.6a4.5 4.5 0 0 0 .5 8.9z" />,
  eye: (
    <>
      <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
  eyeSlash: (
    <>
      <path d="M2.5 12S6 5.5 12 5.5c1.6 0 3 .4 4.3 1.1M21.5 12s-3.5 6.5-9.5 6.5c-1.6 0-3-.4-4.3-1.1" />
      <path d="M4 20 20 4" />
    </>
  ),
  arrowRight: <path d="M5 12h13M13 6.5l5.5 5.5-5.5 5.5" />,
  sparkles: (
    <>
      <path d="M11 3.5 12.6 8 17 9.5l-4.4 1.6L11 15.5l-1.6-4.4L5 9.5 9.4 8z" />
      <path d="M18 14.5l.8 2.2 2.2.8-2.2.8-.8 2.2-.8-2.2-2.2-.8 2.2-.8z" />
    </>
  ),
  // skip back / skip forward: a circle open at the top, the
  // filled arrowhead points the way the time goes. Optically centred: the
  // circle (not circle + arrowhead) on the button's centre, the digits on
  // the circle's (their cap height, not the em box)
  back10: (
    <>
      <path d="M12 4a8.4 8.4 0 1 1-7.27 4.2" />
      <path d="M9 4 12.5 1.8v4.4z" fill="currentColor" strokeWidth={1} />
      <text x="12" y="15" fontSize="7" fontWeight="700" textAnchor="middle" fill="currentColor" stroke="none" fontFamily="-apple-system, system-ui, sans-serif">
        10
      </text>
    </>
  ),
  forward10: (
    <>
      <path d="M12 4a8.4 8.4 0 1 0 7.27 4.2" />
      <path d="M15 4 11.5 1.8v4.4z" fill="currentColor" strokeWidth={1} />
      <text x="12" y="15" fontSize="7" fontWeight="700" textAnchor="middle" fill="currentColor" stroke="none" fontFamily="-apple-system, system-ui, sans-serif">
        10
      </text>
    </>
  ),
  back15: (
    <>
      <path d="M12 4a8.4 8.4 0 1 1-7.27 4.2" />
      <path d="M9 4 12.5 1.8v4.4z" fill="currentColor" strokeWidth={1} />
      <text x="12" y="15" fontSize="7" fontWeight="700" textAnchor="middle" fill="currentColor" stroke="none" fontFamily="-apple-system, system-ui, sans-serif">
        15
      </text>
    </>
  ),
  forward30: (
    <>
      <path d="M12 4a8.4 8.4 0 1 0 7.27 4.2" />
      <path d="M15 4 11.5 1.8v4.4z" fill="currentColor" strokeWidth={1} />
      <text x="12" y="15" fontSize="7" fontWeight="700" textAnchor="middle" fill="currentColor" stroke="none" fontFamily="-apple-system, system-ui, sans-serif">
        30
      </text>
    </>
  ),
  speaker: (
    <>
      <path d="M4 9.5h3.2L12 5.5v13l-4.8-4H4z" fill="currentColor" stroke="none" />
      <path d="M15.5 9a4.2 4.2 0 0 1 0 6M18 6.5a7.8 7.8 0 0 1 0 11" />
    </>
  ),
  speakerMute: (
    <>
      <path d="M4 9.5h3.2L12 5.5v13l-4.8-4H4z" fill="currentColor" stroke="none" />
      <path d="m15.5 9.5 5 5M20.5 9.5l-5 5" />
    </>
  ),
  pip: (
    <>
      <rect x="2.8" y="4.8" width="18.4" height="14.4" rx="2.4" />
      <rect x="12" y="12" width="6.6" height="4.6" rx="1" fill="currentColor" stroke="none" />
    </>
  ),
  airplay: (
    <>
      <path d="M6.5 17H5a2.5 2.5 0 0 1-2.5-2.5v-8A2.5 2.5 0 0 1 5 4h14a2.5 2.5 0 0 1 2.5 2.5v8A2.5 2.5 0 0 1 19 17h-1.5" />
      <path d="M12 14.5 17 20.5H7z" fill="currentColor" stroke="none" />
    </>
  ),
  expand: <path d="M4.5 9.5v-5h5M19.5 9.5v-5h-5M4.5 14.5v5h5M19.5 14.5v5h-5" />,
  collapse: <path d="M9.5 4.5v5h-5M14.5 4.5v5h5M9.5 19.5v-5h-5M14.5 19.5v-5h5" />,
  captions: (
    <>
      <path d="M5.5 5h13A2.5 2.5 0 0 1 21 7.5v8a2.5 2.5 0 0 1-2.5 2.5H11l-4.5 3v-3h-1A2.5 2.5 0 0 1 3 15.5v-8A2.5 2.5 0 0 1 5.5 5z" />
      <path d="M7 10.5h5M14 10.5h3M7 13.8h3M12 13.8h5" />
    </>
  ),
  ellipsis: (
    <>
      <circle cx="6" cy="12" r="1.6" fill="currentColor" stroke="none" />
      <circle cx="12" cy="12" r="1.6" fill="currentColor" stroke="none" />
      <circle cx="18" cy="12" r="1.6" fill="currentColor" stroke="none" />
    </>
  ),
  restart: (
    <>
      <path d="M4.5 12a7.5 7.5 0 1 0 2.2-5.3" />
      <path d="M4.2 3.6v4h4" />
    </>
  ),
  checkCircle: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="m8.2 12.3 2.6 2.6 5-5.4" />
    </>
  ),
  warning: (
    <>
      <path d="M10.3 4.6 2.9 17.5A2 2 0 0 0 4.6 20.5h14.8a2 2 0 0 0 1.7-3L13.7 4.6a2 2 0 0 0-3.4 0z" />
      <path d="M12 9.5v4.5" />
      <circle cx="12" cy="17" r="0.4" fill="currentColor" />
    </>
  ),
  folder: <path d="M3.5 7.5V17a2 2 0 0 0 2 2h13a2 2 0 0 0 2-2V9.5a2 2 0 0 0-2-2h-6.3L10.4 5.6a1.6 1.6 0 0 0-1.1-.5H5.5a2 2 0 0 0-2 2z" />,
  trash: (
    <>
      <path d="M4.5 6.5h15M9.5 6.5V4.8a.8.8 0 0 1 .8-.8h3.4a.8.8 0 0 1 .8.8v1.7" />
      <path d="M6.5 6.5 7.3 19a1.6 1.6 0 0 0 1.6 1.5h6.2a1.6 1.6 0 0 0 1.6-1.5l.8-12.5" />
      <path d="M10 10.5v6M14 10.5v6" />
    </>
  ),
  chart: (
    <>
      <path d="M4 20h16" />
      <rect x="5.5" y="11" width="3" height="6.5" rx="0.8" />
      <rect x="10.5" y="6" width="3" height="11.5" rx="0.8" />
      <rect x="15.5" y="9" width="3" height="8.5" rx="0.8" />
    </>
  ),
  people: (
    <>
      <circle cx="9" cy="8.5" r="3.2" />
      <path d="M3.5 19c.6-3.2 2.8-5 5.5-5s4.9 1.8 5.5 5" />
      <circle cx="16.5" cy="9.5" r="2.5" />
      <path d="M15.6 14.2c2.4-.4 4.3 1.2 4.9 4" />
    </>
  ),
  sliders: (
    <>
      <path d="M4 7h9M17 7h3M4 17h3M11 17h9" />
      <circle cx="15" cy="7" r="2" />
      <circle cx="9" cy="17" r="2" />
    </>
  ),
  plug: (
    <>
      <path d="M9 3.5v4M15 3.5v4" />
      <path d="M6.5 7.5h11v3a5.5 5.5 0 0 1-11 0z" />
      <path d="M12 16v4.5" />
    </>
  ),
  send: <path d="M20.5 3.5 10 14M20.5 3.5 14 20.5l-4-6.5-6.5-4z" />,
  clock: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5V12l3 2" />
    </>
  ),
  arrowUp: <path d="M12 19V5M6 11l6-6 6 6" />,
  returnArrow: <path d="M9 14.5 4.5 10 9 5.5M4.5 10h10a5 5 0 0 1 0 10H12" />,
  arrowDown: <path d="M12 5v14M6 13l6 6 6-6" />,
  server: (
    <>
      <rect x="4" y="4.5" width="16" height="6" rx="1.8" />
      <rect x="4" y="13.5" width="16" height="6" rx="1.8" />
      <circle cx="8" cy="7.5" r="0.5" fill="currentColor" />
      <circle cx="8" cy="16.5" r="0.5" fill="currentColor" />
    </>
  ),
  pencil: <path d="m15.5 5.5 3 3L9 18H6v-3zM13.5 7.5l3 3" />,
  shuffle: <path d="M3.5 7h3.2c1.5 0 2.9.8 3.7 2l3.2 6c.8 1.2 2.2 2 3.7 2h3.2M17.5 4.5 20.5 7l-3 2.5M17.5 14.5l3 2.5-3 2.5M3.5 17h3.2c1.2 0 2.3-.5 3.1-1.3M20.5 7h-3.2c-1.2 0-2.3.5-3.1 1.3" />,
  repeat: <path d="M4.5 11V9.5a3 3 0 0 1 3-3h12M16.5 3.5l3 3-3 3M19.5 13v1.5a3 3 0 0 1-3 3h-12M7.5 20.5l-3-3 3-3" />,
  repeatOne: (
    <>
      <path d="M4.5 11V9.5a3 3 0 0 1 3-3h12M16.5 3.5l3 3-3 3M19.5 13v1.5a3 3 0 0 1-3 3h-12M7.5 20.5l-3-3 3-3" />
      <path d="M11 10.8 12.4 10v4.5" strokeWidth="1.7" />
    </>
  ),
  queue: <path d="M4 6.5h16M4 11.5h16M4 16.5h9M16.5 14.5v5l4-2.5z" />,
  quote: <path d="M5 18.5c3 0 5-2.4 5-5.5V7.5H5v5.5h3.2M14 18.5c3 0 5-2.4 5-5.5V7.5h-5v5.5h3.2" />,
  heart: <path d="M12 19.5s-7.5-4.4-7.5-10A4.2 4.2 0 0 1 12 7a4.2 4.2 0 0 1 7.5 2.5c0 5.6-7.5 10-7.5 10z" />,
  heartFill: (
    <path d="M12 19.5s-7.5-4.4-7.5-10A4.2 4.2 0 0 1 12 7a4.2 4.2 0 0 1 7.5 2.5c0 5.6-7.5 10-7.5 10z" fill="currentColor" />
  ),
  star: <path d="M12 3.6l2.6 5.3 5.8.8-4.2 4.1 1 5.8L12 16.9l-5.2 2.7 1-5.8-4.2-4.1 5.8-.8z" />,
  starFill: <path d="M12 3.6l2.6 5.3 5.8.8-4.2 4.1 1 5.8L12 16.9l-5.2 2.7 1-5.8-4.2-4.1 5.8-.8z" fill="currentColor" />,
  /** The programme guide: channels × time. */
  guide: (
    <>
      <rect x="3" y="4.5" width="18" height="15" rx="2.5" />
      <path d="M3 9.5h18M8 9.5v10M8 14.5h6.5M14.5 9.5v10" />
    </>
  ),
  moon: <path d="M19.5 14.5A7.5 7.5 0 0 1 9.5 4.5a7.5 7.5 0 1 0 10 10z" />,
  speed: (
    <>
      <path d="M4.6 17.5a8.5 8.5 0 1 1 14.8 0" />
      <path d="m12 13 4-4.5" />
      <circle cx="12" cy="13" r="1.2" fill="currentColor" />
    </>
  ),
  headphones: (
    <>
      <path d="M4 15.5v-3a8 8 0 0 1 16 0v3" />
      <rect x="3.5" y="14" width="4" height="6.5" rx="1.6" />
      <rect x="16.5" y="14" width="4" height="6.5" rx="1.6" />
    </>
  ),
  // «Хочу посмотреть»: a list with a plus (SF «text.badge.plus»)
  // on whole pixels at 24 px (the item page's round buttons): the bars stay crisp at 1x
  watchlist: <path d="M4 6h14M4 11h14M4 16h7M18 13v8M14 17h8" />,
  bookmark: <path d="M6.5 4.5h11v15.5L12 16l-5.5 4z" />,
  bookmarkFill: <path d="M6.5 4.5h11v15.5L12 16l-5.5 4z" fill="currentColor" />,
  textSize: <path d="M3.5 18.5 8 7l4.5 11.5M5 15h6M13.5 18.5l3.5-8.5 3.5 8.5M14.6 16h4.8" />,
  list: (
    <>
      <path d="M9 6.5h11M9 12h11M9 17.5h11" />
      <circle cx="4.75" cy="6.5" r="0.6" fill="currentColor" />
      <circle cx="4.75" cy="12" r="0.6" fill="currentColor" />
      <circle cx="4.75" cy="17.5" r="0.6" fill="currentColor" />
    </>
  ),
} as const

export type IconName = keyof typeof paths

export interface IconProps extends Omit<SVGProps<SVGSVGElement>, 'children'> {
  name: IconName
  size?: number
  /** Accessible name; omit for decorative icons. */
  label?: string
}

export function Icon({ name, size = 20, label, strokeWidth = 1.9, ...rest }: IconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden={label ? undefined : true}
      role={label ? 'img' : undefined}
      aria-label={label}
      focusable="false"
      data-icon={name}
      {...rest}
    >
      {paths[name]}
    </svg>
  )
}
