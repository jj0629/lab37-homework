import { ref, watchEffect } from 'vue'

export type ThemePref = 'system' | 'light' | 'dark'

function load(): ThemePref {
  try {
    const v = localStorage.getItem('theme')
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    /* storage unavailable */
  }
  return 'system'
}

const pref = ref<ThemePref>(load())

watchEffect(() => {
  const root = document.documentElement
  if (pref.value === 'system') root.removeAttribute('data-theme')
  else root.setAttribute('data-theme', pref.value)
  try {
    localStorage.setItem('theme', pref.value)
  } catch {
    /* ignore */
  }
})

export function useTheme() {
  function cycle() {
    pref.value = pref.value === 'system' ? 'light' : pref.value === 'light' ? 'dark' : 'system'
  }
  return { pref, cycle }
}
