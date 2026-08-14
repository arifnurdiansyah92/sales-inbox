// MUI Imports
import Button from '@mui/material/Button'
import Card from '@mui/material/Card'
import CardContent from '@mui/material/CardContent'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'

// Type Imports
import type { ConnectionStatus } from '@/types/chatTypes'

type Props = {
  status: ConnectionStatus
  qr: { dataUrl: string; timeoutMs: number } | null
  statusError: string | null
}

const QR_STEPS = [
  'Buka WhatsApp di HP kamu',
  'Ketuk Menu ⋮ lalu Perangkat Tertaut',
  'Ketuk Tautkan Perangkat',
  'Arahkan kamera HP ke kode QR ini'
]

const ConnectionCard = ({ status, qr, statusError }: Props) => {
  return (
    <div className='flex-auto grid place-items-center p-6'>
      {status === 'waiting_qr' && qr ? (
        <Card>
          <CardContent className='flex flex-col items-center gap-5'>
            <Typography variant='h5'>Hubungkan WhatsApp</Typography>
            <div className='bg-white p-4 rounded-lg'>
              <img src={qr.dataUrl} alt='Kode QR WhatsApp' className='is-[264px] bs-[264px]' />
            </div>
            <ol className='list-decimal pli-5 flex flex-col gap-1 self-start'>
              {QR_STEPS.map(step => (
                <li key={step}>
                  <Typography color='text.primary'>{step}</Typography>
                </li>
              ))}
            </ol>
            <Typography variant='caption' color='text.secondary' className='text-center'>
              Kode QR diperbarui otomatis. Halaman ini akan tersambung sendiri setelah scan berhasil.
            </Typography>
          </CardContent>
        </Card>
      ) : status === 'disconnected' ? (
        <div className='flex flex-col items-center gap-3'>
          <i className='tabler-plug-off text-5xl text-textDisabled' />
          <Typography variant='h5'>Tidak terhubung</Typography>
          {statusError && (
            <Typography color='text.secondary' className='text-center'>
              {statusError}
            </Typography>
          )}
          <Button variant='contained' onClick={() => window.location.reload()}>
            Muat Ulang
          </Button>
        </div>
      ) : (
        <div className='flex flex-col items-center gap-3'>
          <CircularProgress />
          <Typography color='text.secondary'>Menghubungkan ke WhatsApp…</Typography>
        </div>
      )}
    </div>
  )
}

export default ConnectionCard
