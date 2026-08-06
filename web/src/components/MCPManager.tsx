import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  createMCPServer,
  deleteMCPServer,
  updateMCPServer,
} from '../api'
import type { MCPAuthType, MCPServer, MCPServerInput } from '../api'

interface MCPManagerProps {
  servers: MCPServer[]
  loading: boolean
  onReload: () => Promise<void> | void
}

interface MCPFormState {
  id?: string
  name: string
  url: string
  auth_type: MCPAuthType
  username: string
  password: string
  bearer_token: string
  run_write_tools_automatically: boolean
  has_password: boolean
  has_bearer_token: boolean
}

const emptyMCPForm = (): MCPFormState => ({
  name: '',
  url: '',
  auth_type: 'none',
  username: '',
  password: '',
  bearer_token: '',
  run_write_tools_automatically: true,
  has_password: false,
  has_bearer_token: false,
})

const formFromServer = (server: MCPServer): MCPFormState => ({
  id: server.id,
  name: server.name,
  url: server.url,
  auth_type: server.auth_type,
  username: server.username || '',
  password: '',
  bearer_token: '',
  run_write_tools_automatically: server.run_write_tools_automatically,
  has_password: server.has_password,
  has_bearer_token: server.has_bearer_token,
})

function MCPServerModal({
  initial,
  onClose,
  onSaved,
}: {
  initial: MCPFormState
  onClose: () => void
  onSaved: () => Promise<void> | void
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState<MCPFormState>(initial)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => setForm(initial), [initial])

  const set = <K extends keyof MCPFormState>(key: K, value: MCPFormState[K]) => {
    setForm(current => ({ ...current, [key]: value }))
  }

  const submit = async () => {
    if (saving) return
    setSaving(true)
    setError('')
    const input: MCPServerInput = {
      id: form.id,
      name: form.name.trim(),
      url: form.url.trim(),
      auth_type: form.auth_type,
      username: form.auth_type === 'basic' ? form.username.trim() : undefined,
      password: form.auth_type === 'basic' ? form.password : undefined,
      bearer_token: form.auth_type === 'bearer' ? form.bearer_token : undefined,
      run_write_tools_automatically: form.run_write_tools_automatically,
    }
    try {
      if (form.id) await updateMCPServer(form.id, input)
      else await createMCPServer(input)
      await onSaved()
      onClose()
    } catch (e: any) {
      setError(e?.message || t('mcp.save_failed'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-[90] bg-black/55 backdrop-blur-[1px] flex items-center justify-center p-4" onMouseDown={onClose}>
      <div className="w-full max-w-[600px] max-h-[90vh] overflow-y-auto rounded-xl border border-border bg-bg-primary shadow-2xl" onMouseDown={event => event.stopPropagation()}>
        <div className="flex items-start justify-between gap-4 px-5 py-4 border-b border-border sticky top-0 bg-bg-primary z-10">
          <div>
            <h3 className="text-[16px] font-semibold text-text-primary">{form.id ? t('mcp.edit_title') : t('mcp.add_title')}</h3>
            <p className="text-[11px] text-text-muted mt-1">{t('mcp.form_help')}</p>
          </div>
          <button type="button" onClick={onClose} className="w-7 h-7 rounded-md border border-border bg-bg-card hover:bg-bg-card-hover text-text-secondary cursor-pointer">×</button>
        </div>

        <div className="p-5 space-y-4">
          <label className="block">
            <span className="block text-[11px] font-medium text-text-secondary mb-1.5">{t('mcp.name')}</span>
            <input
              value={form.name}
              onChange={event => set('name', event.target.value)}
              placeholder={t('mcp.name_placeholder')}
              className="w-full h-9 px-3 rounded-md border border-border bg-bg-card text-[13px] text-text-primary outline-none focus:border-notion-blue"
            />
          </label>

          <label className="block">
            <span className="block text-[11px] font-medium text-text-secondary mb-1.5">{t('mcp.server_url')}</span>
            <input
              value={form.url}
              onChange={event => set('url', event.target.value)}
              placeholder="https://example.com/mcp"
              spellCheck={false}
              className="w-full h-9 px-3 rounded-md border border-border bg-bg-card text-[12px] font-mono text-text-primary outline-none focus:border-notion-blue"
            />
          </label>

          <div>
            <span className="block text-[11px] font-medium text-text-secondary mb-1.5">{t('mcp.authentication')}</span>
            <div className="grid grid-cols-3 gap-2">
              {(['none', 'basic', 'bearer'] as MCPAuthType[]).map(authType => (
                <button
                  key={authType}
                  type="button"
                  role="radio"
                  aria-checked={form.auth_type === authType}
                  onClick={() => set('auth_type', authType)}
                  className={`h-9 rounded-md border text-[12px] cursor-pointer transition-colors ${form.auth_type === authType ? 'border-notion-blue bg-notion-blue/10 text-notion-blue' : 'border-border bg-bg-card text-text-secondary hover:bg-bg-card-hover'}`}
                >
                  {t(`mcp.auth_${authType}`)}
                </button>
              ))}
            </div>
          </div>

          {form.auth_type === 'basic' && (
            <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
              <label className="block">
                <span className="block text-[11px] font-medium text-text-secondary mb-1.5">{t('mcp.username')}</span>
                <input
                  value={form.username}
                  onChange={event => set('username', event.target.value)}
                  autoComplete="off"
                  className="w-full h-9 px-3 rounded-md border border-border bg-bg-card text-[13px] text-text-primary outline-none focus:border-notion-blue"
                />
              </label>
              <label className="block">
                <span className="block text-[11px] font-medium text-text-secondary mb-1.5">{t('mcp.password')}</span>
                <input
                  type="password"
                  value={form.password}
                  onChange={event => set('password', event.target.value)}
                  placeholder={form.has_password ? t('mcp.secret_saved_placeholder') : ''}
                  autoComplete="new-password"
                  className="w-full h-9 px-3 rounded-md border border-border bg-bg-card text-[13px] text-text-primary outline-none focus:border-notion-blue"
                />
              </label>
            </div>
          )}

          {form.auth_type === 'bearer' && (
            <label className="block">
              <span className="block text-[11px] font-medium text-text-secondary mb-1.5">{t('mcp.bearer_token')}</span>
              <input
                type="password"
                value={form.bearer_token}
                onChange={event => set('bearer_token', event.target.value)}
                placeholder={form.has_bearer_token ? t('mcp.secret_saved_placeholder') : ''}
                autoComplete="new-password"
                className="w-full h-9 px-3 rounded-md border border-border bg-bg-card text-[13px] text-text-primary outline-none focus:border-notion-blue"
              />
            </label>
          )}

          <button
            type="button"
            role="switch"
            aria-checked={form.run_write_tools_automatically}
            onClick={() => set('run_write_tools_automatically', !form.run_write_tools_automatically)}
            className={`w-full text-left rounded-lg border p-3 cursor-pointer transition-colors ${form.run_write_tools_automatically ? 'border-ok/35 bg-ok/[.06]' : 'border-border bg-bg-card hover:bg-bg-card-hover'}`}
          >
            <div className="flex items-center justify-between gap-3">
              <div>
                <div className="text-[12px] font-semibold text-text-primary">{t('mcp.write_tools_auto')}</div>
                <div className="text-[11px] text-text-muted mt-1">{t('mcp.write_tools_auto_help')}</div>
              </div>
              <span className={`relative w-8 h-[18px] rounded-full shrink-0 transition-colors ${form.run_write_tools_automatically ? 'bg-ok' : 'bg-overlay/15'}`}>
                <span className={`absolute top-[3px] left-[3px] w-3 h-3 rounded-full bg-white transition-transform ${form.run_write_tools_automatically ? 'translate-x-[14px]' : ''}`} />
              </span>
            </div>
          </button>

          <div className="rounded-md border border-warn/20 bg-warn/[.05] px-3 py-2 text-[10px] leading-relaxed text-text-muted">
            {t('mcp.secret_help')}
          </div>

          {error && <div className="text-[11px] text-err break-words">{error}</div>}
        </div>

        <div className="flex justify-end gap-2 px-5 py-4 border-t border-border sticky bottom-0 bg-bg-primary">
          <button type="button" onClick={onClose} disabled={saving} className="h-9 px-4 rounded-md border border-border bg-bg-card hover:bg-bg-card-hover text-[12px] text-text-secondary cursor-pointer disabled:opacity-40">{t('common.cancel')}</button>
          <button type="button" onClick={submit} disabled={saving || !form.name.trim() || !form.url.trim()} className="h-9 px-4 rounded-md border border-notion-blue/30 bg-notion-blue text-on-color text-[12px] font-medium cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed">
            {saving ? t('mcp.saving') : t('common.save')}
          </button>
        </div>
      </div>
    </div>
  )
}

export function MCPManager({ servers, loading, onReload }: MCPManagerProps) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState<MCPFormState | null>(null)
  const [deletingID, setDeletingID] = useState('')
  const [error, setError] = useState('')

  const remove = async (server: MCPServer) => {
    if (!window.confirm(t('mcp.confirm_delete', { name: server.name }))) return
    setDeletingID(server.id)
    setError('')
    try {
      await deleteMCPServer(server.id)
      await onReload()
    } catch (e: any) {
      setError(e?.message || t('mcp.delete_failed'))
    } finally {
      setDeletingID('')
    }
  }

  return (
    <section className="mb-6 px-5 py-5 bg-bg-primary border border-overlay/5 rounded-lg shadow-inner max-sm:px-3 max-sm:py-4">
      <div className="flex items-start justify-between gap-4 mb-4 max-sm:flex-col">
        <div>
          <div className="text-[14px] text-text-primary font-semibold">{t('mcp.title')}</div>
          <div className="text-[11px] text-text-muted mt-1 max-w-[760px] leading-relaxed">{t('mcp.description')}</div>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <button type="button" onClick={() => void onReload()} disabled={loading} className="h-8 px-3 rounded-md border border-border bg-bg-card hover:bg-bg-card-hover text-[11px] text-text-secondary cursor-pointer disabled:opacity-40">
            {loading ? t('common.loading') : t('common.refresh')}
          </button>
          <button type="button" onClick={() => setEditing(emptyMCPForm())} className="h-8 px-3 rounded-md border border-notion-blue/30 bg-notion-blue/10 hover:bg-notion-blue/20 text-[11px] font-medium text-notion-blue cursor-pointer">
            + {t('mcp.add')}
          </button>
        </div>
      </div>

      {error && <div className="mb-3 text-[11px] text-err">{error}</div>}

      {servers.length === 0 ? (
        <button type="button" onClick={() => setEditing(emptyMCPForm())} className="w-full rounded-lg border border-dashed border-border bg-bg-card/40 hover:bg-bg-card p-6 text-center cursor-pointer">
          <div className="text-[13px] font-medium text-text-primary">{t('mcp.empty_title')}</div>
          <div className="text-[11px] text-text-muted mt-1">{t('mcp.empty_help')}</div>
        </button>
      ) : (
        <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
          {servers.map(server => (
            <div key={server.id} className="rounded-lg border border-border bg-bg-card p-3.5">
              <div className="flex items-start gap-3">
                <div className="w-9 h-9 rounded-lg bg-notion-blue/10 border border-notion-blue/20 flex items-center justify-center text-[17px] shrink-0">🤖</div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-[13px] font-semibold text-text-primary truncate">{server.name}</span>
                    <span className="text-[9px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-overlay/[.07] text-text-muted">{t(`mcp.auth_${server.auth_type}`)}</span>
                    {server.run_write_tools_automatically && <span className="text-[9px] px-1.5 py-0.5 rounded bg-ok/10 text-ok">{t('mcp.auto_run_badge')}</span>}
                  </div>
                  <div className="text-[10px] font-mono text-text-muted mt-1 truncate" title={server.url}>{server.url}</div>
                  <div className="text-[10px] text-text-muted mt-1">
                    {server.auth_type === 'basic' && t('mcp.basic_identity', { username: server.username || '—', saved: server.has_password ? t('mcp.saved') : t('mcp.missing') })}
                    {server.auth_type === 'bearer' && t('mcp.bearer_identity', { saved: server.has_bearer_token ? t('mcp.saved') : t('mcp.missing') })}
                    {server.auth_type === 'none' && t('mcp.no_credentials')}
                  </div>
                </div>
                <div className="flex items-center gap-1 shrink-0">
                  <button type="button" onClick={() => setEditing(formFromServer(server))} className="h-7 px-2 rounded border border-border bg-bg-secondary hover:bg-bg-card-hover text-[10px] text-text-secondary cursor-pointer">{t('common.edit')}</button>
                  <button type="button" onClick={() => void remove(server)} disabled={deletingID === server.id} className="h-7 px-2 rounded border border-err/20 bg-err/[.05] hover:bg-err/10 text-[10px] text-err cursor-pointer disabled:opacity-40">{deletingID === server.id ? '…' : t('common.delete')}</button>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {editing && <MCPServerModal initial={editing} onClose={() => setEditing(null)} onSaved={onReload} />}
    </section>
  )
}

export function MCPInstallModal({
  servers,
  selectedCount,
  busy,
  onClose,
  onInstall,
}: {
  servers: MCPServer[]
  selectedCount: number
  busy: boolean
  onClose: () => void
  onInstall: (serverID: string) => Promise<void> | void
}) {
  const { t } = useTranslation()
  const [serverID, setServerID] = useState(servers[0]?.id || '')

  useEffect(() => {
    if (!servers.some(server => server.id === serverID)) setServerID(servers[0]?.id || '')
  }, [servers, serverID])

  return (
    <div className="fixed inset-0 z-[85] bg-black/55 backdrop-blur-[1px] flex items-center justify-center p-4" onMouseDown={onClose}>
      <div className="w-full max-w-[480px] rounded-xl border border-border bg-bg-primary shadow-2xl" onMouseDown={event => event.stopPropagation()}>
        <div className="px-5 py-4 border-b border-border">
          <h3 className="text-[16px] font-semibold text-text-primary">{t('mcp.install_title')}</h3>
          <p className="text-[11px] text-text-muted mt-1">{t('mcp.install_help', { count: selectedCount })}</p>
        </div>
        <div className="p-5">
          {servers.length === 0 ? (
            <div className="rounded-lg border border-warn/25 bg-warn/[.06] p-3 text-[11px] text-text-secondary">{t('mcp.install_empty')}</div>
          ) : (
            <div className="space-y-2">
              {servers.map(server => (
                <button
                  key={server.id}
                  type="button"
                  role="radio"
                  aria-checked={serverID === server.id}
                  onClick={() => setServerID(server.id)}
                  className={`w-full text-left rounded-lg border p-3 cursor-pointer transition-colors ${serverID === server.id ? 'border-notion-blue bg-notion-blue/[.08]' : 'border-border bg-bg-card hover:bg-bg-card-hover'}`}
                >
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="text-[12px] font-semibold text-text-primary">{server.name}</div>
                      <div className="text-[10px] font-mono text-text-muted mt-1 truncate">{server.url}</div>
                    </div>
                    <span className="text-[9px] uppercase text-text-muted shrink-0">{t(`mcp.auth_${server.auth_type}`)}</span>
                  </div>
                </button>
              ))}
            </div>
          )}
        </div>
        <div className="flex justify-end gap-2 px-5 py-4 border-t border-border">
          <button type="button" onClick={onClose} disabled={busy} className="h-9 px-4 rounded-md border border-border bg-bg-card hover:bg-bg-card-hover text-[12px] text-text-secondary cursor-pointer disabled:opacity-40">{t('common.cancel')}</button>
          <button type="button" onClick={() => void onInstall(serverID)} disabled={busy || !serverID} className="h-9 px-4 rounded-md border border-notion-blue/30 bg-notion-blue text-on-color text-[12px] font-medium cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed">
            {busy ? t('actions.starting') : t('mcp.install_action')}
          </button>
        </div>
      </div>
    </div>
  )
}
