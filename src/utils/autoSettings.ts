import { reactive } from 'vue'

export const runtimeLimits = { grace_period_seconds: 120, url_discover_timeout_seconds: 600 } as const
export type RuntimeKey = keyof typeof runtimeLimits
type SettingsAPI = { getSettings(): Promise<Record<string, string>>; setSettings(values: Record<string, string>): Promise<unknown> }

/** Serialize field patches, so a slower earlier request cannot overwrite a newer value. */
export function createAutoSettings(api: SettingsAPI) {
  const state = reactive({
    loaded: false, loadError: '',
    values: { grace_period_seconds: '8', url_discover_timeout_seconds: '30' },
    fields: {
      grace_period_seconds: { status: 'idle', error: '' },
      url_discover_timeout_seconds: { status: 'idle', error: '' },
    },
  })
  let queue = Promise.resolve()
  const revision = { grace_period_seconds: 0, url_discover_timeout_seconds: 0 }
  async function load() {
    state.loadError = ''
    try {
      const settings = await api.getSettings()
      for (const key of Object.keys(runtimeLimits) as RuntimeKey[]) {
        if (settings[key] !== undefined) state.values[key] = settings[key]
      }
      state.loaded = true
      return settings
    } catch (error) { state.loadError = String(error); return null }
  }
  function set(key: RuntimeKey, value: string) {
    if (!state.loaded) return Promise.resolve()
    state.values[key] = value
    const current = ++revision[key], field = state.fields[key]
    field.error = ''
    const number = Number(value)
    if (!/^\d+$/.test(value) || number < 1 || number > runtimeLimits[key]) {
      field.status = 'invalid'
      return Promise.resolve()
    }
    field.status = 'saving'
    queue = queue.then(async () => {
      if (current !== revision[key]) return
      try {
        await api.setSettings({ [key]: String(number) })
        if (current === revision[key]) field.status = 'saved'
      } catch (error) {
        if (current === revision[key]) { field.status = 'error'; field.error = String(error) }
      }
    })
    return queue
  }
  return { state, load, set }
}
