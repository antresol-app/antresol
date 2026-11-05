/// <reference types="vite/client" />

/**
 * true in the core build (a tree without src/app/routesFull.tsx: the
 * public one), false in the sources build: vite.config.ts replaces it with a
 * literal at build time. The sources build's modules have stubs beside them
 * (X.stub.ts / X.stub.tsx) that the public tree resolves to instead.
 * The core build serves the library alone; features of another build that
 * bring titles from outside must not be in its bundle at all, so code and strings of theirs sit behind this flag and the
 * bundler drops them as dead code. Test the identifier itself —
 * `if (__LIBRARY_EDITION__)`, `!__LIBRARY_EDITION__ && …`, `__LIBRARY_EDITION__ ? … : …`
 * — never a copy in a variable, and keep modules that only the sources build
 * uses free of top-level calls, so they drop out whole. A sources build may still
 * run library-only (VAULT_LIBRARY_ONLY): then `user.libraryOnly` hides the
 * same screens at run time. scripts/check-library-edition.sh checks the
 * bundle; docs/build-tags.md has the rest.
 */
declare const __LIBRARY_EDITION__: boolean
