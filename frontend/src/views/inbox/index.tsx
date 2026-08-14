'use client'

// React Imports
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react'

// Next Imports
import { useRouter } from 'next/navigation'

// MUI Imports
import Alert from '@mui/material/Alert'
import Snackbar from '@mui/material/Snackbar'

// Third-party Imports
import classnames from 'classnames'

// Type Imports
import type { Chat, Message, MessageType, WSEvent } from '@/types/chatTypes'

// Component Imports
import ChatPanel from './ChatPanel'
import ChatSidebar from './ChatSidebar'
import ConnectionCard from './ConnectionCard'

// Hook Imports
import { useNotificationSound } from './hooks/useNotificationSound'
import { useWhatsAppSocket } from './hooks/useWhatsAppSocket'

// Util Imports
import {
  ApiError,
  authLogout,
  fetchChats,
  fetchMe,
  fetchMessages,
  fetchStatus,
  logout,
  markChatRead,
  sendMedia,
  sendMessage,
  setChatStatus,
  setOnUnauthorized
} from '@/utils/whatsappApi'

import { commonLayoutClasses } from '@layouts/utils/layoutClasses'

import { MESSAGE_PAGE_SIZE, initialInboxState, inboxReducer } from './reducer'
import { chatStatus } from './utils'

const EMPTY_META = { loading: true, error: false, hasMore: false }

// An optimistic send still pending after this long is marked failed
const PENDING_FAIL_TIMEOUT_MS = 30000

// On WS reconnect, pending sends older than this are swept to failed
const PENDING_SWEEP_AGE_MS = 10000

const mediaTypeFromFile = (file: File): MessageType => {
  if (file.type.startsWith('image/') && file.type !== 'image/gif') return 'image'
  if (file.type === 'video/mp4') return 'video'

  return 'document'
}

