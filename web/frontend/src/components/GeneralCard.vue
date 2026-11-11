<script setup>
import { ref, watch } from 'vue'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import ToggleSwitch from 'primevue/toggleswitch'
import { post } from '../api'

const props = defineProps({
  autostart: { type: Boolean, default: true },
  running: { type: Boolean, default: false },
})

const toast = useToast()
const autoStart = ref(props.autostart)
const savingAutostart = ref(false)
const updating = ref(false)
const resultVisible = ref(false)
const result = ref({ ok: true, text: '' })

watch(
  () => props.autostart,
  (v) => {
    autoStart.value = v
  },
)

async function toggleAutostart() {
  savingAutostart.value = true
  const desired = autoStart.value
  try {
    await post(desired ? '/api/autostart/enable' : '/api/autostart/disable')
    toast.add({
      severity: 'success',
      summary: desired ? 'Autostart enabled' : 'Autostart disabled',
      life: 2200,
    })
  } catch (err) {
    autoStart.value = !desired
    toast.add({ severity: 'error', summary: 'Failed', detail: err.message, life: 5000 })
  } finally {
    savingAutostart.value = false
  }
}

async function checkUpdate() {
  updating.value = true
  try {
    const data = await post('/api/update')
    result.value = { ok: true, text: data.message || 'Update complete' }
  } catch (err) {
    result.value = { ok: false, text: err.message }
  } finally {
    updating.value = false
    resultVisible.value = true
  }
}
</script>

<template>
  <section class="card">
    <div class="setting-row">
      <div>
        <div class="setting-label">Auto-start on boot</div>
        <div class="muted">Launch Syncthing when the device boots</div>
      </div>
      <ToggleSwitch v-model="autoStart" :disabled="savingAutostart" @update:model-value="toggleAutostart" />
    </div>

    <div class="subsec">
      <Button
        label="Check &amp; update Syncthing"
        icon="pi pi-refresh"
        severity="secondary"
        outlined
        class="btn-block"
        :loading="updating"
        @click="checkUpdate"
      />
    </div>

    <Dialog
      v-model:visible="resultVisible"
      header="Update result"
      modal
      :style="{ width: 'min(92vw, 420px)' }"
    >
      <p
        style="white-space: pre-wrap; margin: 0; font-size: 13px; line-height: 1.6"
        :style="{ color: result.ok ? '#9be7b0' : '#ffb4b2' }"
      >
        {{ result.text }}
      </p>
      <template #footer>
        <Button label="Close" @click="resultVisible = false" />
      </template>
    </Dialog>
  </section>
</template>
