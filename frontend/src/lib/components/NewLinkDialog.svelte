<script lang="ts">
  // Before a link exists: what it's for, and who may use it. Asked up front
  // because it decides what happens to the second person, and a link pasted
  // into a group chat can't be taken back. The last choice of who is remembered.
  import { User, Users, Link2, FolderOpen, Upload, Download } from '@lucide/svelte'
  import Dialog from './Dialog.svelte'
  import { app, createAskLink, createLink } from '../state.svelte'
  import { formatSize } from '../format'
  import type { PickedFile } from '../api'
  import { t, what as describe } from '../i18n.svelte'

  const KEY = 'tropicalpeer.linkScope'
  let unlimited = $state(read() === 'anyone')
  // Not remembered: letting strangers offer files is a choice for each link.
  let replies = $state(false)
  let asking = $state(false)
  let purpose = $state('')
  function read() {
    try {
      return localStorage.getItem(KEY)
    } catch {
      return null
    }
  }

  const files = $derived((app.linkDraft ?? []) as PickedFile[])
  const named = $derived(files.filter((f): f is Exclude<PickedFile, string> => typeof f !== 'string'))
  const names = $derived(files.map((f) => (typeof f === 'string' ? f.split(/[\\/]/).pop()! : f.name)))
  const total = $derived(named.length === files.length && named.every((f) => f.size >= 0) ? named.reduce((n, f) => n + f.size, 0) : -1)

  function close() {
    app.linkDraft = null
  }

  function go() {
    const chosen = files
    try {
      localStorage.setItem(KEY, unlimited ? 'anyone' : 'one')
    } catch {}
    close()
    if (asking && !chosen.length) createAskLink(purpose, unlimited)
    else createLink(chosen, unlimited, replies)
  }

  const options = [
    { anyone: false, icon: User, title: 'links.onePerson', hint: 'links.onePersonHint' },
    { anyone: true, icon: Users, title: 'links.anyone', hint: 'links.anyoneHint' },
  ]
  const modes = [
    { ask: false, icon: Download, title: 'links.modeShare', hint: 'links.modeShareHint' },
    { ask: true, icon: Upload, title: 'links.modeAsk', hint: 'links.modeAskHint' },
  ]
  const optionClass = (on: boolean) =>
    `flex cursor-pointer items-start gap-3 rounded-md border px-3.5 py-3 transition-colors duration-150
     has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-primary
     ${on ? 'border-primary bg-primary-soft' : 'border-line hover:bg-surface-2'}`
</script>

{#if app.linkDraft}
  <Dialog title={t('links.new')} onclose={close}>
    {#if files.length}
      <div class="-mt-1 mb-5">
        <div class="display truncate text-[17px] font-medium">{describe(names.map((name) => ({ name })))}</div>
        <div class="truncate text-sm text-muted">
          {[total >= 0 ? formatSize(total) : '', files.length > 1 ? names.join(', ') : ''].filter(Boolean).join(' · ')}
        </div>
      </div>
    {:else}
      <!-- Files already chosen answer this one: they are to be shared. -->
      <fieldset class="mb-5">
        <legend class="mb-2 text-sm font-medium">{t('links.whatFor')}</legend>
        <div class="grid grid-cols-2 gap-2 max-sm:grid-cols-1">
          {#each modes as m (m.title)}
            {@const Icon = m.icon}
            <label class={optionClass(asking === m.ask)}>
              <input type="radio" name="link-mode" class="sr-only" checked={asking === m.ask} onchange={() => (asking = m.ask)} />
              <Icon size={18} strokeWidth={1.75} class="mt-0.5 shrink-0 {asking === m.ask ? 'text-primary' : 'text-muted'}" />
              <span class="min-w-0">
                <span class="block font-medium">{t(m.title)}</span>
                <span class="block text-sm text-muted">{t(m.hint)}</span>
              </span>
            </label>
          {/each}
        </div>
      </fieldset>
      {#if asking}
        <label class="mb-5 block">
          <span class="mb-2 block text-sm font-medium">{t('links.purposeLabel')}</span>
          <input class="field w-full" type="text" maxlength="120" placeholder={t('links.purposePlaceholder')} bind:value={purpose} />
        </label>
      {/if}
    {/if}

    <fieldset>
      <legend class="mb-2 text-sm font-medium">{t('links.whoCan')}</legend>
      <div class="flex flex-col gap-2">
        {#each options as o (o.title)}
          {@const Icon = o.icon}
          <label class={optionClass(unlimited === o.anyone)}>
            <input type="radio" name="link-scope" class="sr-only" checked={unlimited === o.anyone} onchange={() => (unlimited = o.anyone)} />
            <Icon size={18} strokeWidth={1.75} class="mt-0.5 shrink-0 {unlimited === o.anyone ? 'text-primary' : 'text-muted'}" />
            <span class="min-w-0">
              <span class="block font-medium">{t(o.title)}</span>
              <span class="block text-sm text-muted">{t(asking ? (o.anyone ? 'links.askAnyoneHint' : 'links.askOneHint') : o.hint)}</span>
            </span>
          </label>
        {/each}
      </div>
    </fieldset>

    {#if !asking}
      <label class="mt-4 flex cursor-pointer items-start gap-3">
        <input type="checkbox" class="check mt-0.5" bind:checked={replies} />
        <span class="min-w-0">
          <span class="block font-medium">{t('links.allowReplies')}</span>
          <span class="block text-sm text-muted">{t(unlimited ? 'links.allowRepliesHintAnyone' : 'links.allowRepliesHintOne')}</span>
        </span>
      </label>
    {:else}
      <p class="mt-4 text-sm text-muted">{t('links.askApproval')}</p>
    {/if}

    {#if app.device}
      <p class="mt-4 text-sm text-muted">{t('links.seenAs', { name: app.device.name })}</p>
    {/if}

    <div class="mt-6 flex justify-end gap-3 max-sm:flex-col-reverse max-sm:items-stretch">
      <button class="btn-secondary" onclick={close}>{t('common.cancel')}</button>
      <button class="btn-primary" onclick={go}>
        {#if files.length || asking}
          <Link2 size={16} /> {t('links.create')}
        {:else}
          <FolderOpen size={16} /> {t('links.chooseFiles')}
        {/if}
      </button>
    </div>
  </Dialog>
{/if}
