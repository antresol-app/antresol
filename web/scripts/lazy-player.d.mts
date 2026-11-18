export const PLAYER: string
type Chunk = { file: string; isEntry?: boolean; imports?: string[]; dynamicImports?: string[] }
type Manifest = Record<string, Chunk>
export function staticClosure(manifest: Manifest, entry: string): Set<string>
export function isPlayerChunk(key: string, chunk: Chunk): boolean
export function lazyPlayerProblems(manifest: Manifest): string[]
