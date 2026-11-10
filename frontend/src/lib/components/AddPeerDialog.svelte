<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { fade } from 'svelte/transition'
  import { Copy, Check, LoaderCircle, RefreshCw, CircleCheckBig, Ticket, Keyboard, ChevronRight } from '@lucide/svelte'
  import Dialog from './Dialog.svelte'
  import Bed from './Bed.svelte'
  import WaveBand from './WaveBand.svelte'
  import { api, on, errorText, type Invite, type Peer } from '../api'
  import { toast } from '../state.svelte'
  import { t } from '../i18n.svelte'

  let { onclose }: { onclose: () => void } = $props()

  // The dialog first asks whether the other device already shows a code, so
  // nobody makes a ticking code they didn't need. After that the two ways are
  // tabs, to switch if they guessed wrong.
  type Tab = 'show' | 'enter'
  let tab = $state<Tab | null>(null)
  let paired = $state<Peer | null>(null)

  // ---- "Show my code" ----
  let invite = $state<Invite | null>(null)
  let inviteStatus = $state<'loading' | 'waiting' | 'failed'>('loading')
  let inviteError = $state('')
  let now = $state(Date.now())
  let copied = $state(false)

  const totalMs = 5 * 60 * 1000
  const remainingMs = $derived(invite ? Math.max(0, invite.expiresAt - now) : totalMs)
  const remainingText = $derived(
    `${Math.floor(remainingMs / 60000)}:${String(Math.floor((remainingMs % 60000) / 1000)).padStart(2, '0')}`,
  )

  async function startInvite() {
    invite = null
    inviteError = ''
    inviteStatus = 'loading'
    try {
      invite = await api.startInvite()
      inviteStatus = 'waiting'
    } catch (e) {
      inviteError = errorText(e)
      inviteStatus = 'failed'
    }
  }

  async function copyCode() {
    if (!invite) return
    await navigator.clipboard.writeText(invite.code)
    copied = true
    setTimeout(() => (copied = false), 1500)
  }

  // ---- "Enter a code" ----
  let codeInput = $state('')
  let joining = $state(false)
  let joinError = $state('')
  let codeField: HTMLInputElement | undefined = $state()

  const codeLooksValid = $derived(/^\s*\d+[\s-]+[a-z]+[\s-]+[a-z]+\s*$/i.test(codeInput))
  // Said quietly once something is typed, so a disabled Pair has a reason.
  const codeHint = $derived(codeInput.trim() !== '' && !codeLooksValid)

  async function join(e: SubmitEvent) {
    e.preventDefault()
    if (!codeLooksValid || joining) return
    joining = true
    joinError = ''
    try {
      onPaired(await api.joinWithCode(codeInput.trim()))
    } catch (err) {
      joinError = errorText(err)
    } finally {
      joining = false
    }
  }

  function onPaired(p: Peer) {
    paired = p
    toast(t('pair.pairedWith', { name: p.name }))
  }

  // ---- Tab switching & lifecycle ----
  const tabs = [
    ['show', 'pair.showCode', Ticket],
    ['enter', 'pair.enterCode', Keyboard],
  ] as const
  const tabEls: Record<string, HTMLButtonElement | undefined> = $state({})

  function selectTab(next: Tab) {
    if (next === tab) return
    const from = tab
    tab = next
    if (next === 'show') {
      startInvite()
      // The choice row that had focus is gone; the tab it led to takes over.
      if (from === null) setTimeout(() => tabEls.show?.focus(), 0)
    } else {
      if (from === 'show') api.cancelInvite()
      invite = null
      setTimeout(() => codeField?.focus(), 0)
    }
  }

  // Arrow keys move between the two tabs, as in any tab list.
  function onTabKey(e: KeyboardEvent) {
    const keys: Record<string, Tab> = { ArrowLeft: 'show', ArrowRight: 'enter', Home: 'show', End: 'enter' }
    const next = keys[e.key]
    if (!next) return
    e.preventDefault()
    selectTab(next)
    tabEls[next]?.focus()
  }

  const unsubs: (() => void)[] = []
  let timer: ReturnType<typeof setInterval>

  onMount(() => {
    unsubs.push(
      on('pair:done', onPaired),
      on('pair:failed', (msg) => {
        inviteError = msg
        inviteStatus = 'failed'
      }),
    )
    timer = setInterval(() => (now = Date.now()), 250)
  })

  onDestroy(() => {
    unsubs.forEach((u) => u())
    clearInterval(timer)
    if (!paired && tab === 'show') api.cancelInvite()
  })
</script>

