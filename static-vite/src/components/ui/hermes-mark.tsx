import type { SVGProps } from 'react'
import { EMPTY_D, EMPTY_TRANSFORM, TITLEBAR_D, TITLEBAR_TRANSFORM } from './hermes-paths'

// Hermes caduceus mark — path data verbatim from vanilla index.html (see hermes-paths.ts).

export function HermesMark(props: SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" width={16} height={16} aria-hidden="true" {...props}>
      <defs>
        <linearGradient id="app-titlebar-mark" x1="0" y1="0" x2="1" y2="0">
          <stop offset="0" stopColor="#08EBF1" />
          <stop offset="1" stopColor="#3889FD" />
        </linearGradient>
        <radialGradient id="app-titlebar-tile" cx="50%" cy="42%" r="75%">
          <stop offset="0%" stopColor="#0D3460" />
          <stop offset="100%" stopColor="#021128" />
        </radialGradient>
      </defs>
      <rect width="64" height="64" rx="14.3" fill="url(#app-titlebar-tile)" />
      <g transform={TITLEBAR_TRANSFORM}>
        <path fill="url(#app-titlebar-mark)" fillRule="evenodd" d={TITLEBAR_D} />
      </g>
    </svg>
  )
}

export function HermesEmptyMark(props: SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" width={80} height={80} aria-label="Hermes caduceus" {...props}>
      <defs>
        <linearGradient id="hermes-mark" x1="0" y1="0" x2="1" y2="0">
          <stop className="hm-g0" offset="0" stopColor="#08EBF1" />
          <stop className="hm-g1" offset="1" stopColor="#3889FD" />
        </linearGradient>
      </defs>
      <g transform={EMPTY_TRANSFORM}>
        <path fill="url(#hermes-mark)" fillRule="evenodd" d={EMPTY_D} />
      </g>
    </svg>
  )
}
