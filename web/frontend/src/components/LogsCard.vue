<script setup>
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import InputNumber from 'primevue/inputnumber'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import ToggleSwitch from 'primevue/toggleswitch'
import { api, postJSON } from '../api'
import { createLogStream } from '../events'

const DEFAULT_LINES = 128
const MIN_LINES = 16
const MAX_LINES = 4000

const toast = useToast()

const expanded = ref(false)
const sources = ref([])
const selected = ref('')
const live = ref(true)

const maxLines = ref(DEFAULT_LINES)
const lines = ref([])
const status = ref('idle')
const error = ref('')
const pre = ref(null)
const stick = ref(true)

let stopStream = null
let saveTimer = null
let hydrating = false

const statusSeverity = {
  live: 'success',
  connecting: 'info',
  offline: 'warn',
  error: 'danger',
  idle: 'secondary',
}

function stop() {
  if (stopStream) {
    stopStream()
    stopStream = null
  }
}

function scrollToEnd() {
  if (!stick.value) return
  nextTick(() => {
    const el = pre.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

function onScroll() {
  const el = pre.value
  if (!el) return
  stick.value = el.scrollHeight - el.scrollTop - el.clientHeight < 40
}

function trim() {
  if (lines.value.length > maxLines.value) {
    lines.value.splice(0, lines.value.length - maxLines.value)
  }
}

function start() {
  stop()
  error.value = ''
  if (!selected.value) {
    status.value = 'idle'
    return
  }
  if (!live.value) {
    status.value = 'idle'
    return
  }

  lines.value = []
  stick.value = true
  status.value = 'connecting'
  stopStream = createLogStream({
    source: selected.value,
    onHistory: (history) => {
      lines.value = history.slice(-maxLines.value)
      scrollToEnd()
    },
    onLine: (line) => {
      lines.value.push(line)
      trim()
      scrollToEnd()
    },
    onState: (state) => {
      status.value = state
    },
    onError: (message) => {
      error.value = message
      status.value = 'error'
    },
  })
}

async function loadSources() {
  try {
    const data = await api('/api/logs')
    sources.value = data.logs || []
    const serverLines = Number(data.lines)
    if (Number.isFinite(serverLines) && serverLines >= MIN_LINES && serverLines <= MAX_LINES) {
      hydrating = true
      maxLines.value = Math.round(serverLines)
      hydrating = false
    }
    if (!sources.value.some((s) => s.id === selected.value)) {
      selected.value = sources.value.length ? sources.value[0].id : ''
    }
    if (!sources.value.length) {
      error.value = 'No log source'
    }
  } catch (err) {
    error.value = err.message
    toast.add({ severity: 'error', summary: 'Cannot list logs', detail: err.message, life: 4500 })
  }
}

async function toggle() {
  expanded.value = !expanded.value
  if (expanded.value) {
    await loadSources()
    start()
  } else {
    stop()
  }
}

function refresh() {
  start()
}

watch(selected, () => {
  if (expanded.value) start()
})

watch(live, (on) => {
  if (!expanded.value) return
  if (on) start()
  else stop()
})

watch(maxLines, (value) => {
  const n = Math.min(MAX_LINES, Math.max(MIN_LINES, Math.round(Number(value) || DEFAULT_LINES)))
  if (n !== value) {
    maxLines.value = n
    return
  }
  if (hydrating) return

  clearTimeout(saveTimer)
  saveTimer = setTimeout(async () => {
    try {
      const data = await postJSON('/api/logs/config', { lines: n })
      if (Number(data.lines) && Number(data.lines) !== n) {
        hydrating = true
        maxLines.value = Number(data.lines)
        hydrating = false
      }
    } catch (err) {
      toast.add({ severity: 'error', summary: 'Cannot save log setting', detail: err.message, life: 4500 })
    }
    trim()
    if (expanded.value && live.value) start()
  }, 400)
})

onBeforeUnmount(() => {
  clearTimeout(saveTimer)
  stop()
})
</script>

<template>
  <section class="card">
    <div class="card-title-row" @click="toggle">
      <div style="display: flex; align-items: center; gap: 8px">
        <span class="card-title">📜 Logs</span>
        <Tag
          v-if="expanded"
          :value="status"
          :severity="statusSeverity[status] || 'secondary'"
        />
      </div>
      <span class="card-hint">{{ expanded ? 'hide' : 'show' }}</span>
    </div>

    <div v-show="expanded" style="margin-top: 14px">
      <div class="row2">
        <Select
          v-model="selected"
          :options="sources"
          option-label="name"
          option-value="id"
          placeholder="Select log"
          fluid
        />
        <Button
          icon="pi pi-refresh"
          severity="secondary"
          outlined
          style="flex: 0 0 auto"
          title="Reload log"
          @click="refresh"
        />
      </div>

      <div class="setting-row" style="margin-top: 12px">
        <div>
          <div class="setting-label">Live tail</div>
          <div class="muted">Push new entries as they are written (inotify)</div>
        </div>
        <ToggleSwitch v-model="live" />
      </div>

      <div class="field" style="margin-top: 12px">
        <label class="field-label">Kept entries</label>
        <InputNumber
          v-model="maxLines"
          :min="16"
          :max="4000"
          :step="16"
          show-buttons
          fluid
        />
        <div class="muted" style="margin-top: 5px">
          The most recent N entries are kept in memory and older entries are discarded
          automatically (default 128). Nothing is written to disk.
        </div>
      </div>

      <div v-if="error" class="form-error">{{ error }}</div>

      <pre ref="pre" class="log-view" @scroll="onScroll">{{
        lines.length ? lines.join('\n') : '— no entries —'
      }}</pre>
    </div>
  </section>
</template>
