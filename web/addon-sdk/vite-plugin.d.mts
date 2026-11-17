import type { Plugin } from 'vite'

/** The modules an addon shares with the app, and the names each one has. */
export declare const SHARED: Record<'react' | 'react/jsx-runtime' | 'react-dom' | 'react-router' | '@antresol/addon-sdk', string[]>
/** The virtual module that stands for shared specifier `s` in the bundle. */
export declare function sharedModule(s: keyof typeof SHARED): string
/** What a built index.css may not hold (global selectors, sibling escapes, at-rules other than @media/@supports/@container/@keyframes/@font-face, !important, outside url()); `id` prefixes @font-face families; empty = fine. */
export declare function cssProblems(css: string, options?: { id?: string }): string[]
/** Builds an addon's web bundle: one ES module, dist/web/index.js (and index.css from its CSS modules), on the app's shared modules. */
export declare function antresolAddon(options?: { entry?: string; outDir?: string; id?: string }): Plugin
export default antresolAddon
