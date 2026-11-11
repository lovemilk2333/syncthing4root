<script setup>
import { reactive, ref, watch } from 'vue'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import InputNumber from 'primevue/inputnumber'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import ToggleSwitch from 'primevue/toggleswitch'
import { api, postJSON } from '../api'

const props = defineProps({
  config: { type: Object, required: true },
  state: { type: Object, required: true },
})

const emit = defineEmits(['saved'])
const toast = useToast()

const expanded = ref(false)
const dirty = ref(false)
const saving = ref(false)
const exprError = ref('')
const wifiInput = ref('')

const form = reactive({
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
})

const probeTypes = [
  { label: 'Ping (ICMP)', value: 'ping' },
  { label: 'TCPing', value: 'tcp' },
]

const termTokens = ['wifi', 'cellular', 'power', 'probe', 'AND', 'OR', 'NOT', '(', ')']

function fill(cfg) {
  if (!cfg) return
  form.enabled = !!cfg.enabled
  form.expr = cfg.expr || ''
  form.interval_sec = cfg.interval_sec ?? 30
  form.wifi.enabled = !!(cfg.wifi && cfg.wifi.enabled)
  form.wifi.whitelist = [...((cfg.wifi && cfg.wifi.whitelist) || [])]
  form.cellular.enabled = !!(cfg.cellular && cfg.cellular.enabled)
  form.power.enabled = !!(cfg.power && cfg.power.enabled)
  const probe = cfg.probe || {}
  form.probe.enabled = !!probe.enabled
  form.probe.type = probe.type || 'ping'
  form.probe.target = probe.target || ''
  form.probe.port = probe.port ?? 22
  form.probe.timeout_ms = probe.timeout_ms ?? 2000
  form.probe.up_threshold = probe.up_threshold ?? 2
  form.probe.down_threshold = probe.down_threshold ?? 3
}

watch(
  () => props.config,
  (cfg) => {
    if (!dirty.value) fill(cfg)
  },
  { immediate: true },
)

function markDirty() {
  dirty.value = true
  exprError.value = ''
}

function addWifi() {
  const v = (wifiInput.value || '').trim()
  if (!v) return
  if (!form.wifi.whitelist.includes(v)) form.wifi.whitelist.push(v)
  wifiInput.value = ''
  dirty.value = true
}

function removeWifi(index) {
  form.wifi.whitelist.splice(index, 1)
  dirty.value = true
}

async function scanWifi() {
  try {
    const d = await api('/api/netpolicy/scan')
    if (d.transport !== 'wifi' || !d.ssid) {
      const dev = d.iface ? `, dev: ${d.iface}` : ''
      toast.add({
        severity: 'warn',
        summary: 'SSID unavailable',
        detail: `transport: ${d.transport}${dev}`,
        life: 4000,
      })
      return
    }
    if (!form.wifi.whitelist.includes(d.ssid)) form.wifi.whitelist.push(d.ssid)
    dirty.value = true
    toast.add({ severity: 'success', summary: 'Added SSID', detail: d.ssid, life: 2500 })
  } catch (err) {
    toast.add({ severity: 'error', summary: 'Scan failed', detail: err.message, life: 4500 })
  }
}

function insertToken(token) {
  const isWord = /^[A-Za-z]/.test(token)
  let v = form.expr || ''
  if (isWord && v && !/\s$/.test(v)) v += ' '
  v += token + (isWord ? ' ' : '')
  form.expr = v
  markDirty()
}

function clearExpr() {
  form.expr = ''
  markDirty()
}

