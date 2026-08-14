// Type Imports
import type { Chat, Message, StatusPayload } from '@/types/chatTypes'

const API_BASE = process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:8080'

export const getWsUrl = (): string => `${API_BASE.replace(/^http/, 'ws')}/ws`

export const avatarUrl = (jid: string): string => `${API_BASE}/api/avatar/${encodeURIComponent(jid)}`

export const mediaUrl = (chatJid: string, messageId: string): string =>
  `${API_BASE}/api/media/${encodeURIComponent(chatJid)}/${encodeURIComponent(messageId)}`

const request = async <T>(path: string, init?: RequestInit): Promise<T> => {
  const res = await fetch(`${API_BASE}${path}`, init)

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

    throw new Error(message)
  }

  const text = await res.text()

  return (text ? JSON.parse(text) : undefined) as T
}

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

export const logout = (): Promise<void> => request<void>('/api/logout', { method: 'POST' })
