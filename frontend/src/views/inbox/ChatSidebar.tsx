'use client'

// React Imports
import { useMemo, useState } from 'react'

// MUI Imports
import Alert from '@mui/material/Alert'
import Button from '@mui/material/Button'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogContentText from '@mui/material/DialogContentText'
import DialogTitle from '@mui/material/DialogTitle'
import IconButton from '@mui/material/IconButton'
import InputAdornment from '@mui/material/InputAdornment'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import Tooltip from '@mui/material/Tooltip'
import Typography from '@mui/material/Typography'

// Type Imports
import type { Chat, Me } from '@/types/chatTypes'

import type { NotificationMode } from './hooks/useNotificationSound'

// Component Imports
import CustomTextField from '@core/components/mui/TextField'

import ChatListItem from './ChatListItem'

type Props = {
  chats: Chat[]
  loading: boolean
  error: boolean
  selectedJid: string | null
  search: string
  me: Me | null
  soundMode: NotificationMode
  onSoundModeChange: (mode: NotificationMode) => void
  onSearchChange: (value: string) => void
  onSelectChat: (jid: string) => void
  onRetry: () => void
  onLogout: () => void
}

const ChatSidebar = ({
  chats,
  loading,
  error,
  selectedJid,
  search,
  me,
  soundMode,
  onSoundModeChange,
  onSearchChange,
  onSelectChat,
  onRetry,
  onLogout
}: Props) => {
  // States
  const [logoutDialogOpen, setLogoutDialogOpen] = useState(false)
  const [soundMenuAnchor, setSoundMenuAnchor] = useState<HTMLElement | null>(null)

  // Vars
  const filteredChats = useMemo(() => {
    const term = search.trim().toLowerCase()

    if (!term) return chats

    return chats.filter(chat => chat.name.toLowerCase().includes(term))
  }, [chats, search])

  const handleConfirmLogout = () => {
    setLogoutDialogOpen(false)
    onLogout()
  }

  const handleSelectSound = (mode: NotificationMode) => {
    setSoundMenuAnchor(null)
    onSoundModeChange(mode)
  }

  const soundIcon =
    soundMode === 'muted' ? 'tabler-bell-off' : soundMode === 'soft' ? 'tabler-bell' : 'tabler-bell-ringing'

  return (
    <div className='is-[300px] md:is-[360px] shrink-0 border-ie border-divider flex flex-col min-bs-0'>
      <div className='flex items-center justify-between gap-2 pli-4 plb-3'>
        <div className='flex flex-col min-is-0'>
          <Typography variant='h5'>Inbox</Typography>
          {me && (
            <Typography variant='caption' color='text.secondary' noWrap>
              {me.name || me.phone}
            </Typography>
          )}
        </div>
        <div className='flex items-center'>
          <Tooltip title='Suara notifikasi'>
            <IconButton onClick={e => setSoundMenuAnchor(e.currentTarget)} aria-label='Suara notifikasi'>
              <i className={soundIcon} />
            </IconButton>
          </Tooltip>
          <Menu anchorEl={soundMenuAnchor} open={Boolean(soundMenuAnchor)} onClose={() => setSoundMenuAnchor(null)}>
            <MenuItem selected={soundMode === 'loud'} onClick={() => handleSelectSound('loud')}>
              <i className='tabler-bell-ringing text-xl mie-2' />
              Kencang — berdering sampai diklik
            </MenuItem>
            <MenuItem selected={soundMode === 'soft'} onClick={() => handleSelectSound('soft')}>
              <i className='tabler-bell text-xl mie-2' />
              Singkat
            </MenuItem>
            <MenuItem selected={soundMode === 'muted'} onClick={() => handleSelectSound('muted')}>
              <i className='tabler-bell-off text-xl mie-2' />
              Bisu
            </MenuItem>
          </Menu>
          <Tooltip title='Putuskan koneksi'>
            <IconButton onClick={() => setLogoutDialogOpen(true)} aria-label='Putuskan koneksi'>
              <i className='tabler-plug-off' />
            </IconButton>
          </Tooltip>
        </div>
      </div>
      <div className='pli-4 pbe-3'>
        <CustomTextField
          fullWidth
          placeholder='Cari chat…'
          value={search}
          onChange={e => onSearchChange(e.target.value)}
          slotProps={{
            input: {
              startAdornment: (
                <InputAdornment position='start'>
                  <i className='tabler-search' />
                </InputAdornment>
              )
            }
          }}
        />
      </div>
      <div className='overflow-y-auto flex-auto min-bs-0'>
        {loading ? (
          [...Array(5)].map((_, index) => (
            <div key={index} className='flex items-center gap-3 pli-4 plb-3'>
              <Skeleton variant='circular' width={40} height={40} />
              <div className='flex flex-col flex-auto gap-1'>
                <Skeleton variant='text' width='60%' />
                <Skeleton variant='text' width='85%' />
              </div>
            </div>
          ))
        ) : error ? (
          <div className='pli-4 plb-3'>
            <Alert
              severity='error'
              action={
                <Button color='inherit' size='small' onClick={onRetry}>
                  Coba Lagi
                </Button>
              }
            >
              Gagal memuat daftar chat.
            </Alert>
          </div>
        ) : chats.length === 0 ? (
          <Typography color='text.secondary' className='pli-4 plb-4 text-center'>
            Belum ada percakapan
          </Typography>
        ) : filteredChats.length === 0 ? (
          <Typography color='text.secondary' className='pli-4 plb-4 text-center'>
            Tidak ada chat yang cocok
          </Typography>
        ) : (
          filteredChats.map(chat => (
            <ChatListItem key={chat.jid} chat={chat} selected={chat.jid === selectedJid} onSelect={onSelectChat} />
          ))
        )}
      </div>
      <Dialog open={logoutDialogOpen} onClose={() => setLogoutDialogOpen(false)}>
        <DialogTitle>Putuskan koneksi WhatsApp?</DialogTitle>
        <DialogContent>
          <DialogContentText>
            Sesi WhatsApp di aplikasi ini akan dihapus. Kamu perlu scan ulang kode QR untuk menghubungkan lagi.
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button color='secondary' onClick={() => setLogoutDialogOpen(false)}>
            Batal
          </Button>
          <Button variant='contained' color='error' onClick={handleConfirmLogout}>
            Putuskan
          </Button>
        </DialogActions>
      </Dialog>
    </div>
  )
}

export default ChatSidebar
