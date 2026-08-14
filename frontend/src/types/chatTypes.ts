export type ConnectionStatus = 'disconnected' | 'connecting' | 'waiting_qr' | 'connected'

export type Me = { jid: string; name: string; phone: string }

export type StatusPayload = { status: ConnectionStatus; me: Me | null; error: string | null }

export type MessageType = 'text' | 'image' | 'video' | 'document' | 'audio' | 'sticker' | 'unknown'

export type MessageStatus = '' | 'sent' | 'delivered' | 'read'

export type ChatStatus = 'open' | 'resolved'

export type AdminUser = { id: number; username: string; name: string; isOwner: boolean }

export type PresenceViewer = { chatJid: string; adminId: number; adminName: string }

export type Chat = {
  jid: string
  name: string
  isGroup: boolean
  lastMessage: string
  lastMessageAt: number
  unreadCount: number
  status?: ChatStatus
  pinned?: boolean
}

export type Message = {
  id: string
  chatJid: string
  senderJid: string
  senderName: string
  fromMe: boolean
  text: string
  type: MessageType
  timestamp: number
  mimeType?: string
  fileName?: string
  fileSize?: number
  hasMedia?: boolean
  status?: MessageStatus
  adminId?: number
  adminName?: string
  pending?: boolean
  failed?: boolean
  localUrl?: string
}

export type WSEvent =
  | { type: 'status'; data: StatusPayload }
  | { type: 'qr'; data: { dataUrl: string; timeoutMs: number } }
  | { type: 'message'; data: Message }
  | { type: 'chat_upsert'; data: Chat }
  | { type: 'receipt'; data: { chatJid: string; ids: string[]; status: 'delivered' | 'read' } }
  | { type: 'presence'; data: { viewers: PresenceViewer[] } }

// The only client→server frame: which chat this admin currently has open (null = none)
export type WSClientFrame = { type: 'viewing'; data: { chatJid: string | null } }
