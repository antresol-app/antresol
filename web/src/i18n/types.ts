// Catalog shapes. Russian is the source: every namespace file in ./ru is the
// truth for keys; the matching file in ./en must have the same keys (the
// `EnOf` type makes tsc enforce it, a test checks the placeholders too).

/** Plural forms by Intl.PluralRules category; ru uses one/few/many, en one/other. */
export type Plural = { one: string; few?: string; many?: string; other?: string }

/** A catalog value: a string with `{name}` placeholders, or plural forms (pick by `count`). */
export type Entry = string | Plural

/** The English half of a namespace: same keys, strings stay strings, plurals become one/other. */
export type EnOf<T> = { [K in keyof T]: T[K] extends string ? string : { one: string; other: string } }

export type Params = Record<string, string | number>
