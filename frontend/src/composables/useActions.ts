import { ref } from 'vue'
import { api } from '../api/client'
import type { ImportResult } from '../api/types'
import { useToast } from './useToast'

/** Bumped after any action that changes data, so open views refresh at once. */
export const dataVersion = ref(0)
export function bump() {
  dataVersion.value++
}

export function importToast(r: ImportResult) {
  const { push } = useToast()
  push({
    kind: r.rejected.length ? 'info' : 'success',
    title: `Imported ${r.filename}`,
    body: `${r.new} new · ${r.duplicate} already seen · ${r.rejected.length} rejected`,
    details: r.rejected.map((x) => `Line ${x.line}: ${x.reason}`),
  })
}

export function useCsvUpload() {
  const uploading = ref(false)
  const toast = useToast()
  async function upload(file: File) {
    uploading.value = true
    try {
      importToast(await api.importCSV(file))
      bump()
    } catch (e) {
      toast.error(e, `Could not import ${file.name}`)
    } finally {
      uploading.value = false
    }
  }
  return { uploading, upload }
}
