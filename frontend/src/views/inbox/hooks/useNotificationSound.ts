'use client'

// React Imports
import { useCallback, useEffect, useRef, useState } from 'react'

export type NotificationMode = 'loud' | 'soft' | 'muted'

const MODE_STORAGE_KEY = 'inbox-notification-mode'
const LEGACY_MUTE_KEY = 'inbox-notification-muted'
const SOFT_MIN_INTERVAL_MS = 1500
const RING_CYCLE_MS = 1300

const playTone = (
  ctx: AudioContext,
  destination: AudioNode,
  frequency: number,
  startAt: number,
  duration: number,
  peak: number
) => {
  const oscillator = ctx.createOscillator()
  const gain = ctx.createGain()

  oscillator.type = 'sine'
  oscillator.frequency.setValueAtTime(frequency, startAt)

  gain.gain.setValueAtTime(0.0001, startAt)
  gain.gain.exponentialRampToValueAtTime(peak, startAt + 0.015)
  gain.gain.exponentialRampToValueAtTime(0.0001, startAt + duration)

  oscillator.connect(gain)
  gain.connect(destination)
  oscillator.start(startAt)
  oscillator.stop(startAt + duration + 0.02)
}

export const useNotificationSound = (): {
  mode: NotificationMode
  setMode: (mode: NotificationMode) => void
  notify: () => void
} => {
  // States
  const [mode, setModeState] = useState<NotificationMode>('loud')

  // Refs (autoplay policy: the context stays suspended until the user interacts with the page)
  const ctxRef = useRef<AudioContext | null>(null)
  const modeRef = useRef(mode)
  const lastSoftPlayedRef = useRef(0)
  const ringIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const ringMasterGainRef = useRef<GainNode | null>(null)

  modeRef.current = mode

  // Restore preference after hydration to avoid an SSR mismatch (default: loud)
  useEffect(() => {
    const stored = localStorage.getItem(MODE_STORAGE_KEY)

    if (stored === 'loud' || stored === 'soft' || stored === 'muted') {
      setModeState(stored)
    } else if (localStorage.getItem(LEGACY_MUTE_KEY) === '1') {
      setModeState('muted')
    }
  }, [])

  const getContext = useCallback(() => {
    ctxRef.current = ctxRef.current ?? new AudioContext()

    if (ctxRef.current.state === 'suspended') {
      ctxRef.current.resume().catch(() => undefined)
    }

    return ctxRef.current
  }, [])

  const stopRinging = useCallback(() => {
    if (ringIntervalRef.current) {
      clearInterval(ringIntervalRef.current)
      ringIntervalRef.current = null
    }

    document.removeEventListener('pointerdown', stopRinging)

    const master = ringMasterGainRef.current

    if (master && ctxRef.current) {
      // Fade out fast so the ring dies the moment the user clicks
      master.gain.setTargetAtTime(0.0001, ctxRef.current.currentTime, 0.03)
      setTimeout(() => master.disconnect(), 200)
      ringMasterGainRef.current = null
    }
  }, [])

  const startRinging = useCallback(() => {
    if (ringIntervalRef.current) return

    try {
      const ctx = getContext()
      const master = ctx.createGain()

      master.gain.setValueAtTime(1, ctx.currentTime)
      master.connect(ctx.destination)
      ringMasterGainRef.current = master

      const ringOnce = () => {
        const startAt = ctx.currentTime

        playTone(ctx, master, 880, startAt, 0.4, 0.5)
        playTone(ctx, master, 1174, startAt + 0.45, 0.5, 0.5)
      }

      ringOnce()
      ringIntervalRef.current = setInterval(ringOnce, RING_CYCLE_MS)
      document.addEventListener('pointerdown', stopRinging)
    } catch {
      // Audio unavailable (no user gesture yet or unsupported) — skip silently
    }
  }, [getContext, stopRinging])

  const notify = useCallback(() => {
    if (modeRef.current === 'muted') return

    if (modeRef.current === 'loud') {
      startRinging()

      return
    }

    const now = Date.now()

    if (now - lastSoftPlayedRef.current < SOFT_MIN_INTERVAL_MS) return
    lastSoftPlayedRef.current = now

    try {
      const ctx = getContext()
      const startAt = ctx.currentTime

      playTone(ctx, ctx.destination, 987, startAt, 0.18, 0.12)
      playTone(ctx, ctx.destination, 1318, startAt + 0.09, 0.2, 0.12)
    } catch {
      // Audio unavailable — skip silently
    }
  }, [getContext, startRinging])

  const setMode = useCallback(
    (next: NotificationMode) => {
      localStorage.setItem(MODE_STORAGE_KEY, next)
      setModeState(next)

      if (next !== 'loud') stopRinging()
    },
    [stopRinging]
  )

  // Stop any ongoing ring when the view unmounts
  useEffect(() => stopRinging, [stopRinging])

  return { mode, setMode, notify }
}
