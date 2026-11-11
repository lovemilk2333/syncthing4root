export async function api(path, options = {}) {
  const headers = { Accept: 'application/json', ...(options.headers || {}) }
  const res = await fetch(path, { ...options, headers })
  let data = {}
  try {
    data = await res.json()
  } catch {
    // empty or non-JSON response
  }
  if (!res.ok) {
    throw new Error(data.error || `${res.status} ${res.statusText}`)
  }
  return data
}

export function postJSON(path, body) {
  return api(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export function post(path) {
  return api(path, { method: 'POST' })
}
