// A PDF saved for offline reading must open offline, but its reader's big files
// (pdf.js and its worker) are not precached (scripts/sw-assets.mjs): they are
// loaded once when the PDF is saved, through the service worker, which keeps
// every built file it serves (shell() in sw.ts).
export async function warmPdfReader(): Promise<void> {
  const { PDF_WORKER_URL } = await import('../reader/PdfReader')
  const res = await fetch(PDF_WORKER_URL)
  if (!res.ok) throw new Error(`pdf worker: ${res.status}`)
  await res.blob()
}
