export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

type ApiRequestOptions = RequestInit & { csrfToken?: string }

export async function apiFetch<T>(path: string, options: ApiRequestOptions = {}): Promise<T> {
  const { csrfToken, headers: requestHeaders, ...requestOptions } = options
  const headers = new Headers(requestHeaders)
  headers.set('Accept', 'application/json')
  if (csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }

  const response = await fetch(path, { ...requestOptions, headers, credentials: 'same-origin' })
  if (!response.ok) {
    const fallback = response.statusText || `Request failed (${response.status})`
    if (response.headers.get('Content-Type')?.includes('application/json')) {
      const body = await response.json().catch(() => null)
      throw new ApiError(response.status, typeof body?.error === 'string' ? body.error : fallback)
    }
    throw new ApiError(response.status, (await response.text()).trim() || fallback)
  }

  return response.json()
}
