// Reads module CSS for tests: a rule's declarations by its selector.
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')

/** A file under web/src. */
export const css = (file: string) => readFileSync(path.join(SRC, file), 'utf8')

/** The declarations of the first rule whose selector list names `selector` exactly. */
export function rule(file: string, selector: string): string {
  const src = css(file).replace(/\/\*[\s\S]*?\*\//g, '')
  // a rule at the top level: after the start or the end of another rule
  const re = /(?:^|(?<=\}))\s*([^{}@]+)\{([^}]*)\}/g
  for (let m = re.exec(src); m; m = re.exec(src)) {
    if (m[1]!.split(',').some((s) => s.trim() === selector)) return m[2]!
  }
  throw new Error(`${selector} not in ${file}`)
}

/** The literal colours (hex, rgb(), rgba()) in a file under web/src, comments aside. */
export function literalColours(file: string): string[] {
  const code = css(file).replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '')
  return code.match(/#[0-9a-fA-F]{3,8}\b|rgba?\(/g) ?? []
}
