// Next Imports
import type { Metadata } from 'next'

// Component Imports
import Login from '@views/Login'

export const metadata: Metadata = {
  title: 'Login',
  description: 'Masuk ke Sales Inbox'
}

const LoginPage = () => {
  return <Login />
}

export default LoginPage
