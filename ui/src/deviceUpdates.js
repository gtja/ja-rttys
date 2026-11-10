// The stream only invalidates the list. All device data still comes from the
// authenticated APIs, including when an intermediary cannot carry SSE.
export function watchDeviceChanges({refresh, onState = () => {}, EventSourceImpl = globalThis.EventSource, clock = globalThis, visibility = globalThis.document}) {
  let source, fallback, watchdog, queued
  let stopped = false, running = false, pending = false

  async function run() {
    queued = undefined
    if (stopped) return
    if (running) {
      pending = true
      return
    }
    running = true
    try {
      await refresh()
    } finally {
      running = false
      if (pending && !stopped) {
        pending = false
        schedule()
      }
    }
  }
  function schedule() {
    if (!stopped && queued === undefined) queued = clock.setTimeout(run, 150)
  }
  function poll() {
    if (stopped) return
    onState('reconnecting')
    if (fallback === undefined) fallback = clock.setInterval(schedule, 10000)
    schedule()
  }
  function keepAlive() {
    clock.clearTimeout(watchdog)
    watchdog = clock.setTimeout(() => {
      poll()
      connect()
    }, 45000)
  }
  function connect() {
    source?.close()
    if (stopped || !EventSourceImpl) {
      poll()
      return
    }
    try {
      source = new EventSourceImpl('/api/events/devices')
    } catch {
      poll()
      return
    }
    const stream = source
    const current = () => !stopped && source === stream
    source.onopen = () => {
      if (!current()) return
      clock.clearInterval(fallback)
      fallback = undefined
      onState('live')
      keepAlive()
      schedule()
    }
    source.addEventListener('devices-changed', () => {
      if (!current()) return
      keepAlive()
      schedule()
    })
    source.addEventListener('heartbeat', () => { if (current()) keepAlive() })
    source.onerror = () => { if (current()) poll() }
    keepAlive()
  }
  function visible() {
    if (visibility.visibilityState === 'visible') schedule()
  }
  visibility?.addEventListener('visibilitychange', visible)
  poll()
  connect()
  return () => {
    stopped = true
    source?.close()
    clock.clearInterval(fallback)
    clock.clearTimeout(watchdog)
    clock.clearTimeout(queued)
    visibility?.removeEventListener('visibilitychange', visible)
  }
}

export function deviceTransitions(previous, current) {
  const before = new Map(previous.map(device => [device.id, device]))
  return {
    online: current.filter(device => device.online && !before.get(device.id)?.online),
    offline: current.filter(device => !device.online && before.get(device.id)?.online)
  }
}
