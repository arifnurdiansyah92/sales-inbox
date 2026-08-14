'use client'

// React Imports
import { useRef, useState } from 'react'
import type { ChangeEvent, KeyboardEvent } from 'react'

// MUI Imports
import IconButton from '@mui/material/IconButton'
import Tooltip from '@mui/material/Tooltip'

// Component Imports
import CustomTextField from '@core/components/mui/TextField'

type Props = {
  onSend: (text: string) => void
  onSendMedia: (file: File) => void
}

const Composer = ({ onSend, onSendMedia }: Props) => {
  // States
  const [value, setValue] = useState('')

  // Refs
  const inputRef = useRef<HTMLInputElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const handleSend = () => {
    const trimmed = value.trim()

    if (!trimmed) return

    onSend(trimmed)
    setValue('')
    inputRef.current?.focus()
  }

  const handleKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  const handleFileChange = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]

    if (file) onSendMedia(file)

    // Reset so picking the same file again re-triggers the change event
    e.target.value = ''
  }

  return (
    <div className='flex items-end gap-3 pli-4 plb-3 border-bs border-divider'>
      <input ref={fileInputRef} type='file' hidden onChange={handleFileChange} />
      <Tooltip title='Lampirkan file'>
        <IconButton onClick={() => fileInputRef.current?.click()} aria-label='Lampirkan file'>
          <i className='tabler-paperclip' />
        </IconButton>
      </Tooltip>
      <CustomTextField
        fullWidth
        multiline
        maxRows={4}
        placeholder='Ketik pesan…'
        value={value}
        inputRef={inputRef}
        onChange={e => setValue(e.target.value)}
        onKeyDown={handleKeyDown}
      />
      <IconButton color='primary' onClick={handleSend} disabled={!value.trim()} aria-label='Kirim pesan'>
        <i className='tabler-send' />
      </IconButton>
    </div>
  )
}

export default Composer
