'use client'

// React Imports
import { useState } from 'react'
import type { FormEvent } from 'react'

// Next Imports
import { useRouter } from 'next/navigation'

// MUI Imports
import Alert from '@mui/material/Alert'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import IconButton from '@mui/material/IconButton'
import InputAdornment from '@mui/material/InputAdornment'
import Typography from '@mui/material/Typography'

// Component Imports
import Logo from '@components/layout/shared/Logo'
import CustomTextField from '@core/components/mui/TextField'

// Util Imports
import { ApiError, login } from '@/utils/whatsappApi'

const Login = () => {
  // States
  const [isPasswordShown, setIsPasswordShown] = useState(false)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [errorMessage, setErrorMessage] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  // Hooks
  const router = useRouter()

  const handleClickShowPassword = () => setIsPasswordShown(show => !show)

  const handleSubmit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault()

    if (submitting) return

    setSubmitting(true)
    setErrorMessage(null)

    try {
      await login(username, password)
      router.push('/inbox')
    } catch (error) {
      setErrorMessage(error instanceof ApiError ? error.message : 'Tidak dapat menghubungi server. Coba lagi.')
      setSubmitting(false)
    }
  }

  return (
    <div className='flex justify-center items-center min-bs-[100dvh] p-6'>
      <div className='flex flex-col gap-6 is-full sm:max-is-[400px] bg-backgroundPaper rounded-xl border border-divider p-8'>
        <div className='flex justify-center'>
          <Logo />
        </div>
        <div className='flex flex-col gap-1 text-center'>
          <Typography variant='h4'>Selamat datang 👋🏻</Typography>
          <Typography color='text.secondary'>Masuk untuk mulai mengelola percakapan</Typography>
        </div>
        <form noValidate autoComplete='off' onSubmit={handleSubmit} className='flex flex-col gap-5'>
          {errorMessage && <Alert severity='error'>{errorMessage}</Alert>}
          <CustomTextField
            autoFocus
            fullWidth
            label='Username'
            placeholder='Masukkan username'
            value={username}
            onChange={e => setUsername(e.target.value)}
          />
          <CustomTextField
            fullWidth
            label='Password'
            placeholder='············'
            type={isPasswordShown ? 'text' : 'password'}
            value={password}
            onChange={e => setPassword(e.target.value)}
            slotProps={{
              input: {
                endAdornment: (
                  <InputAdornment position='end'>
                    <IconButton edge='end' onClick={handleClickShowPassword} onMouseDown={e => e.preventDefault()}>
                      <i className={isPasswordShown ? 'tabler-eye-off' : 'tabler-eye'} />
                    </IconButton>
                  </InputAdornment>
                )
              }
            }}
          />
          <Button fullWidth variant='contained' type='submit' disabled={submitting}>
            {submitting ? <CircularProgress size={22} color='inherit' /> : 'Masuk'}
          </Button>
        </form>
      </div>
    </div>
  )
}

export default Login
