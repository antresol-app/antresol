// Stub of the sources build's module of the same name (without .stub), which
// is not in the public tree: imports resolve here only when it is absent
// (tsconfig moduleSuffixes, vite.config.ts resolve.extensions). Same exports
// and types; the core build never reaches them (__LIBRARY_EDITION__).
import type { ImportJob, ImportService } from '../../../api/types'

interface ServicesProps {
  services: ImportService[]
  onFiles: (files: File[]) => void
  uploading: boolean
  onJob: (job: ImportJob) => void
  onStart: () => void
  onProblem: (text: string) => void
}

export default function Services(_props: ServicesProps) {
  return null
}
