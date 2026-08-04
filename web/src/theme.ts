export type DashboardTheme = 'light' | 'dark'

export const THEME_STORAGE_KEY = 'notion-manager-theme'

export function getInitialTheme(): DashboardTheme {
  const documentTheme = document.documentElement.dataset.theme
  if (documentTheme === 'dark' || documentTheme === 'light') return documentTheme
  try {
    return window.localStorage.getItem(THEME_STORAGE_KEY) === 'dark' ? 'dark' : 'light'
  } catch {
    return 'light'
  }
}

export function applyTheme(theme: DashboardTheme): void {
  document.documentElement.dataset.theme = theme
  document.documentElement.style.colorScheme = theme
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, theme)
  } catch {
    // Storage can be unavailable in hardened/private browser contexts.
  }
}
