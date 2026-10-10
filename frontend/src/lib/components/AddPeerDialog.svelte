<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { fade } from 'svelte/transition'
  import { Copy, Check, LoaderCircle, RefreshCw, CircleCheckBig, Ticket, Keyboard } from '@lucide/svelte'
  import Dialog from './Dialog.svelte'
  import Bed from './Bed.svelte'
  import WaveBand from './WaveBand.svelte'
  import { api, on, errorText, type Invite, type Peer } from '../api'
  import { toast } from '../state.svelte'
  import { t } from '../i18n.svelte'

  let { onclose }: { onclose: () => void } = $props()

  type Tab = 'show' | 'enter'
  let tab = $state<Tab>('show')
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
  function selectTab(next: Tab) {
    if (next === tab) return
    tab = next
    if (next === 'show') {
      startInvite()
    } else {
      api.cancelInvite()
      invite = null
      setTimeout(() => codeField?.focus(), 0)
    }
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
    startInvite()
  })

  onDestroy(() => {
    unsubs.forEach((u) => u())
    clearInterval(timer)
    if (!paired) api.cancelInvite()
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
  {:else}
    <!-- Tabs -->
    <div class="relative mb-5 grid grid-cols-2 gap-1 rounded-lg bg-surface-2 p-1" role="tablist">
      <!-- The raised tab slides across rather than jumping. -->
      <span
        class="tab-fill pointer-events-none absolute top-1 bottom-1 left-1 w-[calc(50%-6px)] rounded-md bg-surface shadow-card"
        style:transform={tab === 'enter' ? 'translateX(calc(100% + 4px))' : 'none'}
        aria-hidden="true"
      ></span>
      {#each [['show', 'pair.showCode', Ticket], ['enter', 'pair.enterCode', Keyboard]] as const as [id, label, Icon] (id)}
        <button
          role="tab"
          aria-selected={tab === id}
          class="relative flex items-center justify-center gap-2 rounded-md py-2 text-sm font-medium transition-colors duration-200
            {tab === id ? 'text-ink' : 'text-muted hover:text-ink'}"
          onclick={() => selectTab(id)}
        >
          <Icon size={16} />
          {t(label)}
        </button>
      {/each}
    </div>

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
            <div class="flex items-center gap-2 rounded-lg border border-line bg-ground p-2 pl-4">
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
            disabled={joining}
          />
        </label>
        {#if joinError}
          <p class="text-sm text-bad">{joinError}</p>
        {/if}
        <button class="btn-primary" type="submit" disabled={!codeLooksValid || joining}>
          {#if joining}<LoaderCircle size={16} class="animate-spin" /> {t('pair.pairing')}{:else}{t('pair.pair')}{/if}
        </button>
      </form>
    {/if}
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
