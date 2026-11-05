// Types for the parts of foliate-js epubcfi.js the CFI test uses.
declare module '*/vendor/foliate/epubcfi.js' {
  export function joinIndir(...cfis: string[]): string
  export function fromRange(range: Range): string
  export const fake: { fromIndex(index: number): string }
}
