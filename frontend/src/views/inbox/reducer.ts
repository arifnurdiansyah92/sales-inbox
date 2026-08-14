// Type Imports
import type {
  AdminUser,
  Chat,
  ChatStatus,
  ConnectionStatus,
  Me,
  Message,
  MessageStatus,
  PresenceViewer,
  StatusPayload
} from '@/types/chatTypes'

export const MESSAGE_PAGE_SIZE = 50

export type MessagesMeta = { loading: boolean; error: boolean; hasMore: boolean }

export type ChatStatusFilter = ChatStatus | 'all'

export type InboxState = {
  status: ConnectionStatus
  me: Me | null
  admin: AdminUser | null
  statusError: string | null
  qr: { dataUrl: string; timeoutMs: number } | null
  wsLive: boolean
  chats: Chat[]
  chatsLoading: boolean
  chatsError: boolean
  selectedJid: string | null
  search: string
  statusFilter: ChatStatusFilter
  viewers: PresenceViewer[]
  messages: Record<string, Message[]>
  messagesMeta: Record<string, MessagesMeta>
}

export type InboxAction =
  | { type: 'STATUS_CHANGED'; payload: StatusPayload }
  | { type: 'ADMIN_LOADED'; payload: AdminUser }
  | { type: 'QR_RECEIVED'; payload: { dataUrl: string; timeoutMs: number } }
  | { type: 'WS_LIVE_CHANGED'; payload: boolean }
  | { type: 'SEARCH_CHANGED'; payload: string }
  | { type: 'STATUS_FILTER_CHANGED'; payload: ChatStatusFilter }
  | { type: 'PRESENCE_CHANGED'; payload: PresenceViewer[] }
  | { type: 'CHATS_LOADING' }
  | { type: 'CHATS_LOADED'; payload: Chat[] }
  | { type: 'CHATS_FAILED' }
  | { type: 'CHAT_UPSERTED'; payload: Chat }
  | { type: 'CHAT_SELECTED'; payload: string }
  | { type: 'MESSAGES_LOADING'; payload: { jid: string } }
  | { type: 'MESSAGES_LOADED'; payload: { jid: string; messages: Message[]; mode: 'initial' | 'older' } }
  | { type: 'MESSAGES_FAILED'; payload: { jid: string } }
  | { type: 'MESSAGE_RECEIVED'; payload: Message }
  | { type: 'SEND_OPTIMISTIC'; payload: Message }
  | { type: 'SEND_CONFIRMED'; payload: { chatJid: string; tempId: string; message: Message } }
  | { type: 'SEND_FAILED'; payload: { chatJid: string; tempId: string } }
  | { type: 'SEND_RETRYING'; payload: { chatJid: string; tempId: string } }
  | { type: 'PENDING_SWEPT'; payload: { olderThanTs: number } }
  | { type: 'RECEIPT_RECEIVED'; payload: { chatJid: string; ids: string[]; status: 'delivered' | 'read' } }

export const initialInboxState: InboxState = {
  status: 'connecting',
  me: null,
  admin: null,
  statusError: null,
  qr: null,
  wsLive: false,
  chats: [],
  chatsLoading: false,
  chatsError: false,
  selectedJid: null,
  search: '',
  statusFilter: 'open',
  viewers: [],
  messages: {},
  messagesMeta: {}
}

// Shared comparator: ascending by timestamp, then by id string compare
export const compareMessages = (a: Message, b: Message): number => {
  if (a.timestamp !== b.timestamp) return a.timestamp - b.timestamp
  if (a.id < b.id) return -1
  if (a.id > b.id) return 1

  return 0
}

const sortChats = (chats: Chat[]): Chat[] => [...chats].sort((a, b) => b.lastMessageAt - a.lastMessageAt)

const upsertChat = (chats: Chat[], chat: Chat): Chat[] => {
  const exists = chats.some(c => c.jid === chat.jid)
  const next = exists ? chats.map(c => (c.jid === chat.jid ? { ...c, ...chat } : c)) : [...chats, chat]

  return sortChats(next)
}

// Delivery status only ever upgrades along this rank; receipts can arrive out of order
const STATUS_RANK: Record<MessageStatus, number> = { '': 0, sent: 1, delivered: 2, read: 3 }

const mergeMessages = (existing: Message[], incoming: Message[]): Message[] => {
  const byId = new Map<string, Message>()

  for (const msg of existing) byId.set(msg.id, msg)
  for (const msg of incoming) byId.set(msg.id, msg)

  return [...byId.values()].sort(compareMessages)
}

