// Log tail stream. Same fetch-based SSE transport as the status stream, but
// with `history` / `line` events. History length is a server-side setting.
export function createLogStream({ source, onHistory, onLine, onState, onError }) {
  let closed = false
  let controller = null
  let retry = 0

  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

  function handleFrame(frame) {
    let event = 'message'
    const data = []
    for (const line of frame.split('\n')) {
      if (!line || line.startsWith(':')) continue
      if (line.startsWith('event:')) {
        event = line.slice(6).trim()
      } else if (line.startsWith('data:')) {
        data.push(line.slice(5).replace(/^ /, ''))
      }
    }
    if (!data.length) return
    const raw = data.join('\n')

    if (event === 'history') {
      try {
        const parsed = JSON.parse(raw)
        onHistory?.(parsed.lines || [])
      } catch {
        // ignore malformed history
      }
    } else if (event === 'line') {
      try {
        onLine?.(JSON.parse(raw))
      } catch {
        onLine?.(raw)
      }
    } else if (event === 'ready') {
      onState?.('live')
    } else if (event === 'error') {
      try {
        onError?.(JSON.parse(raw).error || raw)
      } catch {
        onError?.(raw)
      }
    }
  }

  async function run() {
    while (!closed) {
      controller = new AbortController()
      try {
        const url = `/api/logs/stream?source=${encodeURIComponent(source)}`
        const res = await fetch(url, {
          headers: { Accept: 'text/event-stream' },
          cache: 'no-store',
          signal: controller.signal,
        })
        if (res.status === 404) {
          onState?.('error')
          onError?.('log file not found')
          return
        }
        if (!res.ok || !res.body) {
          throw new Error(`log stream unavailable (${res.status})`)
        }

        onState?.('connecting')
        retry = 0

        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''

        while (!closed) {
          const { done, value } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })

          let idx
          while ((idx = buffer.indexOf('\n\n')) !== -1) {
            const frame = buffer.slice(0, idx)
            buffer = buffer.slice(idx + 2)
            handleFrame(frame)
          }
        }
      } catch {
        // network error or aborted — fall through to reconnect
      }

      if (closed) return
      onState?.('offline')
      retry = Math.min(retry + 1, 5)
      await sleep(1000 * retry)
    }
  }

  run()

  return () => {
    closed = true
    controller?.abort()
  }
}

//
// EventSource cannot send custom headers or reliably reuse Basic Auth on all
// browsers, so we stream the response ourselves. The server only sends
// `snapshot` frames (plus keepalive comments), which makes parsing simple.
//
// Returns a stop() function.

// Server-sent events client built on fetch().
//
// EventSource cannot send custom headers or reliably reuse Basic Auth on all
// browsers, so we stream the response ourselves. The server only sends
// `snapshot` frames (plus keepalive comments), which makes parsing simple.
//
// Returns a stop() function.
export function createEventStream({ onSnapshot, onState }) {
  let closed = false
  let controller = null
  let retry = 0

  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

  function handleFrame(frame) {
    let event = 'message'
    const data = []
    for (const line of frame.split('\n')) {
      if (!line || line.startsWith(':')) continue
      if (line.startsWith('event:')) {
        event = line.slice(6).trim()
      } else if (line.startsWith('data:')) {
        data.push(line.slice(5).replace(/^ /, ''))
      }
    }
    if (event === 'snapshot' && data.length) {
      try {
        onSnapshot(JSON.parse(data.join('\n')))
      } catch {
        // ignore malformed frame, the next snapshot will fix the state
      }
    }
  }

  async function run() {
    while (!closed) {
      controller = new AbortController()
      try {
        const res = await fetch('/api/events', {
          headers: { Accept: 'text/event-stream' },
          cache: 'no-store',
          signal: controller.signal,
        })
        if (!res.ok || !res.body) {
          throw new Error(`event stream unavailable (${res.status})`)
        }

        onState('live')
        retry = 0

        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''

        while (!closed) {
          const { done, value } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })

          let idx
          while ((idx = buffer.indexOf('\n\n')) !== -1) {
            const frame = buffer.slice(0, idx)
            buffer = buffer.slice(idx + 2)
            handleFrame(frame)
          }
        }
      } catch {
        // network error or aborted — fall through to reconnect
      }

      if (closed) return
      onState('offline')
      retry = Math.min(retry + 1, 5)
      await sleep(1000 * retry)
    }
  }

  run()

  return () => {
    closed = true
    controller?.abort()
  }
}