<Dialog title={t('pair.title')} {onclose}>
  {#if paired}
    <!-- Success -->
    <div class="flex flex-col items-center gap-4 py-4 text-center" in:fade={{ duration: 200 }}>
      <div class="relative">
        <Bed name={paired.name} id={paired.id} size={72} />
        <div class="absolute -right-1 -bottom-1 grid size-7 place-items-center rounded-full bg-ok text-white ring-4 ring-surface">
          <CircleCheckBig size={16} />
        </div>
      </div>
      <div>
        <div class="display text-lg font-medium">{t('pair.pairedWith', { name: paired.name })}</div>
        <div class="font-mono text-xs text-muted">{paired.id}</div>
      </div>
      <p class="max-w-xs text-sm text-muted">{t('pair.askNote', { name: paired.name })}</p>
      <button class="btn-primary mt-2 w-full" onclick={onclose}>{t('common.done')}</button>
    </div>
  {:else if tab === null}
    <!-- First: which side are we? -->
    <h3 class="display mb-3 text-base font-medium">{t('pair.choose')}</h3>
    <div class="-mx-6 flex flex-col divide-y divide-line border-t border-line max-sm:-mx-5">
      {#each [['enter', 'pair.chooseEnter', 'pair.chooseEnterDesc', Keyboard], ['show', 'pair.chooseShow', 'pair.chooseShowDesc', Ticket]] as const as [id, label, desc, Icon] (id)}
        <button
          class="group flex items-center gap-4 px-6 py-4 text-left transition-colors duration-150 hover:bg-surface-2 max-sm:px-5"
          onclick={() => selectTab(id)}
        >
          <Icon size={20} strokeWidth={1.75} class="shrink-0 text-primary" />
          <span class="flex min-w-0 flex-1 flex-col gap-0.5">
            <span class="font-medium">{t(label)}</span>
            <span class="text-sm text-muted">{t(desc)}</span>
          </span>
          <ChevronRight
            size={18}
            class="shrink-0 text-muted transition-transform duration-200 group-hover:translate-x-0.5 group-hover:text-ink"
          />
        </button>
      {/each}
    </div>
  {:else}
    <!-- Tabs -->
    <div
      class="relative mb-5 grid grid-cols-2 gap-1 rounded-lg bg-surface-2 p-1"
      role="tablist"
      aria-label={t('pair.tabs')}
      tabindex="-1"
      onkeydown={onTabKey}
    >
      <!-- The raised tab slides across rather than jumping. -->
      <span
        class="tab-fill pointer-events-none absolute top-1 bottom-1 left-1 w-[calc(50%-6px)] rounded-md bg-surface shadow-card"
        style:transform={tab === 'enter' ? 'translateX(calc(100% + 4px))' : 'none'}
        aria-hidden="true"
      ></span>
      {#each tabs as [id, label, Icon] (id)}
        <button
          bind:this={tabEls[id]}
          id="pair-tab-{id}"
          role="tab"
          aria-selected={tab === id}
          aria-controls="pair-panel"
          tabindex={tab === id ? 0 : -1}
          class="relative flex items-center justify-center gap-2 rounded-md py-2 text-sm font-medium transition-colors duration-200
            {tab === id ? 'text-ink' : 'text-muted hover:text-ink'}"
          onclick={() => selectTab(id)}
        >
          <Icon size={16} />
          {t(label)}
        </button>
      {/each}
    </div>

    <div id="pair-panel" role="tabpanel" aria-labelledby="pair-tab-{tab}">
      {#if tab === 'show'}
        <div class="flex flex-col items-center gap-4 text-center">
          {#if inviteStatus === 'failed'}
            <div class="flex min-h-48 flex-col items-center justify-center gap-3">
              <p class="max-w-xs text-sm text-bad">{inviteError}</p>
              <button class="btn-secondary" onclick={startInvite}><RefreshCw size={16} /> {t('pair.newCode')}</button>
            </div>
          {:else}
            <div class="w-full">
              <div class="mb-2 text-sm text-muted">{t('pair.enterThis')}</div>
              <div class="flex items-center gap-2 rounded-lg bg-ground p-2 pl-4">
                <div class="selectable flex-1 text-left font-mono text-2xl font-semibold tracking-wide">
                  {invite?.code ?? '·····'}
                </div>
                <button
                  class="rounded-md p-2 text-muted hover:bg-surface-2 hover:text-ink disabled:opacity-40"
                  aria-label={t('pair.copyCode')}
                  disabled={!invite}
                  onclick={copyCode}
                >
                  {#if copied}<Check size={18} class="text-ok" />{:else}<Copy size={18} />{/if}
                </button>
              </div>
            </div>

            <div class="flex w-full flex-col gap-2">
              <WaveBand
                value={remainingMs / totalMs}
                waiting={inviteStatus === 'loading'}
                height={8}
                label={t('pair.timeLeft')}
              />
              <div class="flex flex-wrap justify-between gap-x-4 gap-y-0.5 text-sm text-muted">
                <span>{t('pair.waiting')}</span>
                {#if inviteStatus === 'waiting'}<span class="tabular-nums">{t('pair.expiresIn', { time: remainingText })}</span>{/if}
              </div>
            </div>

            <p class="w-full border-t border-line pt-4 text-left text-sm text-muted">
              {t('pair.otherDevice', { add: t('peers.add'), enter: t('pair.chooseEnter') })}
            </p>
          {/if}
        </div>
      {:else}
        <form class="flex flex-col gap-4" onsubmit={join}>
          <label class="flex flex-col gap-2">
            <span class="text-sm text-muted">{t('pair.typeCode')}</span>
            <input
              bind:this={codeField}
              bind:value={codeInput}
              class="field text-center font-mono text-xl tracking-wide"
              placeholder="7-orchid-tiger"
              spellcheck="false"
              autocomplete="off"
              aria-describedby="pair-code-hint"
              disabled={joining}
            />
          </label>
          {#if joinError}
            <p class="text-sm text-bad" role="alert">{joinError}</p>
          {:else if codeHint}
            <p id="pair-code-hint" class="text-sm text-muted">{t('pair.codeShape')}</p>
          {/if}
          <button class="btn-primary" type="submit" disabled={!codeLooksValid || joining}>
            {#if joining}<LoaderCircle size={16} class="animate-spin" /> {t('pair.pairing')}{:else}{t('pair.pair')}{/if}
          </button>
          <p class="border-t border-line pt-4 text-sm text-muted">
            {t('pair.noCode', { add: t('peers.add'), show: t('pair.chooseShow') })}
          </p>
        </form>
      {/if}
    </div>
  {/if}
</Dialog>

<style>
  .tab-fill {
    transition: transform 300ms var(--ease-out-expo);
  }
  @media (prefers-reduced-motion: reduce) {
    .tab-fill {
      transition: none;
    }
  }
</style>
