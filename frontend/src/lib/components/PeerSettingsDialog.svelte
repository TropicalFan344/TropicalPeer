<script lang="ts">
  import { onMount } from 'svelte'
  import { slide } from 'svelte/transition'
  import { FolderOpen, LoaderCircle, Trash2 } from '@lucide/svelte'
  import Dialog from './Dialog.svelte'
  import Bed from './Bed.svelte'
  import { api, errorText, isAndroid, type Peer, type Policy } from '../api'
  import { toast } from '../state.svelte'
  import { MB, GB } from '../format'
  import { t } from '../i18n.svelte'

  let { peer, onclose, onremove }: { peer: Peer; onclose: () => void; onremove: () => void } = $props()

  // Form state, seeded from the peer once when the dialog opens.
  // svelte-ignore state_referenced_locally
  const initial = peer
  let name = $state(initial.name)
  let policy = $state<Policy>(initial.policy)
  let limitOn = $state(initial.maxAutoSize > 0)
  const initialUnit = initial.maxAutoSize > 0 && initial.maxAutoSize % GB === 0 ? 'GB' : 'MB'
  let limitUnit = $state<'MB' | 'GB'>(initialUnit)
  let limitValue = $state(initial.maxAutoSize > 0 ? initial.maxAutoSize / (initialUnit === 'GB' ? GB : MB) : 500)
  let saveDir = $state(initial.saveDir)
  let defaultDir = $state('')

  let saving = $state(false)
  let error = $state('')

  onMount(async () => {
    defaultDir = (await api.getSettings()).downloadDir
  })

  const options: Policy[] = ['ask', 'auto', 'block']

  const maxAutoSize = $derived(
    policy === 'auto' && limitOn ? Math.round(Math.max(0, limitValue || 0) * (limitUnit === 'GB' ? GB : MB)) : 0,
  )

  async function chooseFolder() {
    try {
      const dir = await api.chooseFolder(t('peerSettings.chooseFolder', { name: peer.name }), saveDir || defaultDir)
      if (dir) saveDir = dir
    } catch (e) {
      toast(t('common.folderPickerFailed', { error: errorText(e) }), 'error', 6000)
    }
  }

  async function save(e: SubmitEvent) {
    e.preventDefault()
    if (policy === 'auto' && limitOn && !(limitValue > 0)) {
      error = t('peerSettings.limitError')
      return
    }
    saving = true
    error = ''
    try {
      // Keep a previously set limit when switching away from auto, so it's
      // still there if the user switches back later.
      const keepLimit = policy !== 'auto' ? peer.maxAutoSize : maxAutoSize
      const updated = await api.updatePeer(peer.id, { name, policy, maxAutoSize: keepLimit, saveDir })
      toast(t('peerSettings.saved', { name: updated.name }))
      onclose()
    } catch (err) {
      error = errorText(err)
    } finally {
      saving = false
    }
  }
</script>

<Dialog title={t('peerSettings.title')} {onclose} width="max-w-lg">
  <form class="flex flex-col gap-5" onsubmit={save}>
    <!-- Identity -->
    <div class="flex items-center gap-4">
      <Bed name={name || peer.name} id={peer.id} size={52} />
      <label class="flex min-w-0 flex-1 flex-col gap-1.5">
        <span class="text-sm font-medium">{t('peerSettings.name')}</span>
        <input class="field" bind:value={name} maxlength="64" spellcheck="false" aria-label={t('peerSettings.nameLabel')} />
      </label>
    </div>
    <div class="-mt-3 pl-[68px] font-mono text-[11px] text-muted">{peer.id}</div>

    <!-- Policy -->
    <fieldset>
      <legend class="mb-2 text-sm font-medium">{t('peerSettings.when', { name: name || peer.name })}</legend>
      <div class="divide-y divide-line overflow-hidden rounded-lg border border-line">
        {#each options as opt (opt)}
          {@const selected = policy === opt}
          <div class="transition-colors {selected ? 'bg-primary-soft/45' : 'hover:bg-surface-2'}">
            <label class="flex cursor-pointer items-start gap-3 px-4 py-3">
              <input class="radio mt-1 shrink-0" type="radio" name="policy" value={opt} bind:group={policy} />
              <span>
                <span class="block font-medium {opt === 'block' ? 'text-bad' : ''}">{t(`peerSettings.${opt}Title`)}</span>
                <span class="block text-sm text-muted">{t(`peerSettings.${opt}Desc`)}</span>
              </span>
            </label>
            {#if opt === 'auto' && selected}
              <div class="flex flex-wrap items-center gap-2 pr-4 pb-3 pl-11 text-sm" transition:slide={{ duration: 150 }}>
                <label class="flex cursor-pointer items-center gap-2">
                  <input type="checkbox" class="check" bind:checked={limitOn} />
                  {t('peerSettings.onlyUpTo')}
                </label>
                <input
                  class="field w-24 py-1.5 disabled:opacity-50"
                  type="number"
                  min="1"
                  step="any"
                  bind:value={limitValue}
                  disabled={!limitOn}
                  aria-label={t('peerSettings.sizeLimit')}
                />
                <select class="field py-1.5 disabled:opacity-50" bind:value={limitUnit} disabled={!limitOn} aria-label={t('peerSettings.sizeUnit')}>
                  <option>MB</option>
                  <option>GB</option>
                </select>
                <span class="text-muted">{t(limitOn ? 'peerSettings.largerAsk' : 'peerSettings.anySize')}</span>
              </div>
            {/if}
          </div>
        {/each}
      </div>
    </fieldset>

    <!-- Folder (desktop only: Android keeps received files in Downloads/TropicalPeer) -->
    {#if !isAndroid}
    <div class="flex flex-col gap-2">
      <span class="text-sm font-medium">{t('peerSettings.saveTo')}</span>
      <div class="flex items-center gap-3 rounded-lg border border-line bg-ground p-3">
        <FolderOpen size={18} class="shrink-0 text-muted" />
        <div class="min-w-0 flex-1">
          <div class="selectable truncate text-sm" title={saveDir || defaultDir}>{saveDir || defaultDir || '…'}</div>
          <div class="text-xs text-muted">{t(saveDir ? 'peerSettings.customFolder' : 'peerSettings.defaultFolder')}</div>
        </div>
        {#if saveDir}
          <button type="button" class="text-sm text-muted hover:text-ink" onclick={() => (saveDir = '')}>{t('peerSettings.useDefault')}</button>
        {/if}
        <button type="button" class="btn-secondary px-3 py-1.5 text-sm" onclick={chooseFolder}>{t('peerSettings.change')}</button>
      </div>
    </div>
    {/if}

    {#if error}
      <p class="text-sm text-bad">{error}</p>
    {/if}

    <!-- Footer -->
    <div class="flex items-center gap-3 border-t border-line pt-4">
      <button type="button" class="mr-auto flex items-center gap-1.5 text-sm text-bad hover:underline" onclick={onremove}>
        <Trash2 size={15} /> {t('peer.remove')}
      </button>
      <button type="button" class="btn-secondary" onclick={onclose}>{t('common.cancel')}</button>
      <button type="submit" class="btn-primary" disabled={saving || !name.trim()}>
        {#if saving}<LoaderCircle size={16} class="animate-spin" />{/if}
        {t('common.save')}
      </button>
    </div>
  </form>
</Dialog>
