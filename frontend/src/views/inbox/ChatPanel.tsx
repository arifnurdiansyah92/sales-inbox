// MUI Imports
import Typography from '@mui/material/Typography'

// Type Imports
import type { Chat, Message } from '@/types/chatTypes'

// Component Imports
import ChatAvatar from './ChatAvatar'
import Composer from './Composer'
import MessageList from './MessageList'

// Util Imports
import type { MessagesMeta } from './reducer'

type Props = {
  chat: Chat | null
  messages: Message[]
  meta: MessagesMeta
  onSend: (text: string) => void
  onSendMedia: (file: File) => void
  onLoadOlder: (jid: string) => void
  onRetryInitial: (jid: string) => void
  onRetryMessage: (message: Message) => void
}

const ChatPanel = ({
  chat,
  messages,
  meta,
  onSend,
  onSendMedia,
  onLoadOlder,
  onRetryInitial,
  onRetryMessage
}: Props) => {
  if (!chat) {
    return (
      <div className='flex flex-col flex-auto min-is-0 min-bs-0 items-center justify-center gap-3'>
        <i className='tabler-message-circle text-6xl text-textDisabled' />
        <Typography color='text.secondary'>Pilih chat untuk mulai percakapan</Typography>
      </div>
    )
  }

  return (
    <div className='flex flex-col flex-auto min-is-0 min-bs-0'>
      <div className='flex items-center gap-3 pli-4 plb-3 border-be border-divider shrink-0'>
        <ChatAvatar jid={chat.jid} name={chat.name} />
        <div className='flex flex-col min-is-0'>
          <Typography color='text.primary' className='font-medium' noWrap>
            {chat.name}
          </Typography>
          {chat.isGroup && (
            <Typography variant='caption' color='text.secondary'>
              Grup
            </Typography>
          )}
        </div>
      </div>
      <MessageList
        chatJid={chat.jid}
        messages={messages}
        meta={meta}
        isGroup={chat.isGroup}
        onLoadOlder={() => onLoadOlder(chat.jid)}
        onRetryInitial={() => onRetryInitial(chat.jid)}
        onRetryMessage={onRetryMessage}
      />
      <Composer onSend={onSend} onSendMedia={onSendMedia} />
    </div>
  )
}

export default ChatPanel
