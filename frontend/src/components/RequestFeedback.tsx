import type { RequestState } from '@/api/useRequest'

export function RequestFeedback({ request, loading }: {
  request: Exclude<RequestState<unknown>, { status: 'ready' }>
  loading: string
}) {
  return request.status === 'error'
    ? <p className="feedback feedback-error" role="alert">{request.message}</p>
    : <p className="feedback" role="status">{loading}</p>
}
