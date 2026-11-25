import { useEffect, useState } from 'react'
import { ApiError } from '@/api/client'

export type RequestState<T> =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ready'; data: T }

export function useRequest<T, Args extends (string | number)[]>(
  load: (...args: [...Args, AbortSignal]) => Promise<T>,
  ...args: Args
): RequestState<T> {
  const key = JSON.stringify(args)
  const [result, setResult] = useState<{ key: string; state: RequestState<T> }>()

  useEffect(() => {
    const controller = new AbortController()
    const parameters = JSON.parse(key) as Args

    async function run() {
      try {
        const data = await load(...parameters, controller.signal)
        if (!controller.signal.aborted) setResult({ key, state: { status: 'ready', data } })
      } catch (error) {
        if (controller.signal.aborted) return
        const message = error instanceof ApiError && error.status === 403
          ? 'You don’t have access to this database.'
          : error instanceof ApiError ? error.message : 'Could not load data. Try again.'
        setResult({ key, state: { status: 'error', message } })
      }
    }

    void run()
    return () => controller.abort()
  }, [key, load])

  return result?.key === key ? result.state : { status: 'loading' }
}
