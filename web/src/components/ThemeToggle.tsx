import { useTranslation } from 'react-i18next'
import { IconMoon, IconSun } from './Icons'
import type { DashboardTheme } from '../theme'

export function ThemeToggle({ theme, onToggle }: { theme: DashboardTheme; onToggle: () => void }) {
  const { t } = useTranslation()
  const title = theme === 'light' ? t('header.switch_to_dark') : t('header.switch_to_light')

  return (
    <button
      type="button"
      onClick={onToggle}
      title={title}
      aria-label={title}
      aria-pressed={theme === 'dark'}
      className="inline-flex h-8 items-center gap-0.5 rounded-lg border border-border bg-bg-card p-1 text-text-muted shadow-sm shadow-shadow/5 transition-colors hover:bg-bg-card-hover"
    >
      <span className={`flex h-6 w-6 items-center justify-center rounded-md transition-all ${theme === 'light' ? 'bg-overlay/10 text-strong shadow-sm' : 'text-text-muted'}`}>
        <IconSun size={13} />
      </span>
      <span className={`flex h-6 w-6 items-center justify-center rounded-md transition-all ${theme === 'dark' ? 'bg-overlay/10 text-strong shadow-sm' : 'text-text-muted'}`}>
        <IconMoon size={13} />
      </span>
      <span className="sr-only">{theme === 'light' ? t('header.light') : t('header.dark')}</span>
    </button>
  )
}
