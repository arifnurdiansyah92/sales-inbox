'use client'

// React Imports
import { useEffect, useRef, useState } from 'react'

// Type Imports
import type { WSEvent } from '@/types/chatTypes'

// Util Imports
import { getWsUrl } from '@/utils/whatsappApi'

const KNOWN_EVENT_TYPES: WSEvent['type'][] = ['status', 'qr', 'message', 'chat_upsert', 'receipt']

type UseWhatsAppSocketProps = {
  onEvent: (e: WSEvent) => void
  onOpen: () => void
}

export const useWhatsAppSocket = ({ onEvent, onOpen }: UseWhatsAppSocketProps): { live: boolean } => {
  // States
  const [live, setLive] = useState(false)

  // Refs (updated every render so the mount effect always sees the latest callbacks)
  const onEventRef = useRef(onEvent)
  const onOpenRef = useRef(onOpen)

  onEventRef.current = onEvent
  onOpenRef.current = onOpen

  useEffect(() => {
    let disposed = false
    let ws: WebSocket | null = null
    let timer: ReturnType<typeof setTimeout> | null = null
    let attempt = 0

    const scheduleReconnect = () => {
      if (disposed || timer) return

      const delay = Math.min(1000 * 2 ** attempt, 30000) + Math.random() * 1000

      attempt += 1

      timer = setTimeout(() => {
        timer = null
        connect()
      }, delay)
    }

    const connect = () => {
      if (disposed) return

      try {
        ws = new WebSocket(getWsUrl())
      } catch {
        scheduleReconnect()

        return
      }

      ws.onopen = () => {
        if (disposed) return

        attempt = 0
        setLive(true)
        onOpenRef.current()
      }

      ws.onmessage = event => {
        if (disposed) return

        try {
          const parsed = JSON.parse(event.data as string) as WSEvent

          if (parsed && KNOWN_EVENT_TYPES.includes(parsed.type)) {
            onEventRef.current(parsed)
          }
        } catch {
          // Ignore malformed payloads
        }
      }

      ws.onclose = () => {
        if (disposed) return

        setLive(false)
        scheduleReconnect()
      }

      ws.onerror = () => {
        // Force the close handler (and therefore the reconnect) to run
        ws?.close()
      }
    }

    connect()

    return () => {
      disposed = true

      if (timer) clearTimeout(timer)

      if (ws) {
        ws.onopen = null
        ws.onmessage = null
        ws.onclose = null
        ws.onerror = null
        ws.close()
      }
    }
  }, [])

  return { live }
}
