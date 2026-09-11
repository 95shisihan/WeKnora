<template>
  <div class="plugin-settings">
    <div class="page-header">
      <div>
        <h2>{{ t('pluginSettings.title') }}</h2>
        <p>{{ t('pluginSettings.description') }}</p>
      </div>
      <input ref="fileInput" type="file" accept=".zip,application/zip" hidden @change="handleFile" />
      <input ref="upgradeInput" type="file" accept=".zip,application/zip" hidden @change="handleUpgrade" />
      <t-button theme="primary" :loading="uploading" :disabled="!!changing" @click="fileInput?.click()">
        <template #icon><t-icon name="upload" /></template>
        {{ t('pluginSettings.upload') }}
      </t-button>
    </div>

    <div class="security-note">
      <t-icon name="secured" size="18px" />
      <span>{{ t('pluginSettings.securityNote') }}</span>
    </div>

    <div v-if="loading" class="state-box">
      <t-loading size="small" />
      <span>{{ t('pluginSettings.loading') }}</span>
    </div>
    <div v-else-if="plugins.length === 0" class="state-box">
      {{ t('pluginSettings.empty') }}
    </div>
    <div v-else class="plugin-list">
      <article v-for="plugin in plugins" :key="plugin.plugin_id" class="plugin-card">
        <div class="plugin-main">
          <div class="plugin-title-row">
            <strong>{{ plugin.name }}</strong>
            <span class="version">v{{ plugin.version }}</span>
            <span class="source-badge">{{ isBuiltin(plugin) ? t('pluginSettings.builtin') : t('pluginSettings.custom') }}</span>
            <span class="status-badge" :class="`status-${plugin.state}`">
              {{ t(`pluginSettings.states.${plugin.state}`) }}
            </span>
          </div>
          <code>{{ plugin.plugin_id }}</code>
          <div class="extensions">
            <span v-for="extension in plugin.extension_types" :key="extension">{{ extension }}</span>
          </div>
          <p v-if="plugin.last_error" class="plugin-error">{{ plugin.last_error }}</p>
          <p v-if="plugin.network" class="network-policy">{{ plugin.network.outbound ? t('pluginSettings.directNetwork') : plugin.network.http ? t('pluginSettings.controlledNetwork') : t('pluginSettings.noNetwork') }}</p>
          <p v-for="(rule, index) in plugin.network?.http?.rules || []" :key="index" class="network-policy">{{ rule.methods.join(', ') }} · {{ rule.hosts.join(', ') }}</p>
        </div>
        <div class="plugin-actions">
        <t-button v-if="!isBuiltin(plugin)" variant="outline" :disabled="uploading || !!changing" @click="chooseUpgrade(plugin)">
          {{ t('pluginSettings.upgrade') }}
        </t-button>
        <t-switch
          :value="plugin.state === 'healthy'"
          :loading="changing === plugin.plugin_id || plugin.state === 'starting'"
          :disabled="plugin.state === 'starting' || uploading || !!changing"
          @change="toggleFromSwitch(plugin, $event)"
        />
        </div>
      </article>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  disablePlugin,
  enablePlugin,
  installPlugin,
  upgradePlugin,
  listPlugins,
  type PluginStatus,
} from '@/api/system'

const { t } = useI18n()
const plugins = ref<PluginStatus[]>([])
const loading = ref(false)
const uploading = ref(false)
const changing = ref('')
const fileInput = ref<HTMLInputElement>()
const upgradeInput = ref<HTMLInputElement>()
const upgradeTarget = ref<PluginStatus>()

function chooseUpgrade(plugin: PluginStatus) {
  if (plugin.state !== 'disabled') {
    MessagePlugin.warning(t('pluginSettings.upgradeStopFirst'))
    return
  }
  upgradeTarget.value = plugin
  upgradeInput.value?.click()
}

async function handleUpgrade(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  const target = upgradeTarget.value
  input.value = ''
  upgradeTarget.value = undefined
  if (!file || !target) return
  if (!file.name.toLowerCase().endsWith('.zip') || file.size > 64 * 1024 * 1024) {
    MessagePlugin.warning(t('pluginSettings.upgradeZipLimit'))
    return
  }
  changing.value = target.plugin_id
  try {
    const confirmed = await new Promise<boolean>(resolve => {
      const dialog = DialogPlugin.confirm({
        header: t('pluginSettings.upgrade'),
        body: t('pluginSettings.upgradeConfirm', { name: target.name, version: target.version, file: file.name }),
        onConfirm: () => { resolve(true); dialog.destroy() },
        onClose: () => { resolve(false); dialog.destroy() },
      })
    })
    if (!confirmed) return
    const result = await upgradePlugin(target.plugin_id, file)
    MessagePlugin.success(t('pluginSettings.upgradeSuccess', { version: result.plugin.version, count: result.renamed_data_sources }))
    await loadPlugins()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('pluginSettings.upgradeFailed'))
    await loadPlugins()
  } finally {
    changing.value = ''
  }
}

