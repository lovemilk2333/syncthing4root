<script setup>
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useToast } from 'primevue/usetoast'

import StatusCard from './components/StatusCard.vue'
import ControlsCard from './components/ControlsCard.vue'
import GeneralCard from './components/GeneralCard.vue'
import NetPolicyCard from './components/NetPolicyCard.vue'
import LogsCard from './components/LogsCard.vue'
import DnsCard from './components/DnsCard.vue'
import AccountCard from './components/AccountCard.vue'

import { api, post } from './api'
import { createEventStream } from './events'

const toast = useToast()

function defaultConfig() {
  return {
    enabled: false,
    expr: '',
    interval_sec: 30,
    wifi: { enabled: false, whitelist: [] },
    cellular: { enabled: false },
    power: { enabled: false },
    probe: {
      enabled: false,
      type: 'ping',
      target: '',
      port: 22,
      timeout_ms: 2000,
      up_threshold: 2,
      down_threshold: 3,
    },
  }
}

function defaultState() {
  return {
    transport: '',
    ssid: '',
    power_save: false,
    probe_ok: false,
    probe_up: false,
    terms: {},
    should_run: false,
    enabled: false,
    explanation: '',
    expr_error: '',
  }
}

const status = reactive({
  running: false,
  pid: null,
  url: '',
  username: 'admin',
  autostart: true,
})

const net = reactive({
  config: defaultConfig(),
  state: defaultState(),
})

const eventSources = reactive({ netlink: false, logcat: false, inotify: false })

const link = ref('connecting')
const loaded = ref(false)
let stopStream = null

function applySnapshot(snapshot) {
  if (!snapshot) return
  if (snapshot.status) Object.assign(status, snapshot.status)
  if (snapshot.netpolicy) {
    if (snapshot.netpolicy.config) net.config = snapshot.netpolicy.config
    if (snapshot.netpolicy.state) net.state = snapshot.netpolicy.state
  }
  if (snapshot.event_sources) Object.assign(eventSources, snapshot.event_sources)
  loaded.value = true
}

onMounted(() => {
  stopStream = createEventStream({
    onSnapshot: applySnapshot,
    onState: (state) => {
      link.value = state
    },
  })
})

onBeforeUnmount(() => {
  if (stopStream) stopStream()
})

async function startSyncthing() {
  try {
    const data = await post('/api/start')
    toast.add({ severity: 'success', summary: 'Started', detail: `PID ${data.pid}`, life: 2500 })
  } catch (err) {
    toast.add({ severity: 'error', summary: 'Cannot start', detail: err.message, life: 5000 })
  }
}

async function stopSyncthing() {
  try {
    await post('/api/stop')
    toast.add({ severity: 'success', summary: 'Stopped', life: 2500 })
  } catch (err) {
    toast.add({ severity: 'error', summary: 'Cannot stop', detail: err.message, life: 5000 })
  }
}

async function openSyncthing() {
  try {
    await post('/api/open-syncthing')
    toast.add({ severity: 'info', summary: 'Opening Syncthing UI…', life: 2500 })
  } catch {
    // fall back to opening the URL directly (pop-up blockers may interfere)
    try {
      const data = await api('/api/syncthing-url')
      window.open(data.url, '_blank')
    } catch (err2) {
      toast.add({ severity: 'error', summary: 'Cannot open', detail: err2.message, life: 5000 })
    }
  }
}
</script>

<template>
  <header class="app-header">
    <h1>Syncthing</h1>
    <div class="subtitle">Root Module Manager</div>
    <div class="link-state">
      <span class="link-dot" :class="link"></span>
      <span>{{ link === 'live' ? 'live updates' : link === 'offline' ? 'reconnecting…' : 'connecting…' }}</span>
    </div>
  </header>

  <StatusCard :status="status" :net="net" :event-sources="eventSources" />

  <ControlsCard :running="status.running" @start="startSyncthing" @stop="stopSyncthing" @open="openSyncthing" />

  <GeneralCard :autostart="status.autostart" :running="status.running" />

  <NetPolicyCard :config="net.config" :state="net.state" />

  <LogsCard />

  <DnsCard />

  <AccountCard :username="status.username" />

  <footer>
    <span v-if="!loaded">loading…</span>
    <span v-else>Syncthing4Root · realtime control panel</span>
  </footer>

  <Toast position="bottom-center" />
</template>
