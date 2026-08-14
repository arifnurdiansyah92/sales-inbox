'use client'

// React Imports
import { useState } from 'react'

// MUI Imports
import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'

// Third-party Imports
import classnames from 'classnames'

// Type Imports
import type { Message, MessageType } from '@/types/chatTypes'

// Util Imports
import { mediaUrl } from '@/utils/whatsappApi'
import { formatClock, formatFileSize, messageTypeLabel, senderColor } from './utils'

type Props = {
  message: Message
  isGroup: boolean
  onRetry: (message: Message) => void
  onMediaLoad: () => void
}

const TYPE_ICONS: Partial<Record<MessageType, string>> = {
  image: 'tabler-photo',
  video: 'tabler-video',
  document: 'tabler-file',
  audio: 'tabler-microphone',
  sticker: 'tabler-sticker',
  unknown: 'tabler-file'
}

const VISUAL_TYPES: MessageType[] = ['image', 'video', 'sticker']

const MessageBubble = ({ message, isGroup, onRetry, onMediaLoad }: Props) => {
  // States
  const [mediaError, setMediaError] = useState(false)

  // Vars
  const isTemp = message.id.startsWith('temp-')
  const remoteUrl = mediaUrl(message.chatJid, message.id)
  const hasMediaSource = Boolean(message.hasMedia || message.localUrl)
  const showMedia = message.type !== 'text' && hasMediaSource && !mediaError

  // Stickers render without the colored bubble background
  const isBareSticker = message.type === 'sticker' && showMedia
  const openMedia = isTemp ? undefined : () => window.open(remoteUrl, '_blank')

  const renderMedia = () => {
    switch (message.type) {
      case 'image':
      case 'sticker':
        return (
          <img
            src={message.localUrl ?? remoteUrl}
            loading='lazy'
            alt=''
            className={classnames(
              'max-bs-[320px] object-contain rounded',
              message.type === 'sticker' ? 'max-is-[160px]' : 'max-is-[280px]',
              { 'cursor-pointer': !isTemp }
            )}
            onClick={openMedia}
            onLoad={onMediaLoad}
            onError={() => setMediaError(true)}
          />
        )
      case 'video':
        return (
          <video
            controls
            preload='metadata'
            src={remoteUrl}
            className='max-is-[280px] rounded'
            onLoadedMetadata={onMediaLoad}
            onError={() => setMediaError(true)}
          />
        )
      case 'audio':
        return <audio controls src={remoteUrl} className='is-[240px]' onError={() => setMediaError(true)} />
      default:
        return (
          <div className={classnames('flex items-center gap-2', { 'cursor-pointer': !isTemp })} onClick={openMedia}>
            <i className='tabler-file text-2xl shrink-0' />
            <div className='flex flex-col min-is-0'>
              <Typography color='inherit' className='break-all'>
                {message.fileName || messageTypeLabel(message.type)}
              </Typography>
              {message.fileSize !== undefined && (
                <Typography variant='caption' color='inherit' className='opacity-70'>
                  {formatFileSize(message.fileSize)}
                </Typography>
              )}
            </div>
          </div>
        )
    }
  }

  const renderBody = () => {
    if (message.type === 'text') {
      return (
        <Typography color='inherit' className='whitespace-pre-wrap break-words'>
          {message.text}
        </Typography>
      )
    }

    if (mediaError) {
      return (
        <div className='flex items-center gap-2 border border-divider rounded plb-2 pli-3'>
          <i
            className={classnames(
              VISUAL_TYPES.includes(message.type) ? 'tabler-photo-off' : 'tabler-file-off',
              'text-xl shrink-0 opacity-70'
            )}
          />
          <Typography color='inherit' className='italic'>
            Media tidak tersedia
          </Typography>
        </div>
      )
    }

    if (showMedia) {
      return (
        <>
          {renderMedia()}
          {message.text.length > 0 && (
            <Typography color='inherit' className='whitespace-pre-wrap break-words mbs-1'>
              {message.text}
            </Typography>
          )}
        </>
      )
    }

    // Old rows / expired media without bytes: keep the italic icon + label placeholder
    return (
      <div className='flex items-center gap-1.5'>
        <i className={classnames(TYPE_ICONS[message.type], 'text-lg shrink-0')} />
        <Typography color='inherit' className='italic whitespace-pre-wrap break-words'>
          {message.text || messageTypeLabel(message.type)}
        </Typography>
      </div>
    )
  }

  const renderTick = () => {
    if (!message.fromMe || message.pending || message.failed) return null

    if (message.status === 'sent') return <i className='tabler-check text-sm opacity-70' />
    if (message.status === 'delivered') return <i className='tabler-checks text-sm opacity-70' />
    if (message.status === 'read')
      return <Box component='i' className='tabler-checks text-sm' sx={{ color: 'info.main' }} />

    return null
  }

  return (
    <div
      className={classnames(
        'flex flex-col max-is-[75%] gap-0.5',
        message.fromMe ? 'self-end items-end' : 'self-start items-start'
      )}
    >
      <Box
        className={classnames('rounded-lg plb-2 pli-3', { 'bg-actionHover': !message.fromMe && !isBareSticker })}
        sx={message.fromMe && !isBareSticker ? { bgcolor: 'primary.main', color: 'primary.contrastText' } : undefined}
      >
        {!message.fromMe && isGroup && (
          <Typography variant='caption' component='div' sx={{ fontWeight: 500, color: senderColor(message.senderJid) }}>
            {message.senderName || message.senderJid.split('@')[0]}
          </Typography>
        )}
        {renderBody()}
        <div className='flex items-center justify-end gap-1 mbs-0.5'>
          <Typography variant='caption' color='inherit' className='opacity-70'>
            {formatClock(message.timestamp)}
          </Typography>
          {message.fromMe && message.pending && <i className='tabler-clock text-sm opacity-70' />}
          {message.fromMe && message.failed && <i className='tabler-alert-circle text-sm' />}
          {renderTick()}
        </div>
      </Box>
      {message.fromMe && message.failed && (
        <Typography
          variant='caption'
          className='cursor-pointer'
          sx={{ color: 'error.main' }}
          onClick={() => onRetry(message)}
        >
          Gagal terkirim. Klik untuk coba lagi.
        </Typography>
      )}
    </div>
  )
}

export default MessageBubble
