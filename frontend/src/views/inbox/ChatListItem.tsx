// MUI Imports
import Chip from '@mui/material/Chip'
import Typography from '@mui/material/Typography'

// Third-party Imports
import classnames from 'classnames'

// Type Imports
import type { Chat } from '@/types/chatTypes'

// Component Imports
import ChatAvatar from './ChatAvatar'

// Util Imports
import { formatListTime } from './utils'

type Props = {
  chat: Chat
  selected: boolean
  onSelect: (jid: string) => void
}

const ChatListItem = ({ chat, selected, onSelect }: Props) => (
  <div
    role='button'
    tabIndex={0}
    className={classnames('flex items-center gap-3 pli-4 plb-3 cursor-pointer', { 'bg-actionHover': selected })}
    onClick={() => onSelect(chat.jid)}
    onKeyDown={e => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        onSelect(chat.jid)
      }
    }}
  >
    <ChatAvatar jid={chat.jid} name={chat.name} />
    <div className='flex flex-col flex-auto min-is-0'>
      <div className='flex items-center gap-2'>
        {chat.isGroup && <i className='tabler-users text-base text-textSecondary shrink-0' />}
        <Typography color='text.primary' className='font-medium flex-auto' noWrap>
          {chat.name}
        </Typography>
        {chat.lastMessageAt > 0 && (
          <Typography variant='caption' color='text.secondary' className='shrink-0'>
            {formatListTime(chat.lastMessageAt)}
          </Typography>
        )}
      </div>
      <div className='flex items-center gap-2'>
        <Typography variant='body2' color='text.secondary' className='flex-auto' noWrap>
          {chat.lastMessage}
        </Typography>
        {chat.unreadCount > 0 && <Chip size='small' color='primary' label={chat.unreadCount} className='shrink-0' />}
      </div>
    </div>
  </div>
)

export default ChatListItem
