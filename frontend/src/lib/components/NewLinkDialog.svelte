<script lang="ts">
  // Before a link exists: who may download from it. Asked up front because
  // it decides what happens to the second person, and a link pasted into a
  // group chat can't be taken back. The last choice is remembered.
  import { User, Users, Link2, FolderOpen } from '@lucide/svelte'
  import Dialog from './Dialog.svelte'
  import { app, createLink } from '../state.svelte'
  import { formatSize } from '../format'
  import type { PickedFile } from '../api'
  import { t, what as describe } from '../i18n.svelte'

  const KEY = 'tropicalpeer.linkScope'
  let unlimited = $state(read() === 'anyone')
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
    createLink(chosen, unlimited)
  }

  const options = [
    { anyone: false, icon: User, title: 'links.onePerson', hint: 'links.onePersonHint' },
    { anyone: true, icon: Users, title: 'links.anyone', hint: 'links.anyoneHint' },
  ]
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
    {/if}

    <fieldset>
      <legend class="mb-2 text-sm font-medium">{t('links.whoCan')}</legend>
      <div class="flex flex-col gap-2">
        {#each options as o (o.title)}
          {@const Icon = o.icon}
          <label
            class="flex cursor-pointer items-start gap-3 rounded-md border px-3.5 py-3 transition-colors duration-150
              has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-primary
              {unlimited === o.anyone ? 'border-primary bg-primary-soft' : 'border-line hover:bg-surface-2'}"
          >
            <input type="radio" name="link-scope" class="sr-only" checked={unlimited === o.anyone} onchange={() => (unlimited = o.anyone)} />
            <Icon size={18} strokeWidth={1.75} class="mt-0.5 shrink-0 {unlimited === o.anyone ? 'text-primary' : 'text-muted'}" />
            <span class="min-w-0">
              <span class="block font-medium">{t(o.title)}</span>
              <span class="block text-sm text-muted">{t(o.hint)}</span>
            </span>
          </label>
        {/each}
      </div>
    </fieldset>

    {#if app.device}
      <p class="mt-4 text-sm text-muted">{t('links.seenAs', { name: app.device.name })}</p>
    {/if}

    <div class="mt-6 flex justify-end gap-3 max-sm:flex-col-reverse max-sm:items-stretch">
      <button class="btn-secondary" onclick={close}>{t('common.cancel')}</button>
      <button class="btn-primary" onclick={go}>
        {#if files.length}
          <Link2 size={16} /> {t('links.create')}
        {:else}
          <FolderOpen size={16} /> {t('links.chooseFiles')}
        {/if}
      </button>
    </div>
  </Dialog>
{/if}
