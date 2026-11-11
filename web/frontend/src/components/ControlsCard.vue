<script setup>
import Button from 'primevue/button'

defineProps({
  running: { type: Boolean, default: false },
})

const emit = defineEmits(['open', 'start', 'stop'])
</script>

<template>
  <section class="card">
    <Button
      label="Open Syncthing UI"
      icon="pi pi-external-link"
      class="btn-block"
      @click="emit('open')"
    />
    <div class="btn-row">
      <Button
        label="Start"
        icon="pi pi-play"
        severity="success"
        :disabled="running"
        @click="emit('start')"
      />
      <Button
        label="Stop"
        icon="pi pi-stop"
        severity="danger"
        :disabled="!running"
        @click="emit('stop')"
      />
    </div>
  </section>
</template>
