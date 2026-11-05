// Types for the parts of foliate-js (view.js) Vault uses.
declare module '*/vendor/foliate/view.js' {
  export interface TocItem {
    label: string
    href: string
    subitems?: TocItem[]
  }
  export interface FoliateBook {
    toc?: TocItem[]
    dir?: string
    metadata?: { title?: unknown; language?: string }
    sections: { linear?: string; size?: number }[]
    /** EPUB and MOBI: 'load' (detail.isScript, allow) and 'data' (detail.data, type) hooks. */
    transformTarget?: EventTarget
  }
  export function makeBook(file: File | Blob | string): Promise<FoliateBook>
  export interface RelocateDetail {
    cfi: string
    fraction: number
    tocItem?: { label: string; href: string } | null
    location?: { current: number; next: number; total: number }
    /** The section on screen (its index in the spine). */
    section?: { current: number; total: number }
  }
  export interface SearchExcerpt {
    pre: string
    match: string
    post: string
  }
  export interface SearchSubitem {
    cfi: string
    excerpt: SearchExcerpt
  }
  export type SearchResult = 'done' | { progress: number } | { label: string; subitems: SearchSubitem[] } | { cfi: string; excerpt: SearchExcerpt }
  export interface Paginator extends HTMLElement {
    /** The page shown and the pages of the section, each with a blank page at either end (paged mode). */
    readonly page: number
    readonly pages: number
    readonly scrolled: boolean
    setStyles(css: string | [string, string]): void
    next(): Promise<void>
    prev(): Promise<void>
    destroy(): void
  }
  export class View extends HTMLElement {
    book: FoliateBook
    renderer: Paginator
    lastLocation: RelocateDetail | null
    open(book: File | Blob | string | FoliateBook): Promise<void>
    close(): void
    init(o: { lastLocation?: string | null; showTextStart?: boolean }): Promise<void>
    goTo(target: string | number): Promise<unknown>
    goToFraction(f: number): Promise<void>
    goLeft(): Promise<void>
    goRight(): Promise<void>
    next(): Promise<void>
    prev(): Promise<void>
    search(o: { query: string; matchCase?: boolean; matchWholeWords?: boolean; matchDiacritics?: boolean }): AsyncGenerator<SearchResult>
    clearSearch(): void
    /** Draws an annotation (its `value` is a CFI): 'draw-annotation' asks how, in the loaded section that holds it. */
    addAnnotation(a: { value: string }): Promise<{ index: number; label: string } | undefined>
    deleteAnnotation(a: { value: string }): Promise<unknown>
    /** The CFI of a range in section `index`. */
    getCFI(index: number, range?: Range): string
    resolveNavigation(target: string | number): { index: number } | undefined
  }
}

declare module '*/vendor/foliate/overlayer.js' {
  type Draw = (rects: DOMRectList, options: { color: string; width?: number }) => SVGElement
  export class Overlayer {
    static highlight: Draw
    static underline: Draw
    static outline: Draw
  }
}

declare module '*/vendor/foliate/vendor/zip.js' {
  export interface Entry {
    filename: string
    directory: boolean
    getData?<T>(writer: { __out?: T } & object): Promise<T>
  }
  export function configure(o: { useWebWorkers?: boolean }): void
  export class BlobReader {
    constructor(blob: Blob)
  }
  export class BlobWriter {
    constructor(type?: string)
    __out?: Blob
  }
  export class ZipReader {
    constructor(reader: BlobReader)
    getEntries(): Promise<Entry[]>
    close(): Promise<void>
  }
}

// Types for the parts of foliate-js epubcfi.js the native reader uses.
declare module '*/vendor/foliate/epubcfi.js' {
  export function collapse(cfi: string, toEnd?: boolean): string
  export function compare(a: string, b: string): number
}