const isBuiltin = (plugin: PluginStatus) => plugin.plugin_id.startsWith('builtin.')

async function loadPlugins() {
  loading.value = true
  try {
    plugins.value = await listPlugins()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('pluginSettings.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function handleFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (!file.name.toLowerCase().endsWith('.zip')) {
    MessagePlugin.warning(t('pluginSettings.zipOnly'))
    return
  }
  uploading.value = true
  try {
    const installed = await installPlugin(file)
	if (installed.network?.http) {
	  await loadPlugins()
	  MessagePlugin.success(t('pluginSettings.installedReview'))
	  return
	}
    try {
      await enablePlugin(installed.plugin_id)
      MessagePlugin.success(t('pluginSettings.installSuccess', { name: installed.name }))
    } catch (error: any) {
      MessagePlugin.warning(error?.message || t('pluginSettings.enableAfterInstallFailed'))
    }
    await loadPlugins()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('pluginSettings.installFailed'))
  } finally {
    uploading.value = false
  }
}

async function togglePlugin(plugin: PluginStatus, enabled: boolean) {
	if (enabled && plugin.network?.http && !plugin.http_approved && !(await approveHTTP(plugin))) return
  changing.value = plugin.plugin_id
  try {
    const status = enabled
      ? await enablePlugin(plugin.plugin_id, plugin.http_policy_digest)
      : await disablePlugin(plugin.plugin_id)
    const index = plugins.value.findIndex((item) => item.plugin_id === plugin.plugin_id)
    if (index >= 0) plugins.value[index] = status
    MessagePlugin.success(t(enabled ? 'pluginSettings.enableSuccess' : 'pluginSettings.disableSuccess'))
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('pluginSettings.changeFailed'))
    await loadPlugins()
  } finally {
    changing.value = ''
  }
}

function approveHTTP(plugin: PluginStatus): Promise<boolean> {
  const policy = plugin.network!.http!
  const rules = policy.rules.map(rule => `${rule.methods.join(', ')}: ${rule.hosts.join(', ')}`).join('; ')
  return new Promise(resolve => {
    const dialog = DialogPlugin.confirm({
      header: t('pluginSettings.approveTitle'),
      body: t('pluginSettings.approveBody', { name: plugin.name, rules, redirects: policy.maxRedirects, seconds: policy.timeoutSeconds, bytes: policy.maxResponseBytes, requestBytes: policy.maxRequestBytes }),
      onConfirm: () => { resolve(true); dialog.destroy() },
      onClose: () => { resolve(false); dialog.destroy() },
    })
  })
}

function toggleFromSwitch(plugin: PluginStatus, value: unknown) {
  void togglePlugin(plugin, Boolean(value))
}

onMounted(loadPlugins)
</script>

<style scoped>
.plugin-settings { display: flex; flex-direction: column; gap: 18px; }
.page-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 24px; }
.page-header h2 { margin: 0 0 6px; font-size: 22px; color: var(--td-text-color-primary); }
.page-header p { margin: 0; color: var(--td-text-color-secondary); line-height: 1.6; }
.security-note { display: flex; align-items: flex-start; gap: 10px; padding: 12px 14px; border: 1px solid var(--td-warning-color-3); border-radius: 8px; background: var(--td-warning-color-1); color: var(--td-text-color-secondary); line-height: 1.5; }
.state-box { min-height: 120px; display: flex; align-items: center; justify-content: center; gap: 10px; color: var(--td-text-color-secondary); }
.plugin-list { display: flex; flex-direction: column; gap: 10px; }
.plugin-card { display: flex; align-items: center; justify-content: space-between; gap: 20px; padding: 16px; border: 1px solid var(--td-component-border); border-radius: 10px; background: var(--td-bg-color-container); }
.plugin-main { min-width: 0; }
.plugin-actions { display: flex; align-items: center; gap: 14px; flex-shrink: 0; }
.plugin-title-row { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-bottom: 6px; }
.version, .source-badge, .status-badge, .extensions span { font-size: 12px; }
.version { color: var(--td-text-color-placeholder); }
.source-badge, .status-badge, .extensions span { padding: 2px 7px; border-radius: 999px; background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); }
.status-healthy { background: var(--td-success-color-1); color: var(--td-success-color-7); }
.status-unhealthy { background: var(--td-error-color-1); color: var(--td-error-color-7); }
.status-starting { background: var(--td-brand-color-1); color: var(--td-brand-color-7); }
code { color: var(--td-text-color-placeholder); font-size: 12px; word-break: break-all; }
.extensions { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 10px; }
.plugin-error { margin: 10px 0 0; color: var(--td-error-color); font-size: 12px; white-space: pre-wrap; word-break: break-word; }
.network-policy { margin: 6px 0 0; color: var(--td-text-color-secondary); font-size: 12px; overflow-wrap: anywhere; }
</style>
