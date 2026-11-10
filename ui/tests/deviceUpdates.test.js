import test from 'node:test'
import assert from 'node:assert/strict'
import {watchDeviceChanges, deviceTransitions} from '../src/deviceUpdates.js'

function fixture(extra = {}) {
  let now = 0, nextId = 0, refreshes = 0
  const tasks = new Map(), streams = [], states = [], listeners = new Map()
  const clock = {
    setTimeout(fn, ms) { const id = ++nextId; tasks.set(id, {fn, at: now + ms}); return id },
    clearTimeout(id) { tasks.delete(id) },
    setInterval(fn, ms) { const id = ++nextId; tasks.set(id, {fn, at: now + ms, interval: ms}); return id },
    clearInterval(id) { tasks.delete(id) }
  }
  class Stream {
    handlers = new Map()
    closed = false
    constructor(url) { this.url = url; streams.push(this) }
    addEventListener(type, fn) { this.handlers.set(type, fn) }
    emit(type) { this.handlers.get(type)?.() }
    close() { this.closed = true }
  }
  const visibility = {
    visibilityState: 'visible',
    addEventListener(type, fn) { listeners.set(type, fn) },
    removeEventListener(type) { listeners.delete(type) }
  }
  const stop = watchDeviceChanges({refresh: async () => { refreshes++ }, onState: state => states.push(state), clock, EventSourceImpl: Stream, visibility, ...extra})
  async function advance(ms) {
    const target = now + ms
    for (;;) {
      const next = [...tasks].filter(([, task]) => task.at <= target).sort((a, b) => a[1].at - b[1].at)[0]
      if (!next) break
      const [id, task] = next
      now = task.at
      if (task.interval) task.at += task.interval
      else tasks.delete(id)
      await task.fn()
    }
    now = target
  }
  return {stop, advance, streams, states, tasks, listeners, visibility, get refreshes() { return refreshes }}
}

test('online/offline events update an already open list, and bursts coalesce', async () => {
  const f = fixture()
  f.streams[0].onopen()
  await f.advance(150)
  assert.equal(f.refreshes, 1)
  for (let i = 0; i < 20; i++) f.streams[0].emit('devices-changed')
  await f.advance(150)
  assert.equal(f.refreshes, 2)
  assert.equal(f.states.at(-1), 'live')
  f.stop()
})

test('disconnect polls, reconnect immediately resynchronizes and stops polling', async () => {
  const f = fixture(), stream = f.streams[0]
  stream.onopen()
  await f.advance(150)
  stream.onerror()
  await f.advance(10150)
  assert.equal(f.refreshes, 3)
  assert.equal(f.states.at(-1), 'reconnecting')
  stream.onopen()
  await f.advance(150)
  assert.equal(f.refreshes, 4)
  await f.advance(10000)
  assert.equal(f.refreshes, 4)
  f.stop()
})

test('a silently stalled stream is replaced; heartbeats keep it alive', async () => {
  const f = fixture()
  f.streams[0].onopen()
  await f.advance(30000)
  f.streams[0].emit('heartbeat')
  await f.advance(30000)
  assert.equal(f.streams.length, 1)
  await f.advance(15000)
  assert.equal(f.streams.length, 2)
  assert.equal(f.streams[0].closed, true)
  f.streams[1].onopen()
  f.streams[0].onerror()
  assert.equal(f.states.at(-1), 'live')
  f.stop()
})

test('unsupported EventSource falls back to timed refresh', async () => {
  const f = fixture({EventSourceImpl: null})
  await f.advance(10150)
  assert.equal(f.refreshes, 2)
  f.stop()
})

test('unmount or tenant switch closes stream, timers and visibility listener', async () => {
  const f = fixture(), stream = f.streams[0]
  stream.onerror()
  f.stop()
  stream.onopen()
  stream.emit('devices-changed')
  await f.advance(60000)
  assert.equal(f.refreshes, 0)
  assert.equal(stream.closed, true)
  assert.equal(f.tasks.size, 0)
  assert.equal(f.listeners.size, 0)
})

test('returning to the page resynchronizes the list', async () => {
  const f = fixture()
  f.listeners.get('visibilitychange')()
  await f.advance(150)
  assert.equal(f.refreshes, 1)
  f.stop()
})

test('a change arriving during an in-flight refresh is retained without overlapping requests', async () => {
  let release, calls = 0, active = 0, peak = 0
  const blocked = new Promise(resolve => { release = resolve })
  const f = fixture({refresh: async () => {
    calls++
    peak = Math.max(peak, ++active)
    if (calls === 1) await blocked
    active--
  }})
  const first = f.advance(150)
  f.streams[0].emit('devices-changed')
  await f.advance(150)
  release()
  await first
  await f.advance(300)
  assert.equal(calls, 2)
  assert.equal(peak, 1)
  f.stop()
})

test('notifications distinguish new online, offline and unchanged devices', () => {
  const previous = [{id: 'a', online: false}, {id: 'b', online: true}, {id: 'c', online: true}]
  const current = [{id: 'a', online: true}, {id: 'b', online: false}, {id: 'c', online: true}, {id: 'd', online: true}]
  const changes = deviceTransitions(previous, current)
  assert.deepEqual(changes.online.map(d => d.id), ['a', 'd'])
  assert.deepEqual(changes.offline.map(d => d.id), ['b'])
  assert.deepEqual(deviceTransitions(current, current), {online: [], offline: []})
})
