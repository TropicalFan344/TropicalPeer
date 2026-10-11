<script lang="ts">
  import { onMount } from 'svelte'
  import { fly } from 'svelte/transition'
  import { dur, easeOutExpo } from '../motion'
  import { LoaderCircle, Copy, Check, RotateCw } from '@lucide/svelte'
  import { api, errorText, isAndroid, isMac, type Settings, type Update } from '../api'
  import { app, toast, restartEngine, activeTransfers } from '../state.svelte'
  import Dialog from '../components/Dialog.svelte'
  import { theme, setTheme, type Theme } from '../theme.svelte'
  import SettingLabel from '../components/SettingLabel.svelte'
  import {
    getReady,
    setReady,
    openNotificationSettings,
    getDownloadFolder,
    chooseDownloadFolder,
    resetDownloadFolder,
    type ReadyState,
    type DownloadFolder,
  } from '../backend'
  import { t, i18n, languages, setLanguage } from '../i18n.svelte'

  const themes: { value: Theme; label: string }[] = [
    { value: 'system', label: isAndroid ? 'settings.matchPhone' : isMac ? 'settings.matchMac' : 'settings.matchWindows' },
    { value: 'light', label: 'settings.light' },
    { value: 'dark', label: 'settings.dark' },
  ]
  const phone = typeof matchMedia !== 'undefined' && matchMedia('(max-width: 639.98px)').matches

  // Appearance: the primary fill slides to the chosen option. Options differ
  // in width (and by language), so the fill measures the one it moves to.
  let segGroup = $state<HTMLElement>()
  const segEls: HTMLElement[] = $state([])
  let seg = $state({ x: 0, w: 0 })
  let segGlide = $state(false) // no slide on first paint, only on a change
  function measureSeg() {
    const el = segEls[themes.findIndex((th) => th.value === theme.value)]
    if (el) seg = { x: el.offsetLeft, w: el.offsetWidth }
  }
  $effect(() => {
    void theme.value
    measureSeg()
  })
  $effect(() => {
    if (!segGroup) return
    const ro = new ResizeObserver(measureSeg) // fonts loading, language changes
    ro.observe(segGroup)
    requestAnimationFrame(() => (segGlide = true))
    return () => ro.disconnect()
  })

  const systemName = $derived(languages.find((l) => l.code === i18n.lang)?.name ?? '')

  type Form = Settings & { deviceName: string }

  let saved = $state<Form | null>(null)
  let form = $state<Form>({
    deviceName: '',
    serverHost: '',
    serverPort: 7777,
    listenPort: 7778,
    downloadDir: '',
    portMapping: true,
  })
  let saving = $state(false)
  let error = $state('')

  async function load() {
    const s = await api.getSettings()
    saved = { ...s, deviceName: app.device?.name ?? '' }
    form = { ...saved }
  }
  onMount(async () => {
    await load()
    // Arriving from a link to a specific row (e.g. Network → Listening port).
    if (app.settingsFocus) {
      document.getElementById(app.settingsFocus)?.scrollIntoView({ block: 'center' })
      app.settingsFocus = ''
    }
  })

  const dirty = $derived(
    saved !== null && (Object.keys(form) as (keyof Form)[]).some((k) => form[k] !== saved![k]),
  )

  async function save(e: SubmitEvent) {
    e.preventDefault()
    if (!saved) return
    saving = true
    error = ''
    try {
      const portChanged = form.listenPort !== saved.listenPort
      if (form.deviceName !== saved.deviceName) await api.setDeviceName(form.deviceName)
      const { deviceName: _, ...settings } = $state.snapshot(form)
      await api.saveSettings(settings)
      await load()
      form.deviceName = app.device?.name ?? form.deviceName
      toast(t('settings.saved'))
      if (portChanged) toast(t('settings.restartPort'), 'info', 10000, { label: t('settings.restartNow'), run: askRestart })
    } catch (err) {
      error = errorText(err)
    } finally {
      saving = false
    }
  }

  function revert() {
    if (saved) form = { ...saved }
    error = ''
  }

  async function pickFolder() {
    try {
      const dir = await api.chooseFolder(t('settings.chooseDownload'), form.downloadDir)
      if (dir) form.downloadDir = dir
    } catch (e) {
      toast(t('common.folderPickerFailed', { error: errorText(e) }), 'error', 6000)
    }
  }

  const serverText = $derived(
    t(app.server.online ? 'settings.serverConnected' : app.server.error ? 'settings.serverRetrying' : 'server.connecting'),
  )
  const serverTone = $derived(app.server.online ? 'text-ok' : app.server.error ? 'text-bad' : 'text-sea')

  // Android: stay online in the background (a foreground service). Applies
  // right away, like Appearance, rather than waiting for Save.
  let ready = $state<ReadyState | null>(null)
  let readyBusy = $state(false)
  if (isAndroid) getReady().then((r) => (ready = r))
  async function toggleReady() {
    if (!ready) return
    readyBusy = true
    try {
      ready = await setReady(!ready.on)
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      readyBusy = false
    }
  }
  async function allowNotifications() {
    try {
      await openNotificationSettings()
    } catch (e) {
      toast(errorText(e), 'info', 6000)
    }
  }
  // Coming back from Android's settings: pick up a changed permission.
  function onvisibilitychange() {
    if (isAndroid && document.visibilityState === 'visible') getReady().then((r) => (ready = r))
  }

  // Android: received files go to Download/TropicalPeer or a folder the user picks.
  let folder = $state<DownloadFolder | null>(null)
  if (isAndroid) getDownloadFolder().then((f) => (folder = f))
  async function changeFolder(reset: boolean) {
    try {
      folder = await (reset ? resetDownloadFolder() : chooseDownloadFolder())
    } catch (e) {
      toast(errorText(e), 'error', 6000)
    }
  }

  // Restart: applies the listening port (and clears a stuck connection)
  // without quitting. Asks first when it would stop files that are moving.
  let confirmRestart = $state(false)
  const running = $derived(activeTransfers().length)
  function askRestart() {
    if (activeTransfers().length) confirmRestart = true
    else restartEngine()
  }
  // The saved port isn't the one in use yet: it takes a restart.
  const portPending = $derived(
    !!saved && saved.listenPort > 0 && !!app.device?.port && saved.listenPort !== app.device.port,
  )

  // On a phone there's no rail foot, so the device ID lives under its name.
  let copied = $state(false)
  async function copyId() {
    if (!app.device) return
    await navigator.clipboard.writeText(app.device.id)
    copied = true
    setTimeout(() => (copied = false), 1500)
  }


  // ---- Updates ----

  function updateStatus(up: Update): string {
    switch (up.state) {
      case 'checking':
        return t('update.checking')
      case 'current':
        return t('update.current', { version: up.current })
      case 'downloading':
        return t('update.downloading', { version: up.version, percent: up.total > 0 ? Math.floor((up.done / up.total) * 100) : 0 })
      case 'ready':
        return t('update.ready', { version: up.version })
      case 'installing':
        return t(isAndroid ? 'update.installingAndroid' : 'update.installing', { version: up.version })
      case 'failed':
        return up.error
      default:
        return up.auto ? '' : t('update.off')
    }
  }

  async function setAutoUpdate(on: boolean) {
    try {
      await api.setAutoUpdate(on)
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function installUpdate() {
    try {
      await api.installUpdate()
    } catch (e) {
      toast(errorText(e), 'error', 6000)
    }
  }
</script>

<svelte:document {onvisibilitychange} />

<div class="page">
  <header>
    <h1 class="page-title">{t('settings.title')}</h1>
    <p class="mt-1 text-muted">{t('settings.subtitle')}</p>
  </header>

  <form class="flex flex-col" onsubmit={save}>
    <section class="row">
      <SettingLabel
        title={t('settings.deviceName')}
        hint={t('settings.deviceNameHint')}
        detail={t('settings.deviceNameDetail')}
      />
      <div>
        <input class="field w-full" bind:value={form.deviceName} maxlength="64" spellcheck="false" aria-label={t('settings.deviceName')} />
        {#if app.device?.id}
          <div class="mt-2 flex items-center gap-2 sm:hidden">
            <span class="selectable font-mono text-sm text-muted">{app.device.id}</span>
            <button
              type="button"
              class="btn-quiet"
              aria-label={copied ? t('device.idCopied') : t('device.copyId')}
              onclick={copyId}
            >
              {#if copied}<Check size={16} class="text-ok" /> {t('common.copied')}{:else}<Copy size={16} /> {t('device.copyIdButton')}{/if}
            </button>
          </div>
        {/if}
      </div>
    </section>

    <section class="row">
      <SettingLabel title={t('settings.appearance')} hint={t('settings.appliesNow')} />
      <div
        bind:this={segGroup}
        class="relative inline-flex w-fit rounded-md border border-line bg-surface p-0.5"
        role="radiogroup"
        aria-label={t('settings.appearance')}
      >
        <span
          class="seg-fill absolute top-0.5 bottom-0.5 left-0 rounded bg-primary {segGlide ? 'seg-glide' : ''}"
          style:transform="translateX({seg.x}px)"
          style:width="{seg.w}px"
          aria-hidden="true"
        ></span>
        {#each themes as th, i (th.value)}
          <label
            bind:this={segEls[i]}
            class="relative cursor-pointer rounded px-3.5 py-1.5 text-sm font-medium transition-colors duration-200 pointer-coarse:py-2.5
              has-focus-visible:outline-2 has-focus-visible:outline-offset-2 has-focus-visible:outline-primary
              {theme.value === th.value ? 'text-primary-ink' : 'text-muted hover:text-ink'}"
          >
            <input class="sr-only" type="radio" name="theme" value={th.value} checked={theme.value === th.value} onchange={() => setTheme(th.value)} />
            {t(th.label)}
          </label>
        {/each}
      </div>
    </section>

    <section class="row">
      <SettingLabel title={t('settings.language')} hint={t('settings.languageHint')} />
      <!-- Applies at once, like Appearance; each language is listed in its own name. -->
      <select
        class="field w-full max-w-72"
        aria-label={t('settings.language')}
        value={i18n.choice}
        onchange={(e) => setLanguage(e.currentTarget.value)}
      >
        <option value="system">{t('settings.matchSystem')}{i18n.choice === 'system' ? ` (${systemName})` : ''}</option>
        {#each languages as l (l.code)}
          <option value={l.code} lang={l.code}>{l.name}</option>
        {/each}
      </select>
    </section>

    {#if isAndroid && ready}
      <section class="row">
        <SettingLabel
          title={t('settings.ready')}
          hint={t('settings.readyHint')}
          detail={t('settings.readyDetail')}
        />
        <div class="flex flex-col gap-2">
          <label class="flex cursor-pointer items-start gap-3">
            <input
              type="checkbox"
              class="mt-0.5 size-4 shrink-0 accent-[var(--primary)] pointer-coarse:size-5"
              checked={ready.on}
              disabled={readyBusy}
              onchange={toggleReady}
            />
            <span class="text-sm">
              <span class="block font-medium">{t('settings.readyToggle')}</span>
              <span class="block text-muted">
                {#if !ready.on}
                  {t('settings.readyOff')}
                {:else if ready.notifications}
                  <span class="text-ok">{t('settings.readyOn')}</span>
                {:else}
                  <span class="text-warn">{t('settings.readyNoNotif')}</span>
                {/if}
              </span>
            </span>
          </label>
          {#if ready.on && !ready.notifications}
            <p class="max-w-[44ch] text-sm text-muted">{t('settings.readyNotifNote')}</p>
            <button
              type="button"
              class="w-fit text-sm font-medium text-primary hover:underline pointer-coarse:-my-1 pointer-coarse:py-2.5"
              onclick={allowNotifications}
            >
              {t('settings.allowNotif')}
            </button>
          {/if}
        </div>
      </section>
    {/if}

    <section class="row">
      <SettingLabel
        title={t('settings.downloadFolder')}
        hint={t('settings.downloadHint')}
        detail={t(isAndroid ? 'settings.downloadDetailPhone' : 'settings.downloadDetail')}
      />
      {#if isAndroid}
        <div class="flex flex-col items-start gap-2.5">
          <div class="selectable font-mono text-sm break-all">{folder?.name ?? 'Download/TropicalPeer'}</div>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn-secondary" onclick={() => changeFolder(false)}>{t('settings.downloadChange')}</button>
            {#if folder?.custom}
              <button type="button" class="btn-quiet" onclick={() => changeFolder(true)}>{t('settings.downloadUseDefault')}</button>
            {/if}
          </div>
        </div>
      {:else}
        <div class="flex gap-2">
          <input class="field min-w-0 flex-1" bind:value={form.downloadDir} spellcheck="false" aria-label={t('settings.downloadFolder')} />
          <button type="button" class="btn-secondary" onclick={pickFolder}>{t('settings.browse')}</button>
        </div>
      {/if}
    </section>

    <section class="row">
      <SettingLabel
        title={t('settings.server')}
        hint={t('settings.serverHint')}
        detail={t('settings.serverDetail')}
      >
        <p class="mt-2 text-sm font-medium {serverTone}">{serverText}</p>
      </SettingLabel>
      <div>
        <div class="grid grid-cols-[1fr_7rem] gap-2">
          <label class="flex min-w-0 flex-col gap-1.5">
            <span class="text-sm text-muted">{t('settings.host')}</span>
            <input
              class="field min-w-0"
              bind:value={form.serverHost}
              placeholder={t('settings.hostPlaceholder')}
              spellcheck="false"
              autocomplete="off"
            />
          </label>
          <label class="flex min-w-0 flex-col gap-1.5">
            <span class="text-sm text-muted">{t('settings.port')}</span>
            <input class="field min-w-0" type="number" min="1" max="65535" bind:value={form.serverPort} />
          </label>
        </div>
        {#if app.server.error && !dirty}
          <p class="mt-2 text-sm text-bad">{app.server.error}</p>
        {/if}
      </div>
    </section>

    <section class="row">
      <SettingLabel
        title={t('settings.forwarding')}
        hint={t('settings.forwardingHint')}
        detail={t('settings.forwardingDetail')}
      />
      <div class="flex flex-col gap-2">
        <label class="flex cursor-pointer items-start gap-3">
          <input
            type="checkbox"
            class="mt-0.5 size-4 shrink-0 accent-[var(--primary)] pointer-coarse:size-5"
            bind:checked={form.portMapping}
          />
          <span class="text-sm">
            <span class="block font-medium">{t('settings.forwardingAsk')}</span>
            <!-- The state in a word; the reasons live on the Network page. -->
            <span class="block text-muted">
              {#if saved && !saved.portMapping}
                {t('settings.off')}
              {:else if app.network.forwarded}
                <span class="text-ok">{t('settings.active')}</span>
              {:else if app.network.mappingErr}
                {t('settings.notAvailable')}
              {:else}
                {t('settings.trying')}
              {/if}
            </span>
          </span>
        </label>
        <button
          type="button"
          class="w-fit text-sm font-medium text-primary hover:underline pointer-coarse:-my-1 pointer-coarse:py-2.5"
          onclick={() => (app.page = 'network')}
        >
          {t('settings.seeReachable')}
        </button>
      </div>
    </section>

    <section class="row" id="listen-port">
      <SettingLabel
        title={t('settings.listenPort')}
        hint={t('settings.listenHint')}
        detail={t('settings.listenDetail')}
      />
      <div class="flex flex-col items-start gap-2.5">
        <div class="flex items-center gap-3">
          <input class="field w-28" type="number" min="0" max="65535" bind:value={form.listenPort} aria-label={t('settings.listenPort')} />
          {#if app.device?.port}
            <span class="text-sm text-muted">{t('settings.nowUsing', { port: app.device.port })}</span>
          {/if}
        </div>
        {#if portPending && !app.restarting}
          <!-- Pending, not wrong: sea, the "still finding out" tone, with the fix beside it. -->
          <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-sm">
            <span class="text-sea">{t('settings.portPending', { port: saved!.listenPort })}</span>
            <button
              type="button"
              class="font-medium text-primary underline decoration-primary/40 underline-offset-4 hover:decoration-primary pointer-coarse:py-2"
              onclick={askRestart}
            >
              {t('settings.restartNow')}
            </button>
          </div>
        {/if}
      </div>
    </section>

    <section class="row">
      <SettingLabel title={t('settings.restart')} hint={t('settings.restartHint')} detail={t('settings.restartDetail')} />
      <div>
        <button type="button" class="btn-secondary" disabled={app.restarting} onclick={askRestart}>
          {#if app.restarting}
            <LoaderCircle size={16} class="animate-spin" /> {t('settings.restarting')}
          {:else}
            <RotateCw size={16} /> {t('settings.restartButton')}
          {/if}
        </button>
      </div>
    </section>

    {#if app.update && app.update.state !== 'unsupported'}
      {@const up = app.update}
      <section class="row" id="updates">
        <SettingLabel title={t('update.title')} hint={t('update.hint')} />
        <div class="flex flex-col items-start gap-3">
          <p class="text-sm {up.state === 'failed' ? 'text-bad' : up.state === 'ready' ? 'text-ok' : 'text-muted'}">
            {updateStatus(up)}
          </p>
          <label class="flex cursor-pointer items-start gap-3">
            <input
              type="checkbox"
              class="mt-0.5 size-4 shrink-0 accent-[var(--primary)] pointer-coarse:size-5"
              checked={up.auto}
              onchange={(e) => setAutoUpdate(e.currentTarget.checked)}
            />
            <span class="text-sm font-medium">{t('update.auto')}</span>
          </label>
          {#if up.state === 'ready' || up.state === 'installing'}
            <button type="button" class="btn-primary" onclick={installUpdate}>
              {t(isAndroid ? 'update.installNow' : 'update.restartNow')}
            </button>
          {:else}
            <button
              type="button"
              class="btn-secondary"
              disabled={up.state === 'checking' || up.state === 'downloading'}
              onclick={() => api.checkForUpdate()}
            >
              {#if up.state === 'checking' || up.state === 'downloading'}
                <LoaderCircle size={16} class="animate-spin" />
              {/if}
              {t('update.check')}
            </button>
          {/if}
        </div>
      </section>
    {/if}

    <section class="row">
      <SettingLabel title={t('settings.version')} hint={t('settings.versionHint')} />
      <p class="tabular-nums text-muted select-all">TropicalPeer {app.device?.version ?? ''}</p>
    </section>

    <!-- Phone: the bar rises only when there is something to save. -->
    {#if !phone || dirty || error || saving}
    <div
      class="sticky bottom-0 -mx-10 mt-2 flex items-center justify-end gap-3 border-t border-line bg-ground px-10 py-4
        max-sm:-mx-4 max-sm:flex-wrap max-sm:px-4 max-sm:py-3"
      transition:fly={{ y: 24, duration: phone ? dur(220) : 0, easing: easeOutExpo }}
    >
      {#if error}
        <p class="mr-auto text-sm text-bad max-sm:w-full">{error}</p>
      {:else if dirty}
        <p class="mr-auto text-sm text-muted max-sm:w-full">{t('settings.unsaved')}</p>
      {/if}
      <button type="button" class="btn-secondary" disabled={!dirty || saving} onclick={revert}>{t('settings.revert')}</button>
      <button type="submit" class="btn-primary" disabled={!dirty || saving}>
        {#if saving}<LoaderCircle size={16} class="animate-spin" />{/if}
        {t('settings.saveChanges')}
      </button>
    </div>
    {/if}
  </form>
</div>

{#if confirmRestart}
  <Dialog title={t('settings.restartConfirmTitle')} onclose={() => (confirmRestart = false)}>
    <p class="text-muted">{t('settings.restartConfirmBody', { count: running })}</p>
    <div class="mt-6 flex justify-end gap-3">
      <button class="btn-secondary" onclick={() => (confirmRestart = false)}>{t('common.cancel')}</button>
      <button
        class="btn-primary"
        onclick={() => {
          confirmRestart = false
          restartEngine()
        }}
      >
        <RotateCw size={16} /> {t('settings.restartButton')}
      </button>
    </div>
  </Dialog>
{/if}

<style>
  .seg-glide {
    transition:
      transform 300ms var(--ease-out-expo),
      width 300ms var(--ease-out-expo);
  }
  @media (prefers-reduced-motion: reduce) {
    .seg-glide {
      transition: none;
    }
  }
  .row {
    display: grid;
    grid-template-columns: minmax(0, 18rem) minmax(0, 1fr);
    align-items: start;
    gap: 2.5rem;
    padding-block: 1.5rem;
    border-top: 1px solid var(--line);
  }
  /* Narrow windows: label above value instead of side by side. */
  @media (max-width: 1000px) {
    .row {
      grid-template-columns: minmax(0, 1fr);
      gap: 0.75rem;
    }
  }
</style>
