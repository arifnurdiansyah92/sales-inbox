// Type Imports
import type { AdminUser, Chat, ChatStatus, Message, StatusPayload } from '@/types/chatTypes'

// Kosongkan NEXT_PUBLIC_API_URL untuk mode same-origin (produksi di belakang Caddy)
const API_BASE = process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:8080'

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

// Registered by the inbox view so a mid-session 401 can redirect to /login
let onUnauthorized: (() => void) | null = null

export const setOnUnauthorized = (handler: (() => void) | null): void => {
  onUnauthorized = handler
}

export const getWsUrl = (): string => {
  if (API_BASE) return `${API_BASE.replace(/^http/, 'ws')}/ws`

  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'

  return `${proto}://${window.location.host}/ws`
}

export const avatarUrl = (jid: string): string => `${API_BASE}/api/avatar/${encodeURIComponent(jid)}`

export const mediaUrl = (chatJid: string, messageId: string): string =>
  `${API_BASE}/api/media/${encodeURIComponent(chatJid)}/${encodeURIComponent(messageId)}`

const request = async <T>(path: string, init?: RequestInit): Promise<T> => {
  // Cookies carry the session; 'include' is needed for the cross-port dev setup
  const res = await fetch(`${API_BASE}${path}`, { ...init, credentials: 'include' })

  if (!res.ok) {
    let message = `Permintaan gagal (${res.status})`

    try {
      const body = await res.json()

      if (body && typeof body.error === 'string' && body.error.length > 0) {
        message = body.error
      }
    } catch {
      // Body is not parseable JSON, keep the fallback message
    }

    if (res.status === 401) onUnauthorized?.()

    throw new ApiError(res.status, message)
  }

  const text = await res.text()

  return (text ? JSON.parse(text) : undefined) as T
}

export const login = (username: string, password: string): Promise<AdminUser> =>
  request<AdminUser>('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password })
  })

export const authLogout = (): Promise<void> => request<void>('/api/auth/logout', { method: 'POST' })

export const fetchMe = (): Promise<AdminUser> => request<AdminUser>('/api/auth/me')

export const fetchStatus = (): Promise<StatusPayload> => request<StatusPayload>('/api/status')

export const fetchChats = (): Promise<Chat[]> => request<Chat[]>('/api/chats')

export const fetchMessages = (
  jid: string,
  opts?: { limit?: number; beforeTs?: number; beforeId?: string }
): Promise<Message[]> => {
  const params = new URLSearchParams()

  if (opts?.limit !== undefined) params.set('limit', String(opts.limit))
  if (opts?.beforeTs !== undefined) params.set('beforeTs', String(opts.beforeTs))
  if (opts?.beforeId !== undefined) params.set('beforeId', opts.beforeId)

  const query = params.toString()

  return request<Message[]>(`/api/chats/${encodeURIComponent(jid)}/messages${query ? `?${query}` : ''}`)
}

export const sendMessage = (jid: string, text: string): Promise<Message> =>
  request<Message>(`/api/chats/${encodeURIComponent(jid)}/messages`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text })
  })

export const sendMedia = (jid: string, file: File, caption?: string): Promise<Message> => {
  const form = new FormData()

  form.append('file', file)

  if (caption) form.append('caption', caption)

  // No manual Content-Type header: the browser sets the multipart boundary itself
  return request<Message>(`/api/chats/${encodeURIComponent(jid)}/media`, { method: 'POST', body: form })
}

export const markChatRead = (jid: string): Promise<void> =>
  request<void>(`/api/chats/${encodeURIComponent(jid)}/read`, { method: 'POST' })

export const setChatStatus = (jid: string, status: ChatStatus): Promise<Chat> =>
  request<Chat>(`/api/chats/${encodeURIComponent(jid)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ status })
  })

export const logout = (): Promise<void> => request<void>('/api/logout', { method: 'POST' })
