<script setup>
import { ref } from 'vue'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import { api, postJSON } from '../api'

const toast = useToast()
const expanded = ref(false)
const loading = ref(false)
const saving = ref(false)
const server = ref('')

const presets = [
  { label: 'auto', value: '' },
  { label: 'DoH alidns', value: 'https://dns.alidns.com/dns-query' },
  { label: 'DoT 1.1.1.1', value: 'tls://1.1.1.1' },
  { label: 'DoH 1.1.1.1', value: 'https://1.1.1.1/dns-query' },
  { label: 'DoH Cloudflare', value: 'https://dns.cloudflare.com/dns-query' },
]

async function toggle() {
  expanded.value = !expanded.value
  if (expanded.value) await load()
}

async function load() {
  loading.value = true
  try {
    const data = await api('/api/dns-config')
    server.value = data.dns || ''
  } catch (err) {
    toast.add({ severity: 'error', summary: 'Cannot load DNS config', detail: err.message, life: 4500 })
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    const data = await postJSON('/api/dns-config', { dns: server.value })
    toast.add({ severity: 'success', summary: data.message || 'DNS saved', life: 3000 })
    await load()
  } catch (err) {
    toast.add({ severity: 'error', summary: 'DNS save failed', detail: err.message, life: 5000 })
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="card">
    <div class="card-title-row" @click="toggle">
      <span class="setting-label">🌐 DNS Resolver</span>
      <span class="card-hint">{{ expanded ? 'hide' : 'show' }}</span>
    </div>

    <div v-show="expanded" style="margin-top: 14px">
      <div class="muted" style="margin-bottom: 10px">
        Used for the update check. Leave empty to use Android's system DNS resolver, or
        set an IP (<code>192.168.1.1</code>, <code>192.168.1.1:5353</code>), a DoH URL
        (<code>https://…</code>) or a DoT server (<code>tls://…</code>).
      </div>

      <div class="dns-chips">
        <Button
          v-for="preset in presets"
          :key="preset.value"
          :label="preset.label"
          size="small"
          severity="secondary"
          outlined
          @click="server = preset.value"
        />
      </div>

      <InputText v-model="server" fluid placeholder="DNS server (empty = auto)" style="margin-bottom: 8px" />

      <Button label="Save DNS" severity="secondary" outlined class="btn-block" :loading="saving" @click="save" />
    </div>
  </section>
</template>
