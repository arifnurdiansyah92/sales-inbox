// Type Imports
import type { MessageType } from '@/types/chatTypes'

const DAY_MS = 24 * 60 * 60 * 1000

export const isSameDay = (a: number, b: number): boolean => {
  const dateA = new Date(a)
  const dateB = new Date(b)

  return (
    dateA.getFullYear() === dateB.getFullYear() &&
    dateA.getMonth() === dateB.getMonth() &&
    dateA.getDate() === dateB.getDate()
  )
}

export const formatClock = (ms: number): string =>
  new Intl.DateTimeFormat('id-ID', { hour: '2-digit', minute: '2-digit' }).format(ms)

export const formatListTime = (ms: number): string => {
  const now = Date.now()

  if (isSameDay(ms, now)) return formatClock(ms)
  if (isSameDay(ms, now - DAY_MS)) return 'Kemarin'

  return new Intl.DateTimeFormat('id-ID', { day: '2-digit', month: '2-digit', year: 'numeric' }).format(ms)
}

export const formatDayDivider = (ms: number): string => {
  const now = Date.now()

  if (isSameDay(ms, now)) return 'Hari Ini'
  if (isSameDay(ms, now - DAY_MS)) return 'Kemarin'

  return new Intl.DateTimeFormat('id-ID', { dateStyle: 'long' }).format(ms)
}

const TYPE_LABELS: Partial<Record<MessageType, string>> = {
  image: 'Foto',
  video: 'Video',
  document: 'Dokumen',
  audio: 'Pesan suara',
  sticker: 'Stiker',
  unknown: 'Pesan tidak didukung'
}

export const messageTypeLabel = (type: MessageType): string => TYPE_LABELS[type] ?? 'Pesan tidak didukung'

const sizeFormatter = new Intl.NumberFormat('id-ID', { maximumFractionDigits: 1 })

export const formatFileSize = (bytes: number): string => {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${sizeFormatter.format(bytes / 1024)} KB`

  return `${sizeFormatter.format(bytes / (1024 * 1024))} MB`
}

const SENDER_COLORS = [
  'primary.main',
  'secondary.main',
  'error.main',
  'warning.main',
  'info.main',
  'success.main',
  'primary.dark',
  'success.dark'
]

export const senderColor = (jid: string): string => {
  let hash = 0

  for (let i = 0; i < jid.length; i++) {
    hash = (hash * 31 + jid.charCodeAt(i)) | 0
  }

  return SENDER_COLORS[Math.abs(hash) % SENDER_COLORS.length]
}
