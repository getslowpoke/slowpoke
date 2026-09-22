// Thin fetch wrapper: session cookie + the custom header the server requires for mutations.
export class ApiError extends Error {
  constructor(status, message) {
    super(message)
    this.status = status
  }
}

async function request(method, path, body) {
  const res = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'X-Slowpoke': '1' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  let data = null
  try {
    data = await res.json()
  } catch {
    data = null
  }
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith('/api/login') && !path.startsWith('/api/me') && !path.startsWith('/api/setup')) {
      window.dispatchEvent(new CustomEvent('slowpoke:unauthorized'))
    }
    throw new ApiError(res.status, data?.error || res.statusText)
  }
  return data
}

export const api = {
  get: (p) => request('GET', p),
  // Some answers are plain text because a shell script reads them too (the fleet list).
  text: async (p) => {
    const res = await fetch(p, { credentials: 'same-origin', headers: { 'X-Slowpoke': '1' } })
    return res.ok ? res.text() : ''
  },
  post: (p, b = {}) => request('POST', p, b),
  patch: (p, b) => request('PATCH', p, b),
  put: (p, b = {}) => request('PUT', p, b),
  del: (p) => request('DELETE', p),
}

export const projectApi = (slug) => ({
  get: (sub = '', query = '') => api.get(`/api/projects/${slug}${sub ? '/' + sub : ''}${query ? '?' + query : ''}`),
})