export const inboxReducer = (state: InboxState, action: InboxAction): InboxState => {
  switch (action.type) {
    case 'STATUS_CHANGED': {
      const { status, me, error } = action.payload

      let next: InboxState = { ...state, status, me, statusError: error }

      // Clear data only when a fresh QR pairing starts. Keep data through 'connecting'/'disconnected' blips.
      if (status === 'waiting_qr' && state.status !== 'waiting_qr') {
        next = {
          ...next,
          chats: [],
          chatsLoading: false,
          chatsError: false,
          selectedJid: null,
          messages: {},
          messagesMeta: {}
        }
      }

      if (status === 'connected') {
        next = { ...next, qr: null }
      }

      return next
    }

    case 'ADMIN_LOADED':
      return { ...state, admin: action.payload }

    case 'QR_RECEIVED':
      return { ...state, qr: action.payload }

    case 'WS_LIVE_CHANGED':
      return { ...state, wsLive: action.payload }

    case 'SEARCH_CHANGED':
      return { ...state, search: action.payload }

    case 'STATUS_FILTER_CHANGED':
      return { ...state, statusFilter: action.payload }

    case 'PRESENCE_CHANGED':
      return { ...state, viewers: action.payload }

    case 'CHATS_LOADING':
      return { ...state, chatsLoading: true, chatsError: false }

    case 'CHATS_LOADED':
      return { ...state, chats: sortChats(action.payload), chatsLoading: false, chatsError: false }

    case 'CHATS_FAILED':
      return { ...state, chatsLoading: false, chatsError: true }

    case 'CHAT_UPSERTED': {
      const chat = action.payload.jid === state.selectedJid ? { ...action.payload, unreadCount: 0 } : action.payload

      return { ...state, chats: upsertChat(state.chats, chat) }
    }

    case 'CHAT_SELECTED': {
      const chats = state.chats.map(c => (c.jid === action.payload ? { ...c, unreadCount: 0 } : c))

      return { ...state, selectedJid: action.payload, chats }
    }

    case 'MESSAGES_LOADING': {
      const { jid } = action.payload
      const meta = state.messagesMeta[jid] ?? { loading: false, error: false, hasMore: true }

      return { ...state, messagesMeta: { ...state.messagesMeta, [jid]: { ...meta, loading: true, error: false } } }
    }

    case 'MESSAGES_LOADED': {
      const { jid, messages } = action.payload
      const merged = mergeMessages(state.messages[jid] ?? [], messages)
      const hasMore = messages.length === MESSAGE_PAGE_SIZE

      return {
        ...state,
        messages: { ...state.messages, [jid]: merged },
        messagesMeta: { ...state.messagesMeta, [jid]: { loading: false, error: false, hasMore } }
      }
    }

    case 'MESSAGES_FAILED': {
      const { jid } = action.payload
      const meta = state.messagesMeta[jid] ?? { loading: false, error: false, hasMore: true }

      return { ...state, messagesMeta: { ...state.messagesMeta, [jid]: { ...meta, loading: false, error: true } } }
    }

    case 'MESSAGE_RECEIVED': {
      const msg = action.payload
      const existing = state.messages[msg.chatJid] ?? []

      if (existing.some(m => m.id === msg.id)) return state

      const messages = { ...state.messages, [msg.chatJid]: [...existing, msg].sort(compareMessages) }
      const current = state.chats.find(c => c.jid === msg.chatJid)
      const isSelected = msg.chatJid === state.selectedJid

      const chat: Chat = current
        ? {
            ...current,
            lastMessage: msg.text,
            lastMessageAt: msg.timestamp,
            unreadCount: isSelected ? 0 : msg.fromMe ? current.unreadCount : current.unreadCount + 1
          }
        : {
            jid: msg.chatJid,
            name: msg.fromMe ? msg.chatJid.split('@')[0] : msg.senderName || msg.chatJid.split('@')[0],
            isGroup: msg.chatJid.endsWith('@g.us'),
            lastMessage: msg.text,
            lastMessageAt: msg.timestamp,
            unreadCount: msg.fromMe || isSelected ? 0 : 1
          }

      return { ...state, messages, chats: upsertChat(state.chats, chat) }
    }

    case 'SEND_OPTIMISTIC': {
      const msg = action.payload
      const existing = state.messages[msg.chatJid] ?? []

      return { ...state, messages: { ...state.messages, [msg.chatJid]: [...existing, msg] } }
    }

    case 'SEND_CONFIRMED': {
      const { chatJid, tempId, message } = action.payload
      const existing = state.messages[chatJid] ?? []

      const next = existing.some(m => m.id === message.id)
        ? existing.filter(m => m.id !== tempId)
        : existing.map(m => (m.id === tempId ? message : m)).sort(compareMessages)

      return { ...state, messages: { ...state.messages, [chatJid]: next } }
    }

    case 'SEND_FAILED': {
      const { chatJid, tempId } = action.payload
      const existing = state.messages[chatJid] ?? []
      const next = existing.map(m => (m.id === tempId ? { ...m, pending: false, failed: true } : m))

      return { ...state, messages: { ...state.messages, [chatJid]: next } }
    }

    case 'SEND_RETRYING': {
      const { chatJid, tempId } = action.payload
      const existing = state.messages[chatJid] ?? []
      const next = existing.map(m => (m.id === tempId ? { ...m, pending: true, failed: false } : m))

      return { ...state, messages: { ...state.messages, [chatJid]: next } }
    }

    case 'PENDING_SWEPT': {
      // Reconnect sweep: optimistic sends the server never confirmed are marked failed
      const { olderThanTs } = action.payload
      const messages: Record<string, Message[]> = {}
      let changed = false

      for (const [jid, list] of Object.entries(state.messages)) {
        let listChanged = false

        const next = list.map(m => {
          if (!m.pending || !m.id.startsWith('temp-') || m.timestamp >= olderThanTs) return m

          listChanged = true

          return { ...m, pending: false, failed: true }
        })

        messages[jid] = listChanged ? next : list

        if (listChanged) changed = true
      }

      if (!changed) return state

      return { ...state, messages }
    }

    case 'RECEIPT_RECEIVED': {
      const { chatJid, ids, status } = action.payload
      const existing = state.messages[chatJid]

      if (!existing) return state

      const idSet = new Set(ids)
      const rank = STATUS_RANK[status]
      let changed = false

      const next = existing.map(m => {
        if (!m.fromMe || !idSet.has(m.id) || STATUS_RANK[m.status ?? ''] >= rank) return m

        changed = true

        return { ...m, status }
      })

      if (!changed) return state

      return { ...state, messages: { ...state.messages, [chatJid]: next } }
    }

    default:
      return state
  }
}
