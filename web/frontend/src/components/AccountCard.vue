<script setup>
import { ref } from 'vue'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import { postJSON } from '../api'

defineProps({
  username: { type: String, default: 'admin' },
})

const toast = useToast()
const expanded = ref(false)
const busy = ref(false)

const newUsername = ref('')
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')

function reset() {
  newUsername.value = ''
  currentPassword.value = ''
  newPassword.value = ''
  confirmPassword.value = ''
}

async function updateAccount() {
  const wantsUsername = newUsername.value.trim() !== ''
  const wantsPassword = newPassword.value !== ''

  if (!currentPassword.value) {
    toast.add({ severity: 'warn', summary: 'Enter your current password', life: 2500 })
    return
  }
  if (!wantsUsername && !wantsPassword) {
    toast.add({ severity: 'warn', summary: 'Enter a new username and/or password', life: 3000 })
    return
  }
  if (wantsPassword && newPassword.value !== confirmPassword.value) {
    toast.add({ severity: 'error', summary: 'Passwords do not match', life: 3000 })
    return
  }
  if (wantsPassword && newPassword.value.length < 4) {
    toast.add({ severity: 'error', summary: 'Password must be at least 4 characters', life: 3000 })
    return
  }

  busy.value = true
  try {
    await postJSON('/api/account', {
      current_password: currentPassword.value,
      new_username: wantsUsername ? newUsername.value.trim() : '',
      new_password: wantsPassword ? newPassword.value : '',
    })
    reset()
    toast.add({
      severity: 'success',
      summary: 'Account updated — re-login required',
      life: 4500,
    })
  } catch (err) {
    toast.add({ severity: 'error', summary: 'Update failed', detail: err.message, life: 5000 })
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="card">
    <div class="card-title-row" @click="expanded = !expanded">
      <span class="setting-label">🔑 Account</span>
      <span class="card-hint">{{ expanded ? 'hide' : 'show' }}</span>
    </div>

    <div v-show="expanded" style="margin-top: 14px">
      <div class="muted" style="margin-bottom: 12px">
        Current login: <code>{{ username }}</code>
      </div>

      <div class="field">
        <label class="field-label">New username <span class="muted">(leave empty to keep)</span></label>
        <InputText v-model="newUsername" fluid placeholder="New username" />
      </div>

      <div class="field">
        <label class="field-label">Current password <span class="muted">(required)</span></label>
        <InputText v-model="currentPassword" type="password" fluid placeholder="Current password" />
      </div>

      <div class="field">
        <label class="field-label">New password <span class="muted">(leave empty to keep)</span></label>
        <InputText v-model="newPassword" type="password" fluid placeholder="New password (min 4 chars)" />
      </div>

      <div class="field">
        <label class="field-label">Confirm new password</label>
        <InputText v-model="confirmPassword" type="password" fluid placeholder="Confirm new password" />
      </div>

      <Button
        label="Update Account"
        icon="pi pi-check"
        severity="secondary"
        outlined
        class="btn-block"
        :loading="busy"
        @click="updateAccount"
      />
    </div>
  </section>
</template>
