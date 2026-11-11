<script setup>
import { computed } from 'vue'
import Tag from 'primevue/tag'

const props = defineProps({
  status: { type: Object, required: true },
  net: { type: Object, required: true },
  eventSources: { type: Object, default: () => ({ netlink: false, logcat: false, inotify: false }) },
})

const activeSources = computed(() => {
  const s = props.eventSources || {}
  const list = []
  if (s.netlink) list.push('netlink')
  if (s.logcat) list.push('logcat')
  if (s.inotify) list.push('inotify')
  return list
})

const mode = computed(() => (props.net.config.enabled ? 'auto' : 'manual'))

const transport = computed(() => {
  const t = props.net.state.transport
  if (!t) return '—'
  return t === 'other' || t === 'none' ? `(${t})` : t
})

const decision = computed(() => {
  const st = props.net.state
  if (!props.net.config.enabled) return ''
  return st.should_run ? 'should run' : 'should stop'
})
</script>

<template>
  <section class="card">
    <div class="status-row">
      <span class="dot" :class="status.running ? 'green' : 'red'"></span>
      <span>{{ status.running ? 'Running' : 'Stopped' }}</span>
      <Tag
        style="margin-left: auto"
        :value="mode"
        :severity="mode === 'auto' ? 'info' : 'secondary'"
      />
    </div>

    <div class="status-detail" v-if="status.running">
      PID: <code>{{ status.pid }}</code> · UI: <code>{{ status.url }}</code>
    </div>
    <div class="status-detail" v-else>UI: <code>{{ status.url }}</code></div>

    <div class="status-detail">
      Net: <code>{{ transport }}</code>
      <template v-if="net.state.ssid"> · SSID: <code>{{ net.state.ssid }}</code></template>
      <template v-if="net.state.power_save"> · <span style="color: #ef5350">battery saver</span></template>
      <template v-if="decision"> · <span :class="net.state.should_run ? 'decision-run' : 'decision-stop'">{{ decision }}</span></template>
    </div>

    <div class="status-detail">
      Events:
      <template v-if="activeSources.length">
        <code v-for="name in activeSources" :key="name" style="margin-right: 4px">{{ name }}</code>
      </template>
      <span v-else style="color: #ef5350">no event source — updates only on manual actions</span>
    </div>
  </section>
</template>