const InboxView = () => {
  // States
  const [state, dispatch] = useReducer(inboxReducer, initialInboxState)
  const [snackbarMessage, setSnackbarMessage] = useState<string | null>(null)

  // Refs (original File per temp id so a failed media send can be retried)
  const pendingFilesRef = useRef(new Map<string, File>())

  // Refs (pending→failed timers per temp id, cleared when the server responds)
  const failTimersRef = useRef(new Map<string, ReturnType<typeof setTimeout>>())

  // Refs (latest selection for WS callbacks that outlive the render)
  const selectedJidRef = useRef<string | null>(null)

  selectedJidRef.current = state.selectedJid

  const loadChats = useCallback(async () => {
    dispatch({ type: 'CHATS_LOADING' })

    try {
      const chats = await fetchChats()

      dispatch({ type: 'CHATS_LOADED', payload: chats })
    } catch {
      dispatch({ type: 'CHATS_FAILED' })
    }
  }, [])

  const loadMessages = useCallback(async (jid: string, mode: 'initial' | 'older', before?: Message) => {
    dispatch({ type: 'MESSAGES_LOADING', payload: { jid } })

    try {
      const messages = await fetchMessages(
        jid,
        before
          ? { limit: MESSAGE_PAGE_SIZE, beforeTs: before.timestamp, beforeId: before.id }
          : { limit: MESSAGE_PAGE_SIZE }
      )

      dispatch({ type: 'MESSAGES_LOADED', payload: { jid, messages, mode } })
    } catch {
      dispatch({ type: 'MESSAGES_FAILED', payload: { jid } })
    }
  }, [])

  const refreshStatus = useCallback(async () => {
    try {
      const status = await fetchStatus()

      dispatch({ type: 'STATUS_CHANGED', payload: status })

      if (status.status === 'connected') loadChats()
    } catch {
      dispatch({
        type: 'STATUS_CHANGED',
        payload: { status: 'disconnected', me: null, error: 'Tidak dapat menghubungi server.' }
      })
    }
  }, [loadChats])

  // Hooks
  const router = useRouter()
  const { mode: soundMode, setMode: setSoundMode, notify } = useNotificationSound()

  const { live, send } = useWhatsAppSocket({
    onEvent: (e: WSEvent) => {
      switch (e.type) {
        case 'status':
          dispatch({ type: 'STATUS_CHANGED', payload: e.data })
          break
        case 'qr':
          dispatch({ type: 'QR_RECEIVED', payload: e.data })
          break

        case 'message': {
          const activeAndFocused = e.data.chatJid === selectedJidRef.current && document.hasFocus()

          // Mute the sound for the chat the admin is actively reading; mark it read for the team instead
          if (!e.data.fromMe && !activeAndFocused) notify()
          if (!e.data.fromMe && activeAndFocused) markChatRead(e.data.chatJid).catch(() => undefined)

          dispatch({ type: 'MESSAGE_RECEIVED', payload: e.data })
          break
        }

        case 'chat_upsert':
          dispatch({ type: 'CHAT_UPSERTED', payload: e.data })
          break
        case 'receipt':
          dispatch({ type: 'RECEIPT_RECEIVED', payload: e.data })
          break
        case 'presence':
          dispatch({ type: 'PRESENCE_CHANGED', payload: e.data.viewers })
          break
      }
    },

    // Refetch status + chats on every (re)connect to heal missed events
    onOpen: () => {
      refreshStatus()
      send({ type: 'viewing', data: { chatJid: selectedJidRef.current } })
      dispatch({ type: 'PENDING_SWEPT', payload: { olderThanTs: Date.now() - PENDING_SWEEP_AGE_MS } })
    }
  })

  // Redirect to /login whenever any request hits a 401 mid-session
  useEffect(() => {
    setOnUnauthorized(() => router.replace('/login'))

    return () => setOnUnauthorized(null)
  }, [router])

  // Bootstrap: verify the admin session first, then fetch status (chats follow via the connected effect)
  useEffect(() => {
    let cancelled = false

    fetchMe()
      .then(admin => {
        if (cancelled) return

        dispatch({ type: 'ADMIN_LOADED', payload: admin })
        refreshStatus()
      })
      .catch(() => {
        if (!cancelled) router.replace('/login')
      })

    return () => {
      cancelled = true
    }
  }, [refreshStatus, router])

  // Clear any armed pending→failed timers on unmount
  useEffect(() => {
    const timers = failTimersRef.current

    return () => {
      for (const timer of timers.values()) clearTimeout(timer)
      timers.clear()
    }
  }, [])

  // Keep reducer state in sync with the socket liveness
  useEffect(() => {
    dispatch({ type: 'WS_LIVE_CHANGED', payload: live })
  }, [live])

  // Fetch initial messages when selecting a chat that has never been loaded
  useEffect(() => {
    if (state.status !== 'connected' || !state.selectedJid) return

    if (!state.messagesMeta[state.selectedJid]) {
      loadMessages(state.selectedJid, 'initial')
    }
  }, [state.status, state.selectedJid, state.messagesMeta, loadMessages])

  // Vars
  const selectedChat = useMemo(
    () => state.chats.find(chat => chat.jid === state.selectedJid) ?? null,
    [state.chats, state.selectedJid]
  )

  const selectedMessages = state.selectedJid ? (state.messages[state.selectedJid] ?? []) : []
  const selectedMeta = state.selectedJid ? (state.messagesMeta[state.selectedJid] ?? EMPTY_META) : EMPTY_META

  // Other admins' presence (own entry filtered out by adminId)
  const otherViewers = useMemo(
    () => state.viewers.filter(viewer => viewer.adminId !== state.admin?.id),
    [state.viewers, state.admin]
  )

  const selectedViewerNames = useMemo(
    () => otherViewers.filter(viewer => viewer.chatJid === state.selectedJid).map(viewer => viewer.adminName),
    [otherViewers, state.selectedJid]
  )

  const clearFailTimer = (tempId: string) => {
    const timer = failTimersRef.current.get(tempId)

    if (timer) {
      clearTimeout(timer)
      failTimersRef.current.delete(tempId)
    }
  }

  const armFailTimer = (chatJid: string, tempId: string) => {
    clearFailTimer(tempId)

    const timer = setTimeout(() => {
      failTimersRef.current.delete(tempId)
      dispatch({ type: 'SEND_FAILED', payload: { chatJid, tempId } })
    }, PENDING_FAIL_TIMEOUT_MS)

    failTimersRef.current.set(tempId, timer)
  }

  const handleSelectChat = (jid: string) => {
    dispatch({ type: 'CHAT_SELECTED', payload: jid })
    send({ type: 'viewing', data: { chatJid: jid } })

    // Fire-and-forget: the server zeroes unread for the whole team and broadcasts chat_upsert
    markChatRead(jid).catch(() => undefined)
  }

  const handleSend = (text: string) => {
    const jid = state.selectedJid

    if (!jid) return

    const tempId = `temp-${crypto.randomUUID()}`

    const tempMsg: Message = {
      id: tempId,
      chatJid: jid,
      senderJid: state.me?.jid ?? '',
      senderName: state.me?.name ?? '',
      fromMe: true,
      text,
      type: 'text',
      timestamp: Date.now(),
      pending: true,
      ...(state.admin ? { adminId: state.admin.id, adminName: state.admin.name } : {})
    }

    dispatch({ type: 'SEND_OPTIMISTIC', payload: tempMsg })
    armFailTimer(jid, tempId)

    sendMessage(jid, text)
      .then(message => {
        clearFailTimer(tempId)
        dispatch({ type: 'SEND_CONFIRMED', payload: { chatJid: jid, tempId, message } })
      })
      .catch(() => {
        clearFailTimer(tempId)
        dispatch({ type: 'SEND_FAILED', payload: { chatJid: jid, tempId } })
      })
  }

  const cleanupMediaTemp = (tempId: string, localUrl?: string) => {
    pendingFilesRef.current.delete(tempId)

    if (localUrl) URL.revokeObjectURL(localUrl)
  }

  const handleSendMedia = (file: File) => {
    const jid = state.selectedJid

    if (!jid) return

    const tempId = `temp-${crypto.randomUUID()}`
    const type = mediaTypeFromFile(file)

    const tempMsg: Message = {
      id: tempId,
      chatJid: jid,
      senderJid: state.me?.jid ?? '',
      senderName: state.me?.name ?? '',
      fromMe: true,
      text: '',
      type,
      timestamp: Date.now(),
      mimeType: file.type,
      fileName: file.name,
      fileSize: file.size,
      pending: true,
      ...(state.admin ? { adminId: state.admin.id, adminName: state.admin.name } : {}),
      ...(type === 'image' ? { localUrl: URL.createObjectURL(file) } : {})
    }

    pendingFilesRef.current.set(tempId, file)

    dispatch({ type: 'SEND_OPTIMISTIC', payload: tempMsg })
    armFailTimer(jid, tempId)

    sendMedia(jid, file)
      .then(message => {
        clearFailTimer(tempId)
        cleanupMediaTemp(tempId, tempMsg.localUrl)
        dispatch({ type: 'SEND_CONFIRMED', payload: { chatJid: jid, tempId, message } })
      })
      .catch(() => {
        clearFailTimer(tempId)
        dispatch({ type: 'SEND_FAILED', payload: { chatJid: jid, tempId } })
      })
  }

  const handleRetryMessage = (message: Message) => {
    const { chatJid, id: tempId, text } = message
    const file = pendingFilesRef.current.get(tempId)

    dispatch({ type: 'SEND_RETRYING', payload: { chatJid, tempId } })
    armFailTimer(chatJid, tempId)

    const resend = file ? sendMedia(chatJid, file) : sendMessage(chatJid, text)

    resend
      .then(serverMsg => {
        clearFailTimer(tempId)

        if (file) cleanupMediaTemp(tempId, message.localUrl)

        dispatch({ type: 'SEND_CONFIRMED', payload: { chatJid, tempId, message: serverMsg } })
      })
      .catch(() => {
        clearFailTimer(tempId)
        dispatch({ type: 'SEND_FAILED', payload: { chatJid, tempId } })
      })
  }

  const handleLoadOlder = (jid: string) => {
    const messages = state.messages[jid] ?? []

    if (messages.length === 0) return

    loadMessages(jid, 'older', messages[0])
  }

  const handleToggleStatus = (chat: Chat) => {
    const nextStatus = chatStatus(chat) === 'open' ? 'resolved' : 'open'

    // Optimistic flip; the server response (or a revert on failure) lands via the same action
    dispatch({ type: 'CHAT_UPSERTED', payload: { ...chat, status: nextStatus } })

    setChatStatus(chat.jid, nextStatus)
      .then(updated => dispatch({ type: 'CHAT_UPSERTED', payload: updated }))
      .catch(() => {
        dispatch({ type: 'CHAT_UPSERTED', payload: chat })
        setSnackbarMessage('Gagal mengubah status chat. Coba lagi.')
      })
  }

  const handleAdminLogout = async () => {
    try {
      await authLogout()
    } catch {
      // Ignore; the session cookie is gone or the redirect below sorts it out
    }

    router.replace('/login')
  }

  const handleLogout = async () => {
    try {
      await logout()

      // Optimistic reset; the backend confirms via a WS status event
      dispatch({ type: 'STATUS_CHANGED', payload: { status: 'waiting_qr', me: null, error: null } })
    } catch (error) {
      if (error instanceof ApiError && error.status === 403) {
        setSnackbarMessage('Hanya owner yang bisa memutuskan koneksi WhatsApp')
      }
    }
  }

  return (
    <div
      className={classnames(
        commonLayoutClasses.contentHeightFixed,
        'flex is-full overflow-hidden rounded-xl border border-divider bg-backgroundPaper'
      )}
    >
      {state.status !== 'connected' ? (
        <ConnectionCard status={state.status} qr={state.qr} statusError={state.statusError} />
      ) : (
        <div className='flex flex-col flex-auto min-is-0 min-bs-0'>
          {!state.wsLive && (
            <Alert severity='warning' className='rounded-none shrink-0'>
              Koneksi terputus. Menyambungkan ulang…
            </Alert>
          )}
          <div className='flex flex-auto min-is-0 min-bs-0'>
            <ChatSidebar
              chats={state.chats}
              loading={state.chatsLoading}
              error={state.chatsError}
              selectedJid={state.selectedJid}
              search={state.search}
              statusFilter={state.statusFilter}
              me={state.me}
              admin={state.admin}
              viewers={otherViewers}
              soundMode={soundMode}
              onSoundModeChange={setSoundMode}
              onSearchChange={value => dispatch({ type: 'SEARCH_CHANGED', payload: value })}
              onStatusFilterChange={value => dispatch({ type: 'STATUS_FILTER_CHANGED', payload: value })}
              onSelectChat={handleSelectChat}
              onRetry={loadChats}
              onLogout={handleLogout}
              onAdminLogout={handleAdminLogout}
            />
            <ChatPanel
              chat={selectedChat}
              messages={selectedMessages}
              meta={selectedMeta}
              viewerNames={selectedViewerNames}
              onSend={handleSend}
              onSendMedia={handleSendMedia}
              onLoadOlder={handleLoadOlder}
              onRetryInitial={jid => loadMessages(jid, 'initial')}
              onRetryMessage={handleRetryMessage}
              onToggleStatus={handleToggleStatus}
            />
          </div>
        </div>
      )}
      <Snackbar
        open={snackbarMessage !== null}
        autoHideDuration={4000}
        onClose={() => setSnackbarMessage(null)}
        message={snackbarMessage}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
      />
    </div>
  )
}

export default InboxView
