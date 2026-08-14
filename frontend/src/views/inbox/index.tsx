'use client'

// React Imports
import { useCallback, useEffect, useMemo, useReducer, useRef } from 'react'

// MUI Imports
import Alert from '@mui/material/Alert'

// Third-party Imports
import classnames from 'classnames'

// Type Imports
import type { Message, MessageType, WSEvent } from '@/types/chatTypes'

// Component Imports
import ChatPanel from './ChatPanel'
import ChatSidebar from './ChatSidebar'
import ConnectionCard from './ConnectionCard'

// Hook Imports
import { useNotificationSound } from './hooks/useNotificationSound'
import { useWhatsAppSocket } from './hooks/useWhatsAppSocket'

// Util Imports
import { fetchChats, fetchMessages, fetchStatus, logout, sendMedia, sendMessage } from '@/utils/whatsappApi'

import { commonLayoutClasses } from '@layouts/utils/layoutClasses'

import { MESSAGE_PAGE_SIZE, initialInboxState, inboxReducer } from './reducer'

const EMPTY_META = { loading: true, error: false, hasMore: false }

const mediaTypeFromFile = (file: File): MessageType => {
  if (file.type.startsWith('image/') && file.type !== 'image/gif') return 'image'
  if (file.type === 'video/mp4') return 'video'

  return 'document'
}

const InboxView = () => {
  // States
  const [state, dispatch] = useReducer(inboxReducer, initialInboxState)

  // Refs (original File per temp id so a failed media send can be retried)
  const pendingFilesRef = useRef(new Map<string, File>())

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
  const { mode: soundMode, setMode: setSoundMode, notify } = useNotificationSound()

  const { live } = useWhatsAppSocket({
    onEvent: (e: WSEvent) => {
      switch (e.type) {
        case 'status':
          dispatch({ type: 'STATUS_CHANGED', payload: e.data })
          break
        case 'qr':
          dispatch({ type: 'QR_RECEIVED', payload: e.data })
          break
        case 'message':
          if (!e.data.fromMe) notify()
          dispatch({ type: 'MESSAGE_RECEIVED', payload: e.data })
          break
        case 'chat_upsert':
          dispatch({ type: 'CHAT_UPSERTED', payload: e.data })
          break
        case 'receipt':
          dispatch({ type: 'RECEIPT_RECEIVED', payload: e.data })
          break
      }
    },

    // Refetch status + chats on every (re)connect to heal missed events
    onOpen: () => {
      refreshStatus()
    }
  })

  // Bootstrap: fetch initial status (chats follow via the connected effect)
  useEffect(() => {
    refreshStatus()
  }, [refreshStatus])

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
      pending: true
    }

    dispatch({ type: 'SEND_OPTIMISTIC', payload: tempMsg })

    sendMessage(jid, text)
      .then(message => dispatch({ type: 'SEND_CONFIRMED', payload: { chatJid: jid, tempId, message } }))
      .catch(() => dispatch({ type: 'SEND_FAILED', payload: { chatJid: jid, tempId } }))
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
      ...(type === 'image' ? { localUrl: URL.createObjectURL(file) } : {})
    }

    pendingFilesRef.current.set(tempId, file)

    dispatch({ type: 'SEND_OPTIMISTIC', payload: tempMsg })

    sendMedia(jid, file)
      .then(message => {
        cleanupMediaTemp(tempId, tempMsg.localUrl)
        dispatch({ type: 'SEND_CONFIRMED', payload: { chatJid: jid, tempId, message } })
      })
      .catch(() => dispatch({ type: 'SEND_FAILED', payload: { chatJid: jid, tempId } }))
  }

  const handleRetryMessage = (message: Message) => {
    const { chatJid, id: tempId, text } = message
    const file = pendingFilesRef.current.get(tempId)

    dispatch({ type: 'SEND_RETRYING', payload: { chatJid, tempId } })

    const resend = file ? sendMedia(chatJid, file) : sendMessage(chatJid, text)

    resend
      .then(serverMsg => {
        if (file) cleanupMediaTemp(tempId, message.localUrl)

        dispatch({ type: 'SEND_CONFIRMED', payload: { chatJid, tempId, message: serverMsg } })
      })
      .catch(() => dispatch({ type: 'SEND_FAILED', payload: { chatJid, tempId } }))
  }

  const handleLoadOlder = (jid: string) => {
    const messages = state.messages[jid] ?? []

    if (messages.length === 0) return

    loadMessages(jid, 'older', messages[0])
  }

  const handleLogout = async () => {
    // Optimistic reset; the backend confirms via a WS status event
    dispatch({ type: 'STATUS_CHANGED', payload: { status: 'waiting_qr', me: null, error: null } })

    try {
      await logout()
    } catch {
      // Ignore; the next status event or refresh will correct the state
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
              me={state.me}
              soundMode={soundMode}
              onSoundModeChange={setSoundMode}
              onSearchChange={value => dispatch({ type: 'SEARCH_CHANGED', payload: value })}
              onSelectChat={jid => dispatch({ type: 'CHAT_SELECTED', payload: jid })}
              onRetry={loadChats}
              onLogout={handleLogout}
            />
            <ChatPanel
              chat={selectedChat}
              messages={selectedMessages}
              meta={selectedMeta}
              onSend={handleSend}
              onSendMedia={handleSendMedia}
              onLoadOlder={handleLoadOlder}
              onRetryInitial={jid => loadMessages(jid, 'initial')}
              onRetryMessage={handleRetryMessage}
            />
          </div>
        </div>
      )}
    </div>
  )
}

export default InboxView