async function save() {
  saving.value = true
  exprError.value = ''
  const payload = {
    enabled: form.enabled,
    expr: (form.expr || '').trim(),
    interval_sec: form.interval_sec || 30,
    wifi: { enabled: form.wifi.enabled, whitelist: [...form.wifi.whitelist] },
    cellular: { enabled: form.cellular.enabled },
    power: { enabled: form.power.enabled },
    probe: {
      enabled: form.probe.enabled,
      type: form.probe.type,
      target: (form.probe.target || '').trim(),
      port: form.probe.port || 22,
      timeout_ms: form.probe.timeout_ms || 2000,
      up_threshold: form.probe.up_threshold || 2,
      down_threshold: form.probe.down_threshold || 3,
    },
  }
  try {
    const data = await postJSON('/api/netpolicy', payload)
    dirty.value = false
    fill(data.config)
    emit('saved', data.config)
    toast.add({ severity: 'success', summary: 'Network policy saved', life: 2500 })
  } catch (err) {
    exprError.value = err.message
    toast.add({ severity: 'error', summary: 'Save failed', detail: err.message, life: 5000 })
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="card">
    <div class="card-title-row" @click="expanded = !expanded">
      <div style="display: flex; align-items: center; gap: 8px">
        <span class="card-title">📶 Syncthing Running Policy</span>
        <Tag :value="config.enabled ? 'on' : 'off'" :severity="config.enabled ? 'info' : 'secondary'" />
      </div>
      <span class="card-hint">{{ expanded ? 'hide' : 'show' }}</span>
    </div>

    <div v-show="expanded" style="margin-top: 14px">
      <div class="policy-state">
        <template v-if="config.enabled">
          Decision:
          <span :class="state.should_run ? 'decision-run' : 'decision-stop'">
            {{ state.should_run ? 'should run' : 'should stop' }}
          </span>
          <div class="muted" style="margin-top: 3px">{{ state.explanation }}</div>
        </template>
        <span v-else class="muted">Policy disabled — manual control.</span>
      </div>

      <div class="setting-row" style="margin-bottom: 14px">
        <span class="setting-label">Enable auto start/stop</span>
        <ToggleSwitch v-model="form.enabled" @update:model-value="markDirty" />
      </div>

      <div class="field">
        <label class="field-label">Run condition (boolean expression)</label>
        <InputText
          v-model="form.expr"
          fluid
          placeholder="e.g. wifi AND NOT cellular"
          style="font-family: monospace"
          @update:model-value="markDirty"
        />
        <div class="token-bar">
          <Button
            v-for="token in termTokens"
            :key="token"
            :label="token"
            size="small"
            severity="secondary"
            outlined
            @click="insertToken(token)"
          />
          <Button label="clear" size="small" severity="secondary" text @click="clearExpr" />
        </div>
        <div class="muted" style="margin-top: 6px">
          Empty = always run. <code>wifi</code> = whitelisted SSID, <code>cellular</code> = mobile
          data, <code>power</code> = battery saver, <code>probe</code> = target reachable. A disabled
          section makes its term always <code>true</code>, except <code>power</code> which is always
          <code>false</code>.
        </div>
        <div v-if="exprError" class="form-error">{{ exprError }}</div>
      </div>

      <!-- WiFi whitelist -->
      <div class="subsec">
        <div class="subsec-head">
          <span class="setting-label">WiFi whitelist <Tag value="wifi" severity="secondary" /></span>
          <ToggleSwitch v-model="form.wifi.enabled" @update:model-value="markDirty" />
        </div>
        <div class="tokens">
          <span v-for="(ssid, i) in form.wifi.whitelist" :key="ssid" class="ssid-chip">
            {{ ssid }}
            <Button
              icon="pi pi-times"
              size="small"
              severity="danger"
              text
              rounded
              style="width: 1.5rem; height: 1.5rem"
              @click="removeWifi(i)"
            />
          </span>
          <span v-if="form.wifi.whitelist.length === 0" class="muted">No SSID added</span>
        </div>
        <div class="row2">
          <InputText v-model="wifiInput" fluid placeholder="SSID name" @keyup.enter="addWifi" />
          <Button label="Add" severity="secondary" outlined style="flex: 0 0 auto" @click="addWifi" />
        </div>
        <Button
          label="Scan current SSID"
          icon="pi pi-wifi"
          severity="secondary"
          outlined
          class="btn-block"
          style="margin-top: 8px"
          @click="scanWifi"
        />
      </div>

      <!-- Cellular -->
      <div class="subsec">
        <div class="subsec-head">
          <span class="setting-label">Cellular detection <Tag value="cellular" severity="secondary" /></span>
          <ToggleSwitch v-model="form.cellular.enabled" @update:model-value="markDirty" />
        </div>
        <div class="muted">
          <code>cellular</code> is true on mobile data. Use <code>NOT cellular</code> to stop on
          cellular.
        </div>
      </div>

      <!-- Power save -->
      <div class="subsec">
        <div class="subsec-head">
          <span class="setting-label">Battery-saver detection <Tag value="power" severity="secondary" /></span>
          <ToggleSwitch v-model="form.power.enabled" @update:model-value="markDirty" />
        </div>
        <div class="muted">
          <code>power</code> is true in battery saver (false when this check is off). Use
          <code>NOT power</code> to stop in battery saver.
        </div>
      </div>

      <!-- Probe -->
      <div class="subsec">
        <div class="subsec-head">
          <span class="setting-label">Reachability probe <Tag value="probe" severity="secondary" /></span>
          <ToggleSwitch v-model="form.probe.enabled" @update:model-value="markDirty" />
        </div>
        <div class="field">
          <label class="field-label">Check interval (seconds)</label>
          <InputNumber v-model="form.interval_sec" :min="5" :max="3600" fluid @update:model-value="markDirty" />
          <div class="muted" style="margin-top: 5px">
            Probe only. WiFi, mobile data and battery-saver changes arrive as Android events and are
            applied immediately.
          </div>
        </div>
        <div class="row2">
          <div class="field" style="margin-bottom: 0">
            <label class="field-label">Type</label>
            <Select
              v-model="form.probe.type"
              :options="probeTypes"
              option-label="label"
              option-value="value"
              fluid
              @update:model-value="markDirty"
            />
          </div>
          <div v-if="form.probe.type === 'tcp'" class="field" style="margin-bottom: 0">
            <label class="field-label">Port</label>
            <InputNumber v-model="form.probe.port" :min="1" :max="65535" fluid @update:model-value="markDirty" />
          </div>
        </div>
        <div class="field" style="margin-top: 12px">
          <label class="field-label">Target (host or IP)</label>
          <InputText v-model="form.probe.target" fluid placeholder="192.168.1.1" @update:model-value="markDirty" />
        </div>
        <div class="row2">
          <div class="field" style="margin-bottom: 0">
            <label class="field-label">Timeout (ms)</label>
            <InputNumber v-model="form.probe.timeout_ms" :min="200" :max="60000" fluid @update:model-value="markDirty" />
          </div>
          <div class="field" style="margin-bottom: 0">
            <label class="field-label">Up threshold</label>
            <InputNumber v-model="form.probe.up_threshold" :min="1" fluid @update:model-value="markDirty" />
          </div>
          <div class="field" style="margin-bottom: 0">
            <label class="field-label">Down threshold</label>
            <InputNumber v-model="form.probe.down_threshold" :min="1" fluid @update:model-value="markDirty" />
          </div>
        </div>
      </div>

      <Button
        label="Save Policy"
        icon="pi pi-check"
        class="btn-block"
        style="margin-top: 16px"
        :loading="saving"
        @click="save"
      />
    </div>
  </section>
</template>
