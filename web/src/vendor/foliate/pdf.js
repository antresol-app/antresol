// Vault stub: the original pdf.js pulls vendor/pdfjs. view.js imports
// './pdf.js' dynamically, so the file must exist for the build; PDFs are
// opened by Vault's PdfReader (pdfjs-dist), never by foliate.
export const makePDF = () => { throw new Error('PDF открывается в PdfReader') }
