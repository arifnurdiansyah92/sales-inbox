'use client'

// React Imports
import { Fragment, useLayoutEffect, useRef } from 'react'
import type { UIEvent } from 'react'

// MUI Imports
import Alert from '@mui/material/Alert'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import CircularProgress from '@mui/material/CircularProgress'
import Divider from '@mui/material/Divider'
import Typography from '@mui/material/Typography'

// Type Imports
import type { Message } from '@/types/chatTypes'

// Component Imports
import MessageBubble from './MessageBubble'

// Util Imports
import type { MessagesMeta } from './reducer'
import { formatDayDivider, isSameDay } from './utils'

type Props = {
  chatJid: string
  messages: Message[]
  meta: MessagesMeta
  isGroup: boolean
  onLoadOlder: () => void
  onRetryInitial: () => void
  onRetryMessage: (message: Message) => void
}

const MessageList = ({ chatJid, messages, meta, isGroup, onLoadOlder, onRetryInitial, onRetryMessage }: Props) => {
  // Refs
  const scrollRef = useRef<HTMLDivElement>(null)
  const isAtBottomRef = useRef(true)
  const prevChatJidRef = useRef<string | null>(null)
  const olderAnchorRef = useRef<{ scrollHeight: number; firstId: string } | null>(null)

  // Vars
  const lastMessage = messages.length > 0 ? messages[messages.length - 1] : null
  const lastMessageId = lastMessage ? lastMessage.id : null
  const lastMessageFromMe = lastMessage ? lastMessage.fromMe : false
  const firstMessageId = messages.length > 0 ? messages[0].id : null

  useLayoutEffect(() => {
    const el = scrollRef.current

    if (!el) return

    // Chat switched: jump straight to the bottom
    if (prevChatJidRef.current !== chatJid) {
      prevChatJidRef.current = chatJid
      olderAnchorRef.current = null
      isAtBottomRef.current = true
      el.scrollTop = el.scrollHeight

      return
    }

    // Older page was prepended: keep the viewport anchored
    if (olderAnchorRef.current) {
      if (firstMessageId && firstMessageId !== olderAnchorRef.current.firstId) {
        el.scrollTop += el.scrollHeight - olderAnchorRef.current.scrollHeight
        olderAnchorRef.current = null
      }

      return
    }

    if (isAtBottomRef.current || lastMessageFromMe) {
      el.scrollTop = el.scrollHeight
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chatJid, lastMessageId, firstMessageId, messages.length])

  // Media (images/videos) grow the scroll height after render; re-stick if the user was at the bottom
  const handleMediaLoad = () => {
    const el = scrollRef.current

    if (el && isAtBottomRef.current) el.scrollTop = el.scrollHeight
  }

  const handleScroll = (e: UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget

    isAtBottomRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80

    if (el.scrollTop < 100 && meta.hasMore && !meta.loading && messages.length > 0) {
      olderAnchorRef.current = { scrollHeight: el.scrollHeight, firstId: messages[0].id }
      onLoadOlder()
    }
  }

  if (meta.loading && messages.length === 0) {
    return (
      <div className='flex-auto grid place-items-center min-bs-0'>
        <CircularProgress />
      </div>
    )
  }

  if (meta.error && messages.length === 0) {
    return (
      <div className='flex-auto grid place-items-center min-bs-0'>
        <Alert
          severity='error'
          action={
            <Button color='inherit' size='small' onClick={onRetryInitial}>
              Coba Lagi
            </Button>
          }
        >
          Gagal memuat pesan.
        </Alert>
      </div>
    )
  }

  if (messages.length === 0) {
    return (
      <div className='flex-auto grid place-items-center min-bs-0'>
        <Typography color='text.secondary'>Belum ada pesan di chat ini</Typography>
      </div>
    )
  }

  return (
    <div
      ref={scrollRef}
      onScroll={handleScroll}
      className='flex flex-col flex-auto min-bs-0 overflow-y-auto pli-4 plb-4 gap-1'
    >
      {meta.loading && (
        <div className='flex justify-center plb-2'>
          <CircularProgress size={20} />
        </div>
      )}
      {messages.map((message, index) => {
        const showDivider = index === 0 || !isSameDay(messages[index - 1].timestamp, message.timestamp)

        return (
          <Fragment key={message.id}>
            {showDivider && (
              <Divider className='plb-2'>
                <Chip size='small' label={formatDayDivider(message.timestamp)} />
              </Divider>
            )}
            <MessageBubble message={message} isGroup={isGroup} onRetry={onRetryMessage} onMediaLoad={handleMediaLoad} />
          </Fragment>
        )
      })}
    </div>
  )
}

export default MessageList
